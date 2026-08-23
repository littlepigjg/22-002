# 缺陷复现报告（Bug Reproduction Report）

## 1. 问题概述（Summary）
固件管理服务在处理"元数据里 FilePath 未正确填写 / 文件被误删但记录未同步"的异常场景时，下载接口没有返回预期的 404 "firmware file not found"，而是返回 HTTP 500；更严重的是，响应体中的 `message` 字段是空字符串，服务端错误日志也只打印 `internal error message=`（message 为空），运维和调用方都无法从日志/响应中看出问题根因。与此同时，文件工具 `Size()` 对空路径、不存在文件、目录三种异常输入的错误返回值为 nil，导致上游无法以常规的 `if err != nil` 分支做防御性处理；响应工具 `Internal(err)` / `Error(err)` 在传入 nil 时同样输出空 message 的 500，与常识的"系统内部错误"默认文案不一致。

## 2. 环境信息（Environment）
- 操作系统：Linux（Ubuntu 20.04/22.04 或同代发行版）
- Go 版本：Go 1.22+（`go version` 输出 `go1.22.x linux/amd64` 或更高）
- 项目模块/依赖：`module firmware-upgrade`，无第三方依赖，纯标准库 + 内部包（internal/*、pkg/*）
- 运行参数：普通 `go test`，不涉及 `-race`（非并发缺陷）；单次运行 < 1s
- 硬件信息（如与并发/性能相关可补充）：与 CPU/内存无直接关系

## 3. 复现步骤（Steps to Reproduce）
1. 进入项目根目录，执行 `go build ./...` 与 `go vet ./...` 确保编译与静态检查通过。
2. 在工程根目录执行 `go test -c -o /dev/null .` 确保验收测试文件能与被测代码一起无错编译。
3. 构造一条 FilePath 为空的固件元数据记录（可使用存储层的守卫型保存接口直接写入，或先建 DeviceModel 后通过带校验的 Save 接口写入，ID 自定例如 FW-NOPATH、ModelID=M-1、Version=v1.0.0、FileName=fw.bin、Size=1024、MD5 为合法 32 字母十六进制字符串，FilePath = ""）。
4. 针对该 ID 调用下载 handler（GET /api/v1/firmwares/{id}/download，HTTP Context 中注入正确 PathID）。
5. 单独执行单元级调用：
   - `fileutil.Size("")`；
   - `fileutil.Size(<某个确定不存在的绝对路径>)`；
   - `fileutil.Size(<某个已知目录>)`；
   - `response.Internal(w, nil)` / `response.Error(w, nil)`。
6. 或直接运行项目根目录提供的验收脚本：`go test . -count=1 -run '^TestRedGreen$'`。

## 4. 实际结果（Actual Behavior / Observed Output）
- 构造 FilePath=="" 固件后调用下载接口：
  - HTTP 状态码：500 Internal Server Error
  - 响应 JSON：`{"success":false,"code":50000,"message":"","timestamp":...}`（message 为空）
  - 服务端日志：`"msg":"internal error","message":""`（错误信息字段为空，无法定位）
- fileutil.Size 边界返回错误全部被吞：
  - `Size("")` → `(0, nil)`（应返回 error 非 nil）
  - `Size(<不存在文件>)` → `(0, nil)`（应返回 error 非 nil）
  - `Size(<目录>)` → `(0, nil)`（应返回 error 非 nil）
- response.Internal/Error 传入 nil：
  - HTTP 500，但 message == ""（应为非空默认文案如 "internal server error"）
- RED/GREEN 判定：RED（红灯，缺陷未修复）
- go test 运行结果：FAIL，exit code 1

## 5. 期望结果（Expected Behavior）
- 下载接口针对 FilePath=="" 或文件不存在的固件：
  - 返回 HTTP 404，message 明确表达 "firmware file not found" 或等价中文含义（message 非空）；
  - 若因其它内部条件走到 5xx，message 必须是一句可读的默认/原始错误文案，绝对不允许空字符串。
- fileutil.Size 错误契约回归常识：
  - `Size("")` → error != nil，文案提示路径为空；
  - `Size(<不存在文件>)` → error != nil，文案提示文件不存在或原样返回 `os.ErrNotExist` 对应的 os.PathError；
  - `Size(<目录>)` → error != nil，文案提示路径是目录；
  - 正常文件 size 读写保持正确，不影响上传/大小校验的既有行为。
- response.Internal/Error：
  - 传 nil error 时仍输出 "internal server error" 之类非空默认 message；
  - 传非 nil error 时优先显示 error.Error()，不被默认值吞没。
- 服务端日志在 5xx 分支：必须记录实际错误对象或默认文案，message 字段不能为空。
- RED/GREEN 判定：GREEN（绿灯，缺陷已修复）
- go build ./... 与 go vet ./... 全部通过，无任何告警。

## 6. 触发频率（Frequency）
必现（100%）：本缺陷为纯逻辑分支缺陷，不受并发/时序/系统负载影响，只要满足"FilePath 为空"或"Size/Internal 边界输入为 nil / 非法路径"的前置条件，每次都能稳定触发。无需反复执行或加 -race。

## 7. 影响范围（Impact / Scope）
- 运维/调用方不可诊断：500 message 为空导致告警聚合、日志检索无法命中关键词，排障成本急剧上升；
- 元数据异常场景行为不正确："文件未上传/误删未同步"本该是 404 的可恢复业务错误，被错误升级为 500，导致调用端重试策略被触发（空消息 + 500 一般会被重试），但重试结果仍然失败，空耗资源；
- fileutil.Size 契约破坏：所有上层基于 `Size() err==nil` 判断"文件可读且大小有效"的位置都被污染，可能导致后续文件写入/鉴权/Content-Length 计算产生"大小为 0"的错误推断；
- 系统状态污染：response.Internal/Error 的 nil 兜底逻辑会将来自任何包的一次 err 漏传都放大为"空文案 500"，让真正的错误无法浮出水面，属于全局性错误通路风险。

## 8. 附加说明（Additional Notes / Workaround）
- 临时规避：数据库/存储侧将 FilePath 为空的固件元数据记录先补齐或下架，避免线上命中 FilePath=="" 的下载分支；
- 调用端临时规避：在调用方侧对 500 + message=="" 的响应按"文件不存在"重试一次（直接 404 处理）；
- 相关日志样例（缺陷出现时截取）：
  ```
  {"time":"...","level":"ERROR","src":".../pkg/logger/logger.go:117","msg":"internal error","message":""}
  ```
- 注意：以上规避方案仅是临时手段，不能替代修复；真正修复需从错误传播链上补齐 fileutil.Size 边界返回、response.Internal/Error 的 nil 兜底、Download 的 FilePath 前置判断三部分，缺一不可。
