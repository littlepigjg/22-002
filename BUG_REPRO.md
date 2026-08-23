# 缺陷复现报告（Bug Reproduction Report）

## 1. 问题概述（Summary）
固件升级管理平台在写 HTTP 错误响应的路径上出现两类异常：一是部分 500 响应的 message 字段完全为空，或仅显示通用 "internal server error"，底层 cause 的关键错误文本（磁盘扇区校验、文件分配、设备校验失败、内部具体错误码等）被整条吞掉，导致线上无法定位根因；二是在某些错误经过业务错误包装、或携带空文本的 Internal 错误进入写响应流程时，进程出现 nil pointer deref panic，即便恢复中间件救回进程，错误上下文也已丢失，日志里只能看到 "internal panic" 的残余字样。该问题在 model 内置 sentinel 错误（NotFound、Conflict、UploadTooLarge 等）的普通路径下不会触发，但一遇到未注册的裸错误、空消息 Internal 错误、以及经过多层错误包装后的未知错误就会稳定命中。

## 2. 环境信息（Environment）
- 操作系统：Linux（内核 5.x 及以上，任意主流发行版均可复现；在 Alpine / Ubuntu 22.04 上均验证）
- Go 版本：go version go1.22.X linux/amd64（go1.22+ 均可，1.22 已必现）
- 项目模块/依赖：module firmware-upgrade，go.mod 中零第三方依赖，纯标准库 + 内部子包（pkg/response、pkg/logger、internal/handler、internal/service 等）
- 运行参数：go test 普通单次执行即可复现，不需要 -race，不需要特殊并发；测试命令耗时约 0.01s
- 硬件信息（与并发/性能相关可补充）：与 CPU 核数、内存大小均无关，单核 1C/1G 环境同样必现

## 3. 复现步骤（Steps to Reproduce）
1. 进入项目根目录，执行 `go build ./...` 确保项目编译全通过；再执行 `go vet ./...` 没有任何静态检查错误。
2. 执行 `go test -c -o /dev/null .` 确认 red_green_test.go 可与被测代码无缝编译通过（如果此步失败，说明被测侧签名被破坏，先回到代码调整签名一致性，不要进入复现步骤）。
3. 在项目根目录执行 `go test . -count=1 -run '^TestRedGreen$' -v` 启动验收用例（共 7 组：case1 空消息 Internal 业务错误、case2 空消息包装普通 cause、case3 未匹配前缀的未知裸错误、case4 response.Error 直接调用 typed-nil 包装结果、case5 typed-nil 接口赋值为 Coder 后写响应、case6 FileOp 边界错误经服务层包装链、case7 全量 model sentinel 回归）。
4. 观察控制台末尾 RED/GREEN 判定段落，以及每个 case 的 t.Log 输出。
5. 如需最小手工复现，还可直接构造一个携带空消息的 Internal 业务错误（例如 NewBizError(500, 50000, "")），将其作为 error 接口传入 WriteError，用 httptest.ResponseRecorder 捕获 HTTP 响应体后解析 JSON 的 message 字段，即可观察到 panic 或空 message。

## 4. 实际结果（Actual Behavior / Observed Output）
- 具体 panic 堆栈片段（节选）：
  ```
  panic: runtime error: invalid memory address or nil pointer dereference
  [signal SIGSEGV: segmentation violation code=0x1 addr=0x0 pc=0x...]
  goroutine running:
    firmware-upgrade/pkg/response.(*bizErr).Error(0x?)
        pkg/response/response.go: ...
    firmware-upgrade/pkg/response.(*bizErr).Unwrap(0x?)
        pkg/response/response.go: ...
    firmware-upgrade/internal/handler.normalizeForResponse(...)
        internal/handler/util.go: ...
    firmware-upgrade/internal/handler.WriteError(...)
        internal/handler/util.go: ...
  ```
- RED/GREEN 判定结果：RED（红灯，缺陷未修复）
- 其他异常现象：
  - case1~case5 共 5 个 case 或走 panic 分支，或最终响应 JSON 中 message 字段为空串 / "internal server error"（cause 原文丢失）；
  - case4 cause.Error() 的原始文本 "corrupt allocation table" / "sector checksum mismatch on device block 0x3F11" 等细节在响应中全部消失；
  - case5 在 typed-nil Coder 写响应途中产生 nil pointer deref。
- go test -race 报告：本缺陷为纯 nil 接口/错误传播问题，无共享内存并发写入，-race 无 DATA RACE 告警。
- 测试最终退出码：1。

## 5. 期望结果（Expected Behavior）
- 无 panic：整个写错误响应流程对任意 error 入参都不触发 runtime panic（包括 typed-nil 的 bizErr 接口、携带空 cause 的包装错误、空消息 Internal 错误、未匹配前缀的未知裸错误）。
- RED/GREEN 判定结果应为 GREEN（绿灯，缺陷已修复），7/7 case 全通过，退出码 0。
- 正确业务行为：
  - 错误响应 JSON 中的 message 字段永远非空、永远是有效可读文本；
  - 当 cause 携带具体错误信息时（如 "sector checksum mismatch on device block 0x3F11"、"corrupt allocation table" 等），message 字段必须完整保留并体现在字符串中；
  - model 中全部内置 sentinel（ErrNotFound、ErrConflict、ErrDeviceNotFound、ErrUploadTooLarge、ErrFirmwareNotPublished 等）在修复前后的 HTTP 状态码、业务 Code、message 原文完全一致，不退化、不替换成通用 internal 文案。
- go build ./... 与 go vet ./... 全部通过，无编译错误无静态告警；go test -c 编译通过。

## 6. 触发频率（Frequency）
必现（100%）：只要构造空消息 Internal 错误或 cause 未匹配前缀的未知裸错误，每次运行都会稳定地打出 nil pointer deref 或空 message。不需要 -race、不需要 count=20 重复、不需要并发压力即可稳定复现。

## 7. 影响范围（Impact / Scope）
- 错误信息全部丢失或退化：用户/运维侧 500 页面看不到根因细节，无法区分是磁盘错误、固件文件损坏、设备端非法上报、还是纯内部逻辑错误。
- 接口偶发 500 看起来像"空错误"：监控系统难以对具体错误类型聚类聚合，SLO 告警无法精准定位。
- 进程 panic 增加重启抖动：若某些调用链未接入 RecoveryMiddleware 或在中间件之外直接写响应，会直接导致单进程/容器 crash，k8s pod 反复重启，升级任务中断、设备轮询失败。
- 调用链上错误包装越多层，问题越严重：FileOp → handler → response 的三段链路共同放大了同一问题，修复不彻底会在任一层复发。

## 8. 附加说明（Additional Notes / Workaround）
临时 workaround（不建议长期使用）：在所有写响应入口前手动判断 `if err.Error() == ""` 并替换成自定义 sentinel 错误（如 `model.ErrInternal`），可短期避免空 message 与 panic；但该方案绕过正常错误包装链路，无法透传 cause 细节，同时会漏掉接口层产生 typed-nil 的更深层路径，只能做 hotfix 顶一下。建议尽快定位到根因并做完整修复。
