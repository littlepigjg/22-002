# 缺陷复现报告（Bug Reproduction Report）

## 1. 问题概述（Summary）
固件升级平台在将设备轮询层改为"细粒度并发"后，高并发场景下（多设备多协程同时发起 Poll 轮询 + 后台 worker 同步上报升级进度）出现偶发的进程级崩溃。报错信息为 "concurrent map read and map write" 致命 panic，使用 -race 运行时还会检测到多处数据竞争（DATA RACE）。单线程或低并发下业务流程正常，一旦并发提升到接近线上水平（几十上百台设备并发轮询、每台多 goroutine 反复请求、后台持续写进度），崩溃可稳定复现。

## 2. 环境信息（Environment）
- 操作系统：Linux（任意主流发行版，如 Ubuntu 22.04 LTS x86_64；Debian 12；CentOS 8+ 等）
- Go 版本：Go 1.22+（与 go.mod 中 `go 1.22` 声明一致，建议 `go version go1.22.x linux/amd64`）
- 项目模块：module firmware-upgrade，无第三方依赖（纯标准库 + 项目内部包）
- 运行参数：必须带 -race 开启竞争检测；-count=5 用于多次执行捕获偶发；CPU 核数 ≥4 时更容易触发
- 硬件信息（相关）：多核 CPU（4 核及以上效果显著，核数越多并发窗口越大，崩溃概率越高）；内存 ≥2GB

## 3. 复现步骤（Steps to Reproduce）
1. 进入项目根目录，执行 `go build ./...` 确保所有包编译通过；
2. 执行 `go vet ./...` 确认无静态检查错误；
3. 执行 `go test -c -o /dev/null .` 确认测试包（含 red_green_test.go）可成功编译，无签名不一致问题；
4. 运行并发验收命令：`go test . -race -count=5 -run '^TestRedGreen$'`；
   - 该命令内部会：创建一个 running 的全量升级任务，注册数十台设备，预填充部分执行记录，随后并发启动多组 Poll 请求与持续的 UpdateProgress 写入，模拟前台服务 + worker 同步运行的真实场景；
   - 同时直接对 safemap 做并发 ForEach 读 + Set/Delete 写的对撞压力测试；
5. 观察标准输出/标准错误，重点关注 "WARNING: DATA RACE"、"fatal error: concurrent map read and map write"、以及堆栈信息与测试退出码；
6. 若单次执行因并发时序未命中未崩，可提高 -count 到 10 或 20 再复现（-race 条件下 race detector 会先于 panic 报告 race）。

## 4. 实际结果（Actual Behavior / Observed Output）
- 致命 panic：典型报错如下（已裁剪无关堆栈段）：
  ```
  fatal error: concurrent map read and map write

  goroutine 153 [running]:
  internal/runtime/maps.fatal(...)
          /usr/local/go/src/runtime/panic.go:1181 +0x18
  firmware-upgrade/pkg/safemap.(*Map[...]).ForEach(...)
          pkg/safemap/safemap.go:94 +0x353
  firmware-upgrade/internal/store.(*inMemoryTaskExecStore).FindAssignedRunning(...)
          internal/store/task_exec_store.go:198 +0x1ad
  firmware-upgrade/internal/service.(*PollService).Poll(...)
          internal/service/poll_service.go:98 +0x89e
  firmware-upgrade_test.TestRedGreen.func3(...)
          red_green_test.go:141 +0x2e5
  ```
- 数据竞争：加 -race 后大量 WARNING: DATA RACE，读侧位于 ForEach 内对执行记录底层 map 的访问，写侧位于 UpdateProgress / Upsert 写入同一份表；
- RED/GREEN 判定：缺陷未修复时为 RED（红灯，缺陷未修复），测试 FAIL，未出现 GREEN 字样；
- 其他异常：进程异常退出（throw 不可 recover），已发出的 Poll 响应部分或全部丢失，压力端观察到 EOF / 连接重置；
- go test -race 报告：每轮均报告多处 race，最终以 exit code 1 或 2 结束。

## 5. 期望结果（Expected Behavior）
- 无 panic、无致命 throw：高并发 Poll + UpdateProgress 对撞下，绝对不会出现 concurrent map read/write 类 panic；
- 无数据竞争：`go test . -race -count=5 -run '^TestRedGreen$'` 完整运行 5 轮，全程无任何 "WARNING: DATA RACE"，竞争检测清零；
- RED/GREEN 判定：所有轮次结束时测试输出均打印 "GREEN（绿灯，缺陷已修复）"，go test 退出码为 0；
- 业务正确：Poll 对于已分配升级中设备正确返回下载信息、对于新设备按灰度正确分配执行记录，UpdateProgress 上报进度写入正确，不丢写、不乱序；
- 静态检查通过：`go build ./...` 与 `go vet ./...` 均全绿无报错。

## 6. 触发频率（Frequency）
- 在 `go test -race -count=5 -run '^TestRedGreen$'` 条件下：崩溃 + race 出现概率接近 100%（至少一轮中出现 panic 或 race），race detector 先于 panic 报告；
- 不带 -race 直接运行：并发压力下约 30%~70% 概率触发 fatal error: concurrent map read and map write，属于高概率偶发；
- 单核环境或极低并发（串行调用）：基本不复现；
- 随着并发数、设备数、迭代次数、CPU 核数增加，复现概率趋近于 100%。

## 7. 影响范围（Impact / Scope）
- 服务可用性：并发上来直接 panic 崩溃，前台 HTTP 请求中断返回 500/EOF，升级 worker 进程退出无法恢复，升级任务大面积停摆；
- 数据一致性：崩溃发生在 Upsert/UpdateProgress 写入中途会导致执行记录与历史记录不同步，进度回退或状态残留（pending 卡住无法推进）；
- 资源泄漏：并发竞争窗口内如果处于通道、上下文资源的持有阶段，panic 可能导致关联 goroutine 无法正常退出、通道未关闭（虽然 Go 最终会 GC，但在高频重启场景下会放大连接资源耗尽风险）；
- 上线风险：无法承受真实生产数百台设备同时 Poll 的压力，灰度发布一旦放量就会触发级联崩溃；
- 诊断难度：panic stack 与 race 告警混杂，如果不通过 -race 多次复现容易误判为某个上层 store 的问题，排查路径长。

## 8. 附加说明（Additional Notes / Workaround）
- 临时规避方法：在设备 Poll 接口外层加全局互斥锁把所有 Poll 串行化，或者强制单 worker 跑 UpdateProgress；此方法可以临时规避 panic，但会把并发吞吐降到单核级别，严重影响线上性能，仅作为 rollback 前的热补丁；
- 临时观测手段：如果现场允许加 -race 运行，可以通过 race detector 输出快速定位读写两侧的竞争对，辅助缩小排查范围；
- 相关日志样例：所有 panic 和 race 堆栈都会分别打印读 goroutine 与写 goroutine 的创建点，需要对比两边的栈共同确定调用链。若现场没有 -race，需要在 crash log 中把 concurrent map read and map write 的 goroutine 栈完整保留。
