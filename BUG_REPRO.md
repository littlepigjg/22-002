# 缺陷复现报告（Bug Reproduction Report）

## 1. 问题概述（Summary）
设备固件升级管理平台的设备轮询（Poll）接口在处理个别设备上报时直接崩溃，抛出 nil pointer dereference panic；现场表现为该设备的轮询请求持续 5xx，直到运维手动清理脏数据才能恢复，而其他设备的轮询、创建/发布任务、设备注册、固件下载等其余功能均正常。初步怀疑是设备对应的任务执行记录中存在畸形数据。

## 2. 环境信息（Environment）
- 操作系统：Linux（任意发行版均可复现，本报告验证环境为 Ubuntu 20.04/22.04 系内核）
- Go 版本：Go 1.22 或以上（`go version` 输出形如 `go version go1.22.x linux/amd64`）
- 项目模块：module `firmware-upgrade`，纯内存实现，无外部数据库依赖
- 运行参数：`go test . -count=1 -run '^TestRedGreen$'`；无需 -race，缺陷属必现逻辑 nil 解引用
- 硬件信息：CPU 任意核数均可，与并发/性能无关

## 3. 复现步骤（Steps to Reproduce）
1. 进入项目根目录 `cd firmware-upgrade`。
2. 执行 `go build ./...` 保证项目能完整编译；再执行 `go vet ./...` 确认无静态错误。
3. （可选）执行 `go test -c -o /dev/null .` 保证测试入口与被测代码签名完全一致。
4. 在执行存储中为指定 DeviceID 写入一条 **TaskID 为空字符串**、`Status=pending`、`AssignedAt` 为最近时间的 `TaskDeviceExecution` 记录（模拟历史版本迁移残留/修复脚本写脏的场景）。
5. 为同一 DeviceID 注册好设备（型号存在、版本号可匹配某条 running 升级任务），并准备好对应的 published 固件与 running 状态升级任务。
6. 调用 `PollService.Poll` 向该 DeviceID 发起一次设备轮询请求。
7. 观察 Poll 返回值/进程 panic 堆栈。

## 4. 实际结果（Actual Behavior / Observed Output）
- 关键 panic 信息：
  ```
  panic: runtime error: invalid memory address or nil pointer dereference
  goroutine running:
    firmware-upgrade/internal/service.(*PollService).buildResponse
      internal/service/poll_service.go:169
    firmware-upgrade/internal/service.(*PollService).Poll
      internal/service/poll_service.go:85
  ```
- RED/GREEN 判定：缺陷未修复时，red_green_test.go 判定输出为 **RED（红灯，缺陷未修复）**，go test 失败退出码为 1。
- 其他异常：该 DeviceID 后续每一次 Poll 都会重复崩溃，无法降级/跳过；即便系统中仍有可分配的 running 升级任务，也不会再触达正常分配分支。
- 本缺陷非并发问题，`go test -race` 不会报告 DATA RACE。

## 5. 期望结果（Expected Behavior）
- 即使存在 TaskID 为空的脏执行记录，Poll 也不得 panic，必须返回有效响应（Resp 非 nil、err 非 nil 或走 NeedUpgrade=false 分支均可，唯一禁止是崩溃）。
- red_green_test.go 判定输出为 **GREEN（绿灯，缺陷已修复）**，go test 退出码为 0。
- 业务上的正确行为至少应满足其一：(a) 跳过空 TaskID 脏记录，走 ListRunning 的正常分配流程，返回真实存在的任务下载信息（NeedUpgrade=true，下载链接/MD5/版本齐全）；或 (b) 明确拒绝空 TaskID 记录，返回 NeedUpgrade=false，并在 message 里说明无法命中升级。两种行为都不能 panic。
- `go build ./...` 与 `go vet ./...` 全部通过；`go test -c -o /dev/null .` 成功。

## 6. 触发频率（Frequency）
必现（100%）：只要设备在 byDevice 索引下有一条 TaskID=="" 且处于 pending/downloading/verifying/upgrading 或空状态的执行记录，Poll 调用立即 100% 崩溃。

## 7. 影响范围（Impact / Scope）
- 线上：单台或小批设备会因脏执行记录持续 5xx，无法进行正常 OTA，也无法被重新分配新的升级任务；如果脏数据在批量迁移后扩散，会出现大面积设备轮询接口崩溃，升级系统可用性下降。
- 服务端：panic 发生在 HTTP handler 未 recover 的情况下，会导致单次请求直接断连；有 HTTP server recover 时也会不断把 nil dereference 打进日志，掩盖真实错误根因。
- 状态：脏执行记录不会被自动清理，缺陷是永久性的，除非运维介入，否则设备无法自愈。

## 8. 附加说明（Additional Notes / Workaround）
- 临时 workaround（非修复）：在数据库/存储层找到该 DeviceID 对应的所有任务执行记录，删除 TaskID 为空的条目后，设备 Poll 即可恢复；但这是事后人工规避，根因仍在代码里，再次写入空 TaskID 脏记录时会立刻复发。
- 附加日志样例提示：若启用 debug 级别日志，poll 进入 FindAssignedRunning 返回 ok=true 后，日志里应能读到 TaskID 字段为空，但 Poll 仍继续进入 buildResponse 才崩。
