# 缺陷复现报告（Bug Reproduction Report）

## 1. 问题概述（Summary）
固件升级管理系统在高并发场景下，任务执行记录（TaskDeviceExecution）的查询结果与全量执行快照对不上。具体表现为：对同一批 task-device 组合在并发写 + 周期性过期清理 + 按任务删除之后，带缓存的 Get 接口和「执行记录快照」接口分别返回互斥的存在性/进度信息——一部分条目在快照里有、但 Get 查不到或进度不一致；另一部分条目 Get 能返回、但快照里不存在。此外在启用 Go 竞态检测器时会反复报出 `WARNING: DATA RACE`，堆栈集中在通用缓存模块的读/淘汰路径。串行跑业务接口时一切正常，只有并发场景下才能复现，导致线上批量设备升级过程中偶发"进度丢失/重复"告警。

## 2. 环境信息（Environment）
- 操作系统：Linux（内核 5.x+，任意发行版均可复现；也可在 macOS 12+ 复现）
- Go 版本：`go version go1.21.x linux/amd64` 及以上（含 Go 1.22/1.23）
- 项目模块/依赖：`firmware-upgrade`（自有模块，`pkg/cache` 通用 TTL 缓存 + `internal/store` 执行记录存储 + `internal/service/progress_service` 进度上报服务），第三方依赖仅标准库 + Go runtime
- 运行参数：必须带 `-race`，`-count=20`（条款要求的偶发竞态捕获重复次数），`-timeout 30s`
- 硬件信息（相关）：CPU 核数 >= 2（单核环境下 race detector 采样率与并发重叠率下降，仍可复现但命中率偏低）

## 3. 复现步骤（Steps to Reproduce）
1. 进入项目根目录并完成前置编译门禁：
   ```
   cd /home/admin/code/22/22-002-6
   go build ./...
   go vet ./...
   go test -c -o /dev/null .
   ```
   以上三步需全部通过。
2. 执行并发缺陷检测验证命令（带竞态检测 + 20 次重复，确保命中偶发窗口）：
   ```
   go test -race . -count=20 -run '^TestRedGreen$' -timeout 30s
   ```
3. 观察终端输出并关注三类关键信息：
   - 是否出现 `WARNING: DATA RACE` 块（含堆栈）；
   - 是否出现 `testing.go: race detected during execution of test` 语句；
   - 是否出现 `RED（红灯，缺陷未修复）` / `RED: N consistency failures detected`。
4. （人工辅助复核）若希望在压测场景手动对照：先并发写入 N 组相同 task-device 不同进度，再周期性 Purge，然后分别走 ExecSnapshot 全量快照与逐条 GetExecWithGuard，统计 "A 有 B 无 / B 有 A 无 / progress 不一致" 的条数。

## 4. 实际结果（Actual Behavior / Observed Output）
按上述步骤执行后，通常观察到：

- `WARNING: DATA RACE` 反复出现，典型堆栈片段：
  ```
  ==================
  WARNING: DATA RACE
  Read at 0x... by goroutine N:
    pkg/cache.(*Cache[...]).Get()
        .../pkg/cache/cache.go:127
  Previous write at 0x... by goroutine M:
    pkg/cache.(*Cache[...]).Get()
        .../pkg/cache/cache.go:127
  ==================
  ```
- 部分轮次出现快照与 Get 双向断言不匹配，关键输出：
  ```
  total_gets=3200 failures=1
  RED（红灯，缺陷未修复）: exec store+cache 发生 1 次键值一致性失败；快照与带缓存 Get 结果不匹配
  --- FAIL: TestRedGreen (0.XXs)
      red_green_test.go:NN: RED: 1 consistency failures detected
  ```
- 整体判定：`FAIL firmware-upgrade  X.XXXs` 并最终 `FAIL`；20 轮内 FAIL/RED 命中数约 30%~70%（>= 1 次即可视为稳定复现）。
- `go test -race` 稳定报告 DATA RACE（属于并发缺陷的直接证据）。
- 偶发现象：`ExecSnapshot()` 返回空的概率极低但存在，此时测试会直接 Fatalf。

## 5. 期望结果（Expected Behavior）
修复后按同一步骤执行应出现：

- 无 `WARNING: DATA RACE`、无 `race detected during execution of test` 警告；
- 每一轮末尾均打印：
  ```
  total_gets=3200 failures=0
  GREEN（绿灯，缺陷已修复）: 所有并发写+淘汰+删除+读路径下键值一致性通过
  ```
- 整包退出码为 0：`ok  firmware-upgrade  X.XXXs`（无 `FAIL firmware-upgrade`）；
- 20 轮全部通过，无任何 panic、无 goroutine 死锁/卡住；
- `go build ./...`、`go vet ./...`、`go test -c -o /dev/null .` 三项门禁仍保持通过（回归确认无签名破坏）；
- 人工复核时：ExecSnapshot 与带缓存 Get 结果 100% 双向一致，不再出现 A/B 互斥存在或进度不一致的情况。

## 6. 触发频率（Frequency）
- 并发 + race detector 模式（`-race -count=20`）：偶发，约 30%~70% 轮次命中（至少 >= 1 次，20 轮内通常可稳定捕捉）；多核机器命中率更高。
- 非 `-race` 模式（`go test . -count=20`）：偶发，键值一致性问题（orphan/误删）可能在少数轮次触发，计数器竞态不会暴露为 WARNING 但依然存在；不建议使用此模式作为最终验收。
- 串行调用：几乎不会出现（race window 完全没机会并发）。

## 7. 影响范围（Impact / Scope）
- 一致性风险：固件升级执行记录出现"缓存里有但真实记录没有 / 缓存新值被旧快照回滚删除"的键值不一致，导致进度上报漏记、重复记或错误进度被误判升级超时，触发大量设备端重试；
- 可用性风险：`-race` 下直接 FAIL，线上如果开启 race 诊断构建会导致压测/灰度流程中断；
- 性能/健壮性：高并发下过期淘汰路径的跨锁竞态可能使缓存命中率出现异常抖动，在 Purge 较频繁时放大整体读写延迟，严重时可导致任务批量任务状态异常（"明明还在升级却被判未执行"），进而影响固件升级批次的完成率统计与 rollout 决策。

## 8. 附加说明（Additional Notes / Workaround）
临时规避方案：
1. 禁用主动 Purge（调用频率降为 0）或降低 Purge 频率，可减少 Purge/Get 跨锁窗口的命中次数，但不能彻底消除 Save/Delete 之间的双写顺序不一致；
2. 压测或生产热修场景下，可在每次读操作后强制绕过缓存直接读持久态快照，绕过 Get 的 cache-first 路径，从业务层把不一致率降到接近 0，代价是读性能下降 ~30%~50%。
其他说明：缺陷完全属于纯逻辑实现，不涉及公开签名/结构体字段修改，不依赖任何第三方服务或外部资源，本地即可复现。
