# 缺陷复现报告（Bug Reproduction Report）

## 1. 问题概述（Summary）
固件升级服务在后台执行超时扫描任务时，针对特定场景会触发空指针解引用 panic 导致服务崩溃。该场景为：设备已被分配升级任务（存在执行记录），但尚未进行任何进度上报（无历史记录），当超时扫描逻辑处理到该设备时即触发崩溃。

## 2. 环境信息（Environment）
- 操作系统：Linux
- Go 版本：go 1.22
- 项目模块：firmware-upgrade
- 运行参数：go test . -count=1 -run '^TestRedGreen$'
- 硬件信息：不涉及特定硬件

## 3. 复现步骤（Steps to Reproduce）
1. 进入项目根目录，执行 `go build ./...` 确保编译通过
2. 执行 `go vet ./...` 确保无静态错误
3. 准备内存存储容器，创建一个升级任务（状态为 running，超时设为 1 秒）
4. 为目标设备创建一条执行记录（TaskDeviceExecution），状态设为 downloading，分配时间设为 2 秒前（确保已超时）
5. **关键点：不创建任何升级历史记录（UpgradeHistory）**
6. 创建 ProgressService 实例
7. 调用 `ScanTimeout(ctx)` 方法

## 4. 实际结果（Actual Behavior / Observed Output）
- 触发 panic：`runtime error: invalid memory address or nil pointer dereference`
- panic 堆栈指向超时扫描处理逻辑中的历史记录更新部分
- RED/GREEN 判定结果：RED（红灯，缺陷未修复）
- 服务崩溃，所有后续请求无法处理
- 测试用例输出：

```
=== RUN   TestRedGreen
    red_green_test.go:57: RED（红灯，缺陷未修复）
    red_green_test.go:58: FAIL: panic detected - nil pointer dereference when FindLatestByDevice returns nil history without error: runtime error: invalid memory address or nil pointer dereference
--- FAIL: TestRedGreen (0.00s)
FAIL
```

## 5. 期望结果（Expected Behavior）
- 无 panic、无程序崩溃
- 对无历史记录的设备安全跳过或创建新记录，不触发空指针解引用
- RED/GREEN 判定结果应为 GREEN
- 超时扫描正常完成，返回正确的处理数量
- go build 与 go vet 全部通过
- 测试用例输出：

```
=== RUN   TestRedGreen
    red_green_test.go:64: GREEN（绿灯，缺陷已修复）
    PASS: ScanTimeout completed successfully without panic, handled 1 timeout records
--- PASS: TestRedGreen (0.00s)
PASS
ok      firmware-upgrade        0.008s
```

## 6. 触发频率（Frequency）
必现（100%）：只要满足"有执行记录但无历史记录"的条件，每次调用超时扫描都会稳定触发 panic。

## 7. 影响范围（Impact / Scope）
- 服务 panic 崩溃：整个固件升级服务进程退出
- 所有正在进行的升级任务中断
- 设备无法继续上报进度
- 新的升级任务无法创建
- 管理后台所有接口不可用
- 需要人工重启服务恢复

## 8. 附加说明（Additional Notes / Workaround）
临时规避方案：
1. 在调用超时扫描前，确保所有执行记录对应的设备都至少有一条历史记录
2. 或在业务逻辑层捕获 panic 并优雅降级，但会导致超时设备无法被正确处理
建议尽快修复根本原因，长期规避方案不可取。
