# 缺陷复现报告（Bug Reproduction Report）

## 1. 问题概述（Summary）
固件上传服务在文件保存过程中发生错误时，会产生文件句柄（file descriptor）泄漏。每次触发保存失败的路径，进程打开的文件句柄数量就会增加1个，多次失败后句柄数持续累积，最终可能导致 "too many open files" 错误，影响服务正常运行。

## 2. 环境信息（Environment）
- 操作系统：Linux（x86_64）
- Go 版本：go 1.22+
- 项目模块：firmware-upgrade
- 运行参数：无需特殊运行参数
- 硬件信息：CPU 核数无特殊要求

## 3. 复现步骤（Steps to Reproduce）
1. 进入项目根目录，执行 `go build ./...` 确保编译通过
2. 执行 `go test . -count=1 -run '^TestRedGreen$' -v` 运行验证测试
3. 观察测试输出中的 "Initial open file count" 和 "Final open file count"
4. 若 Final count > Initial count，说明句柄泄漏已发生

## 4. 实际结果（Actual Behavior / Observed Output）
- 测试输出显示：Initial open file count: 0, Final open file count: 5
- RED（红灯，缺陷未修复）：RESULT: RED
- 每次触发保存失败路径后，文件句柄数增加1个
- 退出码：1（测试失败）

## 5. 期望结果（Expected Behavior）
- 所有文件句柄在使用完毕后都应被正确关闭
- Final open file count 应等于 Initial open file count（均为0）
- GREEN（绿灯，缺陷已修复）：RESULT: GREEN
- go build ./... 编译通过
- go vet ./... 无警告

## 6. 触发频率（Frequency）
必现（100%）：每次触发保存失败的路径都会泄漏一个文件句柄。只要文件打开后、保存过程中发生任何错误导致提前返回，句柄就不会被关闭。

## 7. 影响范围（Impact / Scope）
- 服务长期运行后句柄数持续增长，最终达到系统限制
- 句柄耗尽后所有文件操作（包括正常上传、下载、日志写入）都会失败
- 严重时服务无法处理任何请求，影响固件上传、下载、设备升级等所有核心功能
- 可能导致进程被操作系统 kill

## 8. 附加说明（Additional Notes / Workaround）
- 临时规避：定期重启服务以释放累积的文件句柄
- 根本解决：需要修复保存过程中的句柄泄漏问题
- 测试文件 red_green_test.go 会自动检测此问题并输出 RED/GREEN 判定
