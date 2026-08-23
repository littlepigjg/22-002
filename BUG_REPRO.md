# 缺陷复现报告（Bug Reproduction Report）

## 1. 问题概述（Summary）
设备心跳接口在处理未注册设备的请求时，错误类型传递出现异常。当向系统发送一个不存在的设备 ID 的心跳请求时，返回的错误无法通过标准的 errors.Is(err, model.ErrDeviceNotFound) 方式识别为"设备不存在"类型，导致下游业务逻辑无法正确区分"设备不存在"与其他类型的错误。已注册设备的心跳请求不受影响。

## 2. 环境信息（Environment）
- 操作系统：Linux
- Go 版本：go1.22.0 或以上
- 项目模块/依赖：module firmware-upgrade
- 运行参数：go test（无特殊参数要求）
- 硬件信息：CPU 核数无特殊要求

## 3. 复现步骤（Steps to Reproduce）
1. 进入项目根目录，执行 `go build ./...` 确保编译通过
2. 执行测试命令 `go test . -count=1 -run '^TestRedGreen$'`
3. 观察测试输出，查看 RED/GREEN 判定结果
4. 或执行 `go test . -count=1 -run '^TestHeartbeatErrorPropagation$'` 查看错误传播测试结果

## 4. 实际结果（Actual Behavior / Observed Output）
- 具体的错误信息：测试输出显示 "错误类型不正确" 或 "错误无法通过 errors.Is 识别"
- RED/GREEN 判定结果：RED（红灯，缺陷未修复）
- 其他异常现象：返回的错误消息为 "heartbeat failed: device {id} not found"，而不是原始的 "device not found"
- go test -race：无数据竞争（非并发缺陷）

## 5. 期望结果（Expected Behavior）
- 无 panic、无数据竞争（go test -race 无警告）
- RED/GREEN 判定结果应为 GREEN
- 正确的业务行为：对未注册设备的心跳请求，返回的错误应可通过 errors.Is(err, model.ErrDeviceNotFound) 正确识别
- go build 与 go vet 全部通过

## 6. 触发频率（Frequency）
必现（100%）。只要对未注册设备发送心跳请求，必然触发此缺陷。

## 7. 影响范围（Impact / Scope）
- 设备心跳接口对未注册设备的错误处理失效
- 下游业务模块无法正确识别"设备不存在"错误类型
- 可能导致上游应用对设备离线/未注册状态的处理逻辑异常
- 已注册设备的心跳功能不受影响

## 8. 附加说明（Additional Notes / Workaround）
无
