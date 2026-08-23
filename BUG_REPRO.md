# 缺陷复现报告（Bug Reproduction Report）

## 1. 问题概述（Summary）
升级任务状态管理接口在非法状态转换场景下返回错误的 HTTP 状态码。具体表现为：对已取消的任务再次执行取消操作（或对已完成的任务执行暂停/恢复等非法状态转换）时，接口返回 500 Internal Server Error 而不是预期的 400 BadRequest。错误消息内容本身是正确的（"task state transition illegal"），但 HTTP 响应码不正确，导致前端和调用方无法正确区分业务错误和系统错误。

## 2. 环境信息（Environment）
- 操作系统：Linux
- Go 版本：go1.24.x（以 go.mod 指定版本为准）
- 项目模块/依赖：module firmware-upgrade
- 运行参数：无特殊要求
- 硬件信息：与并发/性能无关，无特殊要求

## 3. 复现步骤（Steps to Reproduce）
1. 进入项目根目录，执行 `go build ./...` 确保编译通过
2. 启动测试或直接运行 `go test . -count=1 -run '^TestRedGreen$' -v`
3. 观察测试输出中的 HTTP 状态码和 RED/GREEN 判定结果

或者通过 HTTP 接口手动复现：
1. 启动服务
2. 创建一个升级任务（状态为 pending 或 running）：POST /api/v1/tasks
3. 第一次取消任务：POST /api/v1/tasks/{task_id}/action，body: {"action":"cancel"}
4. 第二次再次取消同一任务：POST /api/v1/tasks/{task_id}/action，body: {"action":"cancel"}
5. 观察第二次取消的 HTTP 响应状态码

## 4. 实际结果（Actual Behavior / Observed Output）
- 测试输出：
  - `errors.Is(err, model.ErrTaskState) = false`
  - `第三次取消 HTTP 状态码: 500`
  - `RED（红灯，缺陷未修复）`
- HTTP 响应示例：
  - 状态码：500 Internal Server Error
  - 响应体：`{"success":false,"code":50000,"message":"task state transition illegal","timestamp":...}`
- 其他异常现象：pause、resume、finish 等非法状态转换同样返回 500 而非 400
- go test -race：无数据竞争（此缺陷不涉及并发）

## 5. 期望结果（Expected Behavior）
- 重复取消已取消的任务时，HTTP 接口正确返回 400 BadRequest
- pause/resume/finish 等非法状态转换同样返回 400 BadRequest
- HTTP 响应示例：
  - 状态码：400 BadRequest
  - 响应体：`{"success":false,"code":40000,"message":"task state transition illegal","timestamp":...}`
- go build 与 go vet 全部通过
- 测试输出：`GREEN（绿灯，缺陷已修复）`

## 6. 触发频率（Frequency）
必现（100%）。任何对已处于终态（finished/canceled/failed）的任务执行非法状态转换操作都会触发此缺陷。

## 7. 影响范围（Impact / Scope）
- 所有任务状态管理接口（pause/resume/cancel/finish）的非法状态转换场景
- 前端或调用方无法正确处理业务错误：前端期望根据 400 状态码显示业务提示，但收到 500 后可能显示"系统错误"的通用提示
- 影响用户体验：用户重复操作时无法得到明确的"状态不允许"提示
- 可能影响监控告警：500 错误会触发告警，而实际上这是预期的业务场景

## 8. 附加说明（Additional Notes / Workaround）
临时规避方法：在前端或调用方判断任务状态，避免对已完成/已取消/已失败的任务执行状态转换操作。此为临时规避，根本问题需要在服务端修复。
