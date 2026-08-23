# 缺陷复现报告（Bug Reproduction Report）

## 1. 问题概述（Summary）
固件升级管理平台在进行错误归一化改造后，特定的错误归一化路径会导致 HTTP 500 内部错误响应的 message 字段丢失（返回空字符串），而 HTTP 状态码和业务 code 值看起来是正常的（500 / 50000）。用户端界面显示为空提示，无法知道错误原因。最稳定的触发方式是内部兜底函数 RequireNotNil(nil) 产生的错误：在归一化 → 包装 → 提取 message → 输出响应的完整链路上，message 被一层层清空，最终输出空白。常规的业务错误（404 not found / 400 invalid param 等）与常规的原生 errors.New 错误有时正常、有时也会在特定包装条件下表现为空白，但 RequireNotNil(nil) 路径是 100% 必现的。

## 2. 环境信息（Environment）
- 操作系统：Linux（内核版本 5.x+）
- Go 版本：go1.22+（module firmware-upgrade，go 1.22，`go version` 输出验证）
- 项目模块：firmware-upgrade（无第三方运行时依赖，纯标准库 + 内部包 internal/config、internal/dto、internal/handler、internal/model、internal/service、internal/store 以及 pkg/*）
- 运行参数：`go test . -count=1 -run '^TestRedGreen$'`，不带 -race（本缺陷为 nil interface 陷阱，与并发无关）
- 硬件信息：普通 x86_64 / arm64 服务器均可，单核也能稳定复现

## 3. 复现步骤（Steps to Reproduce）
按顺序执行以下步骤可稳定复现：

1. 进入项目根目录：`cd firmware-upgrade`（即本 README 同级目录）。
2. 确保依赖和环境 OK：`go build ./...` 应全部通过、`go vet ./...` 无警告、`go test -c -o /dev/null .` 编译测试包成功。
3. 构造内部兜底错误并走归一化输出链路：执行 `go test . -count=1 -run '^TestRedGreen$' -v`，观察 Scenario1（RequireNotNil(nil) → WriteError）场景的测试结果。
4. 也可以手动复现：编写 Go 代码，调用 `dto.RequireNotNil(nil)` 得到 error 对象，使用 `httptest.NewRecorder()` 作为 ResponseWriter，调用 `handler.WriteError(w, err)`，然后对 `w.Result().Body` 做 JSON 解码查看 body.Message。
5. 检查响应体的 message 字段值（空还是非空）。

## 4. 实际结果（Actual Behavior / Observed Output）
按照复现步骤执行后，实际观察到的结果：

- 场景1（RequireNotNil(nil) 输出）：HTTP 500、code=50000，但响应 JSON 的 `message` 字段为 `""`（空字符串）。关键片段：`{success:false, code:50000, message:"", timestamp:...}`。
- RED/GREEN 判定结果：RED（红灯，缺陷未修复）。
- 其他异常现象：`(*bizErr).Error()` 对该错误返回空串；`response.ExtractMessage` 也返回空串；但实际上在错误构造时确实传入了非空的 fallbackMsg（如 "unexpected nil error"），而该消息文本在链路中被丢弃。
- 测试用例 `Scenario1_RequireNotNil_Nil_MessageNotEmpty` FAIL，退出码为 1；其他普通业务错误子场景 PASS，但只要走到 RequireNotNil(nil) 路径就必现空 message。
- 本缺陷不是并发问题，`go test -race` 不会报告 DATA RACE。

## 5. 期望结果（Expected Behavior）
修复后按相同步骤运行，应出现如下正确行为：

- 无 panic、无数据竞争（go test -race 无警告）。
- RED/GREEN 判定结果应为 GREEN（绿灯，缺陷已修复）。
- 对 dto.RequireNotNil(nil) 产生的错误：WriteError 输出的 HTTP 500 响应体中 message 字段为非空的人类可读文本（应包含"unexpected nil error"或同义原因描述），决不再是 `""`。
- 对所有 model.Er 系列业务错误（ErrNotFound / ErrConflict / ErrInvalidParam / ErrForbidden / ErrFirmwareNotPublished 等）以及 errors.New 产生的任意原生错误：经 NormalizeBizError → WriteError 链路输出后，响应体 message 字段均为非空且语义正确的可读文本，不出现空串。
- 所有 HTTP 状态码映射保持不变（404 / 400 / 409 / 500 / 等），业务 code 映射保持不变。
- go build ./... 与 go vet ./... 全部通过；go test -c -o /dev/null . 编译成功。
- 公开的错误归一化/提取包装 API（SafeWrap、ExtractCode、ExtractMessage、NormalizeBizError、NormalizeMessage、RequireNotNil 等）签名和条目数保持不变（未被删除 / 重命名 / 改签名）。

## 6. 触发频率（Frequency）
必现（100%）。只要调用 dto.RequireNotNil(nil) 返回的错误经 handler.WriteError 输出响应，message 字段一定为空字符串；service 层部分位置 err == nil 的兜底返回同样稳定触发。对于普通的 model.Er 业务错误 / errors.New 原生错误：在默认包装路径当前实现下输出正常，但如果经过二次归一化（再次 NormalizeBizError）或代码后续在更多地方调用 RequireNotNil(nil) 作为兜底时，message 也会稳定为空。

## 7. 影响范围（Impact / Scope）
- 终端用户体验：HTTP 500 类错误提示完全空白，用户无法判断操作失败原因，造成困惑与投诉。
- 可观测性与排障：前端 / 网关记录的响应日志中 message 为空，给问题定位和根因排查带来很大障碍，严重影响运维与支持效率。
- 接口契约一致性：对外返回的 JSON 协议中 message 本应为必填可读字段，空 message 破坏了响应协议的完整性，下游依赖 message 做判断的调用方（管理后台、告警系统、前端展示层）可能出现分支判断失效或显示异常。
- 稳定性与风险：虽然缺陷本身不造成 panic，但内部错误路径 message 丢失可能掩盖更严重的底层异常，导致二次问题无法及时发现；大面积调用 RequireNotNil(nil) 兜底的位置会普遍表现为"500 + 空消息"，严重影响线上可用性体验。

## 8. 附加说明（Additional Notes / Workaround）
临时规避方法（短期）：在前端或网关层对响应体 `message == "" && code >= 50000` 的情形做兜底显示 "internal server error, please retry later" 之类通用文本，给用户一个可读提示，但这只能掩盖症状，无法修复错误包装链路上真正的 message 丢失问题。建议在归一化包装、nil interface 判定、错误消息提取的函数内部实现上做根本修复。
