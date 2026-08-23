# 缺陷复现报告（Bug Reproduction Report）

## 1. 问题概述（Summary）
固件升级服务在处理 HTTP 请求创建升级任务时，存在链路追踪断裂的问题：access log 中记录的 trace_id 无法透传至服务层业务日志，导致无法将一次请求的全链路日志关联起来。同时，当客户端请求超时或主动取消连接时，服务端任务创建操作不会被中断，继续执行至完成，造成不必要的资源消耗和潜在的数据不一致。

## 2. 环境信息（Environment）
- 操作系统：Linux (x86_64)
- Go 版本：go 1.22
- 项目模块：firmware-upgrade
- 关键依赖：仅使用 Go 标准库（log/slog, context, sync 等）
- 运行参数：go test . -count=1 -run '^TestRedGreen$'
- 硬件信息：CPU 4 核（与并发无关，本缺陷不涉及并发）

## 3. 复现步骤（Steps to Reproduce）
1. 进入项目根目录，执行 go build ./... 确保编译通过
2. 执行 go test . -count=1 -run '^TestRedGreen$' 运行验证测试
3. 观察测试输出

也可通过 HTTP 接口手动复现：
1. 启动服务：go run cmd/server/main.go
2. 发送创建任务请求，携带 X-Request-Id 头：
   ```
   POST /api/v1/tasks HTTP/1.1
   X-Request-Id: trace-demo-001
   Content-Type: application/json
   
   {
     "name": "demo-task",
     "model_id": "MODEL-001",
     "target_version": "v1.0.0",
     "strategy": "full"
   }
   ```
3. 观察日志输出，access log 中有 trace_id，但 service 层日志的 trace_id 为空

## 4. 实际结果（Actual Behavior / Observed Output）
测试运行结果（缺陷未修复时）：
- RED（红灯，缺陷未修复）
- trace_id 'trace-test-20250101-0001' 未能透传至服务层日志，trace 记录中未找到匹配的 trace_id
- 实际 trace 记录数: 4，其中非空 trace_id 数: 0
- RED（红灯，上下文取消未能传播）
- 预期返回 context 取消错误，实际返回 nil（操作未被取消）

日志样例（注意 trace_id 均为空）：
```
{"level":"INFO","msg":"Create task called","trace_id":""}
{"level":"INFO","msg":"assignInitialExecutions called","trace_id":""}
{"level":"INFO","msg":"SelectDevices called","trace_id":""}
```

access log 样例（trace_id 正常）：
```
{"level":"INFO","msg":"access ok","trace_id":"trace-demo-001"}
```

## 5. 期望结果（Expected Behavior）
修复后按相同步骤运行应该出现：
- go test . -count=1 -run '^TestRedGreen$' 判定为 GREEN（绿灯，缺陷已修复）
- service 层日志的 trace_id 与 access log 一致：
  ```
  {"level":"INFO","msg":"Create task called","trace_id":"trace-demo-001"}
  {"level":"INFO","msg":"SelectDevices called","trace_id":"trace-demo-001"}
  ```
- 传入已取消 context 时，TaskService.Create 立即返回 context 取消错误
- go build ./... 编译通过
- go vet ./... 无警告

## 6. 触发频率（Frequency）
必现（100%）。任何通过 HTTP 接口创建的任务，其 service 层日志的 trace_id 均为空；任何已取消 context 的请求均不会被中断。

## 7. 影响范围（Impact / Scope）
- 所有需要链路追踪的操作（任务创建、灰度判定、设备轮询等）均无法关联 access log 与 service 日志，线上问题排查困难
- 客户端取消请求后，服务端继续执行创建任务、分配设备执行记录、写入升级历史等操作，造成数据脏写和资源浪费
- 在高并发或批量操作场景下，大量被取消的请求继续执行，可能导致存储层压力增大

## 8. 附加说明（Additional Notes / Workaround）
目前无临时规避方案。如果需要紧急处理 trace_id 问题，可在日志分析层面手动关联 request_id 与 trace_id，但治标不治本。context 取消问题暂无 workaround。
