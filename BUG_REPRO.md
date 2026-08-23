# 缺陷复现报告（Bug Reproduction Report）

## 1. 问题概述（Summary）
固件升级服务在执行灰度发布任务时，设备分组筛选逻辑出现数据异常：创建指定分组（如 alpha）的灰度任务后，命中设备（hit）和未命中设备（miss）的返回结果不稳定，表现为 beta 设备丢失、alpha 设备同时出现在 hit 和 miss 中、或 hit 为空等现象，导致升级执行记录创建错误或完全缺失。

## 2. 环境信息（Environment）
- 操作系统：Linux
- Go 版本：go version go1.25.1 linux/amd64
- 项目模块：firmware-upgrade
- 运行参数：go test -count=1 -run '^TestRedGreen$'
- 硬件信息：CPU 核数与问题无关

## 3. 复现步骤（Steps to Reproduce）
1. 进入项目根目录，执行 `go build ./...` 确保编译通过
2. 执行 `go test -c -o /dev/null .` 确保测试包编译通过
3. 执行 `go clean -testcache && go test -v -run TestRedGreen .` 运行测试
4. 观察测试输出中的 RED/GREEN 判定结果和具体错误信息

## 4. 实际结果（Actual Behavior / Observed Output）
测试输出判定为 RED（红灯，缺陷未修复），典型错误信息包括：
- miss device D-A-1 has group "alpha", expected "beta" — slice backing array corruption detected!
- beta device D-B-1 missing from miss (lost due to slice corruption)
- device D-A-1 appears in both hit and miss (slice overlap corruption)
- execution count mismatch: got 0, expected 4 (alpha devices)
- alpha device D-A-1 missing from executions
- 退出码为 1（FAIL）

## 5. 期望结果（Expected Behavior）
修复后按相同步骤运行应出现：
- 测试输出判定为 GREEN（绿灯，缺陷已修复）
- hit 列表正确包含 4 个 alpha 分组设备
- miss 列表正确包含 4 个 beta 分组设备
- 没有设备同时出现在 hit 和 miss 中
- 创建任务后正确生成 4 条升级执行记录
- go build ./... 通过
- go vet ./... 无报错

## 6. 触发频率（Frequency）
必现（100%），每次运行测试均可稳定复现 RED 结果。具体的错误模式取决于 Go map 迭代顺序（两种模式交替出现）：
- 模式 A：hit=4(alpha), miss=4(alpha)，beta 设备丢失，设备同时出现在 hit 和 miss
- 模式 B：hit=0, miss=8(beta)，alpha 设备丢失，miss 中出现重复的 beta 设备

## 7. 影响范围（Impact / Scope）
- 灰度升级任务无法正确筛选目标设备，导致升级执行记录创建错误
- beta 设备完全丢失，无法被正确排除或进入后续处理流程
- 升级执行记录的版本字段（FromVersion）可能取自错误设备指针
- 同一任务多次查询 SelectDevices 结果不一致，增加排查难度
- 灰度发布功能整体不可用

## 8. 附加说明（Additional Notes / Workaround）
无临时规避方法。问题涉及切片底层数组共享导致的数据竞争式覆盖，仅能通过修正切片使用方式（为每个逻辑切片分配独立底层数组）来彻底解决。
