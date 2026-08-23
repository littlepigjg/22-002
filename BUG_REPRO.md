# 缺陷复现报告（Bug Reproduction Report）

## 1. 问题概述（Summary）
固件升级服务在以「空配置快速装配」方式启动后，每当设备上报升级成功（progress=100、status=success）或其他终态（失败、取消），以及对任务做状态切换、删除、超时扫描等操作时，服务进程直接因空指针 panic 崩溃。正常带配置启动的流程无此问题，仅在特定装配场景下稳定复现，影响升级任务的进度上报和任务管理可用性。

## 2. 环境信息（Environment）
- 操作系统：Linux（任意支持 Go 的发行版）
- Go 版本：go 1.22 及以上
- 项目模块/依赖：module firmware-upgrade（纯标准库+内部包，无第三方依赖）
- 运行参数：无外部依赖，内存存储模式即可复现；无需 -race
- 硬件信息（如与并发/性能相关可补充）：任意 CPU 架构

## 3. 复现步骤（Steps to Reproduce）
1. 进入项目根目录，执行 go build ./... 与 go vet ./... 确保编译与静态检查通过；
2. 在项目根目录执行 go test -c -o /dev/null . 确认测试包签名编译一致；
3. 执行复现命令：go test . -count=1 -run '^TestRedGreen$' -v；
4. 也可手动在代码中复现：
   4.1 调 service.NewServices(nil, nil) 构建整套服务；
   4.2 建一个设备型号（ModelStore.Create）；
   4.3 建一个已发布（FirmwarePublished）的对应型号固件（FirmwareStore.Create）；
   4.4 注册一台同型号设备（DeviceStore.Create）；
   4.5 通过 TaskService.Create 以 StrategyDeviceList 指定上述设备创建升级任务；
   4.6 在 ExecStore 中手动 Upsert 一条该任务+该设备的执行记录（状态任意，如 Upgrading，Progress=50）；
   4.7 在 HistoryStore 中手动 Create 一条对应升级历史（状态 Upgrading、Progress=50，StartedAt 非零）；
   4.8 调用 ProgressService.Report 提交 ReportProgressRequest{TaskID, DeviceID, Status=UpgradeStatusSuccess, Progress=100, MD5Verified=true}。

## 4. 实际结果（Actual Behavior / Observed Output）
- 关键 panic 信息：
```
panic: runtime error: invalid memory address or nil pointer dereference
[signal SIGSEGV: segmentation violation ...]
goroutine ... [running]:
firmware-upgrade/internal/service.(*StatsService).Invalidate(0x0)
    .../internal/service/stats_service.go:... +0x...
firmware-upgrade/internal/service.(*ProgressService).Report(...)
    .../internal/service/progress_service.go:...
...
```
- RED/GREEN 判定结果：RED（红灯，缺陷未修复）
- 其他异常现象：UpdateStatus(cancel/finish)、TaskService.Delete、ProgressService.ScanTimeout、RefreshProgress、StartDueTasks 也会在相同调用栈附近 panic，症状完全一致。
- 本缺陷不涉及并发竞争，go test -race 下不会额外报告 DATA RACE（除非另有并发代码）。

## 5. 期望结果（Expected Behavior）
- 无 panic、无崩溃；即使 nil cfg 快速装配，报告升级成功/失败/取消、任务状态切换、任务删除、超时扫描等业务流程也能正常完成返回；
- RED/GREEN 判定结果应为 GREEN；
- Progress 上报后执行记录 progress=100，status=success，对应历史记录终态正确、FinishedAt 写入；任务统计缓存按正常路径刷新或被安全跳过；
- go build ./... 与 go vet ./... 全部通过；go test . -count=1 -run '^TestRedGreen$' 通过，退出码为 0。

## 6. 触发频率（Frequency）
必现（100%）。只要在 NewServices 装配时传 cfg=nil，后续命中 Report/UpdateStatus/Delete/ScanTimeout 等代码路径的统计失效调用就会崩溃。

## 7. 影响范围（Impact / Scope）
升级进度上报链路不可用：设备成功升级后无法上报终态，导致任务进度永远停在 99%，设备版本也可能得不到更新；任务取消/结束/删除等管理操作会直接挂掉进程，管理后台无法使用；超时扫描 worker 一旦跑起来也会立刻 panic 退出，整个服务存在崩溃重启循环风险，线上可用性显著下降。

## 8. 附加说明（Additional Notes / Workaround）
临时规避：构建服务时确保传非 nil 的 config.Config（例如 config.Default()），不要走 NewServices(cfg=nil) 的快速装配路径；已运行进程遇到崩溃后重启并更换启动方式可暂时止血，但无法解决快速启动集成脚本、单测脚手架、某些嵌入式场景下默认传 nil 的调用方。
