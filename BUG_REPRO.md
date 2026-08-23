# 缺陷复现报告（Bug Reproduction Report）

## 1. 问题概述（Summary）
固件上传服务在连续上传多个文件时，从第二个文件开始，服务端记录的 MD5 校验值与客户端独立计算的 MD5 值不一致。第一个文件的 MD5 计算结果正确，但后续文件的 MD5 均计算错误，且错误值与前一个文件的内容及 MD5 值存在关联。

## 2. 环境信息（Environment）
- 操作系统：Linux
- Go 版本：go 1.22
- 项目模块：firmware-upgrade
- 运行参数：go test . -count=1 -run '^TestRedGreen$'
- 硬件信息：与硬件无关

## 3. 复现步骤（Steps to Reproduce）
1. 进入项目根目录，执行 go build ./... 确保编译通过
2. 创建一个临时目录作为测试数据目录
3. 初始化 FileOpService 实例（指定临时目录作为 DataDir 和 FirmwareDir）
4. 准备两个不同内容的字节切片作为两个文件的数据
5. 将第一个字节切片包装为 io.Reader，调用 FileOpService.SaveReader() 保存，记录返回的 MD5
6. 将第二个字节切片包装为 io.Reader，调用 FileOpService.SaveReader() 保存，记录返回的 MD5
7. 使用独立的 MD5 计算工具（如 md5util.SumBytes）分别计算两个文件内容的正确 MD5
8. 比较第 6 步返回的 MD5 与第 7 步独立计算的第二个文件的 MD5

## 4. 实际结果（Actual Behavior / Observed Output）
- 第一个文件返回的 MD5 与独立计算值一致
- 第二个文件返回的 MD5 与独立计算值不一致
- 测试输出 "RED（红灯，缺陷未修复）"
- 测试退出码为 1
- 错误示例：期望 MD5: c8c755a82e976ef016b9146b4e0bb1c3，实际 MD5: 5a992b71ccea3a7da53af28475e10a75
- 第三个及后续文件的 MD5 同样计算错误

## 5. 期望结果（Expected Behavior）
- 每个文件返回的 MD5 均与独立计算值完全一致
- go test . -count=1 -run '^TestRedGreen$' 退出码为 0
- 测试输出 "GREEN（绿灯，缺陷已修复）"
- go build ./... 编译通过
- go vet ./... 无静态检查错误

## 6. 触发频率（Frequency）
- 必现（100%）：只要连续上传两个及以上文件，从第二个文件开始 MD5 必然错误
- 第一个文件的 MD5 始终正确

## 7. 影响范围（Impact / Scope）
- 所有涉及多文件连续上传的场景均受影响
- 服务端记录的 MD5 不可用于固件完整性校验
- 固件去重逻辑可能失效（错误的 MD5 导致无法正确识别重复固件）
- 下载校验环节可能因 MD5 不匹配导致固件被误判为损坏
- 批量上传场景下所有文件的 MD5 均不可信

## 8. 附加说明（Additional Notes / Workaround）
- 临时规避方法：每个文件使用独立的 MD5 计算器实例，或在每个文件处理完成后手动重置计算状态
- 独立 MD5 计算工具（如 md5util.SumBytes）不受此缺陷影响，可用于校验服务端返回值