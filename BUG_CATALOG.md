# 设备固件升级管理服务 —— 缺陷候选清单 (BUG_CATALOG)

> 项目缩写：**fu** (firmware-upgrade)
> 缺陷数量：**严格 30 条**（编号 0–29，对应范围 0-30）
> 说明：本清单仅标注「可植入位置」，不实际植入任何缺陷。每个缺陷均为跨文件运行时缺陷，均能通过编译。

| # | bug_id | 分类 | 缺陷描述（5W） | 植入位置（跨文件） | 预期表现 | 触发方式 | 难度 |
|---|--------|------|----------------|--------------------|----------|----------|------|
| 0 | fu-concur-00 | concurrency | DeviceStore.byID 在 Create(注册) 与 Heartbeat(心跳) 并行写入时未加锁，并发读写产生 data race 与 map 冲突。 | internal/store/device_store.go:Create + internal/service/device_service.go:Register + internal/store/device_store.go:Heartbeat | `-race` 下 `data race` 或 "concurrent map read and map write"。 | 50 协程并发调用 POST /devices 和 POST /devices/heartbeat，`go test -race -count=5`。 | ★★☆☆☆ |
| 1 | fu-concur-01 | concurrency | UpgradeTaskStore.running/total 计数器使用普通 int，Create/Delete 并发导致计数漂移（负数或不一致）。 | internal/store/task_store.go:Create + internal/store/task_store.go:Delete + internal/handler/stats_handler.go:Overview | `/api/v1/stats/overview` 中 task_count 与实际 list 总数不一致。 | 20 协程交替 Create+Delete 任务 10 轮后统计。 | ★★★☆☆ |
| 2 | fu-concur-02 | concurrency | GrayService.SampleByRatio 中 candidates 切片与外层 list 共享底层数组，并发 append 导致越界。 | internal/service/gray_service.go:SampleByRatio + internal/service/task_service.go:assignInitialExecutions | 偶发 `index out of range` 或设备被重复分配。 | 并发创建 30 个灰度 50% 任务，`-race -count=10`。 | ★★★☆☆ |
| 3 | fu-concur-03 | concurrency | HistoryStore byDevice 索引写入与并发 FindLatestByDevice 遍历无锁竞争。 | internal/store/history_store.go:Create + internal/store/history_store.go:FindLatestByDevice + internal/store/history_store.go:CountDaily | `-race` data race；极端下 `concurrent map iteration and map write`。 | 40 协程：一半 ReportProgress、一半 GET /stats/daily。 | ★★☆☆☆ |
| 4 | fu-concur-04 | concurrency | ProgressService deviceLock 单设备 mutex 下两次 FindLatestByDevice（锁外读）+ 锁内写回，旧值覆盖新进度。 | internal/service/progress_service.go:Report + internal/store/history_store.go:FindLatestByDevice | 并发下进度从 100 回退到 0。 | 100 台设备交替 progress=100/progress=0 上报 20 轮。 | ★★★★☆ |
| 5 | fu-concur-05 | concurrency | cache.Cache 中 Purge 与 Get 的惰性删除存在锁外访问竞态，Set 后被误删。 | pkg/cache/cache.go:Purge + pkg/cache/cache.go:Get + pkg/cache/cache.go:SetTTL | `-race` data race；键值丢失。 | 单测对同一 key 并发 Set+Purge 10 万次，`Len()` 偶发失败。 | ★★★☆☆ |
| 6 | fu-concur-06 | concurrency | safemap.ForEach 在只读锁内调用外部 fn，若 fn 反向 Set 导致重入 panic。 | pkg/safemap/safemap.go:ForEach + internal/store/task_exec_store.go:FindAssignedRunning + internal/service/poll_service.go:Poll | 高并发下偶发 panic "concurrent map writes"。 | 前台服务 + worker 同时启动，1000 次设备 Poll。 | ★★★★☆ |
| 7 | fu-concur-07 | concurrency | RateLimiterMiddleware 中 ComputeIfAbsent 返回 bucket 引用后在 safemap 外读改，存在竞态。 | internal/middleware/middleware.go:RateLimiterMiddleware + pkg/safemap/safemap.go:ComputeIfAbsent | 实际放行请求超过 rpm 上限。 | 500 协程压测 GET /health，计数 2xx 明显超阈值。 | ★★★☆☆ |
| 8 | fu-nil-00 | nil | Poll 服务中 exec.TaskID 为空的记录直接查 task，解引用 nil。 | internal/service/poll_service.go:buildResponse + internal/service/poll_service.go:Poll + internal/store/task_exec_store.go:FindAssignedRunning | `nil pointer dereference`。 | 手工塞空 TaskID exec 记录，再调用 Poll。 | ★★☆☆☆ |
| 9 | fu-nil-01 | nil | WrapBizError 传入 `(*bizErr)(nil)` 使 WriteError 的 err 接口 != nil 但内部值为 nil，格式化丢失 message。 | pkg/response/response.go:WrapBizError + internal/handler/util.go:WriteError | HTTP 500 中 message 为空。 | 构造 `var c response.Coder=(*response.bizErr)(nil)` 传 WriteError。 | ★★★★☆ |
| 10 | fu-nil-02 | nil | NewServices 中 cfg 为 nil 分支把 nil StatsService 注入 ProgressService，Report success 调用 Invalidate() 崩溃。 | internal/service/services.go:NewServices + internal/service/progress_service.go:Report + internal/service/stats_service.go:Invalidate | 上报 progress=100 时 panic。 | 手动 NewServices(cfg=nil) 后上报 status=success。 | ★★☆☆☆ |
| 11 | fu-nil-03 | nil | 固件下载对 FilePath=="" 分支，fileutil.Size("") 返回 err，但 response.Internal(nil) 使日志 message=<nil>。 | internal/handler/firmware_handler.go:Download + pkg/fileutil/fileutil.go:Size + pkg/response/response.go:Internal | HTTP 500 日志显示 `<nil>` message。 | 手工写入元数据 FilePath="" 的固件，请求下载。 | ★★★☆☆ |
| 12 | fu-nil-04 | nil | ScanTimeout 中 FindLatestByDevice 返回 nil history 时直接对 nil 赋值字段。 | internal/service/progress_service.go:ScanTimeout + internal/store/history_store.go:FindLatestByDevice | 偶发 panic 空指针。 | 写 exec 但跳过 history，触发 ScanTimeout。 | ★★★☆☆ |
| 13 | fu-slice-00 | slice | CountByVersion 返回的共享切片被 stats.build append，污染其他请求缓存。 | internal/store/device_store.go:CountByVersion + internal/service/stats_service.go:build + internal/handler/stats_handler.go:Overview | 两次 Overview 的 version_distribution 字段互相篡改。 | 先请求 Overview 再新增设备再请求，比对旧响应切片。 | ★★★☆☆ |
| 14 | fu-slice-01 | slice | GrayService.SelectDevices 中 miss/pool 复用设备底层数组，append 容量不足时覆盖命中设备字段。 | internal/service/gray_service.go:SelectDevices + internal/service/task_service.go:assignInitialExecutions | 命中设备版本被 miss 端覆盖（型号/版本错位）。 | 创建灰度后对 miss 设备改 version，断言命中设备字段不变失败。 | ★★★★☆ |
| 15 | fu-slice-02 | slice | DeviceStore.ListByIDs 返回切片按 len(id) 预留容量，调用端再次 append 写回同底层数组。 | internal/store/device_store.go:ListByIDs + internal/service/gray_service.go:SelectDevices + internal/service/poll_service.go:Poll | 两次 ListByIDs 结果互相污染。 | 并发两次 ListByIDs 同 id 并分别修改字段再读回。 | ★★★★☆ |
| 16 | fu-slice-03 | slice | PageParam 对超大 page_size 截断后，store 因 len(list)==0 返回 total=0（应按真实总数）。 | internal/handler/util.go:PageParam + internal/store/model_store.go:List | 列表分页 total=0。 | 请求 `page_size=9999999`，断言 total 与实际一致。 | ★★☆☆☆ |
| 17 | fu-slice-04 | slice | retry.Do 内错误切片跨闭包跨 goroutine append，存在 data race 丢条目。 | pkg/retry/retry.go.Do + internal/service/progress_service.go:Report | `-race` data race；收集失败条目数量不齐。 | 单测包装 retry.Do 跨 goroutine append。 | ★★★☆☆ |
| 18 | fu-error-00 | error | 型号冲突时 service 用 errors.New 重新包装而非 errors.Join/ %w，handler 层 errors.As(ErrConflict) 失效。 | internal/service/model_service.go:Create + internal/handler/util.go:WriteError | HTTP 500 而不是 409。 | 两次 POST /models 同 id，断言第二次返回 409。 | ★★☆☆☆ |
| 19 | fu-error-01 | error | 固件上传 ParseMultipartForm 失败后一律写 ErrUploadTooLarge，磁盘空间/权限错误被误判为 413。 | internal/handler/firmware_handler.go:Upload + internal/handler/util.go:WriteError | 磁盘写满返回 413 而不是 500。 | 上传临时目录置为只读再上传。 | ★★★☆☆ |
| 20 | fu-error-02 | error | fileop.saveReader 的 os.ErrPermission 未用 %w 包装，上层 errors.Is(os.ErrPermission) 失效。 | internal/service/fileop_service.go:saveReader + pkg/fileutil/fileutil.go:SaveFile | 权限错误信息与根因不一致。 | data/firmwares 设置 555 后上传，错误信息不含 permission。 | ★★★★☆ |
| 21 | fu-error-03 | error | TaskService.UpdateStatus 对 ErrTaskState 重新 errors.New，导致 handler 无法识别业务 vs 系统错误。 | internal/service/task_service.go:UpdateStatus + internal/handler/task_handler.go:Action + internal/handler/util.go:WriteError | 重复 cancel 返回 500（应 400）。 | Create→Cancel→Cancel，断言 HTTP 400。 | ★★☆☆☆ |
| 22 | fu-error-04 | error | Heartbeat 未注册设备时字符串 `device not found` 字面值比较，store 层实现替换后判断漂移。 | internal/store/device_store.go:Heartbeat + internal/service/device_service.go:Heartbeat + internal/handler/device_handler.go:Heartbeat | 存在设备心跳返回 404。 | 切换 DeviceStore 实现后重跑心跳单测。 | ★★★☆☆ |
| 23 | fu-context-00 | context | Progress.Report 的 deviceLock 等待过程未检查 ctx.Done，优雅关闭 goroutine 泄漏。 | internal/service/progress_service.go:Report + cmd/server/main.go:run | SIGTERM 后出现 "worker exit timeout"。 | 启动服务 + 100 设备上报后 SIGTERM，观察日志。 | ★★★★☆ |
| 24 | fu-context-01 | context | GrayService.IsHit 不接收 ctx，trace_id 无法透传，日志关联失败。 | internal/service/gray_service.go:IsHit + internal/service/task_service.go:assignInitialExecutions + pkg/logger/logger.go:WithContext | access_log 与 service 日志 trace_id 不一致。 | 日志比对关联 request_id。 | ★★★☆☆ |
| 25 | fu-context-02 | context | StatsService.Get 的 build 子调用不传 ctx，ctx 超时后仍继续执行长计算。 | internal/service/stats_service.go:Get + internal/service/stats_service.go:build + internal/store/device_store.go:CountByVersion | 压测下短超时请求返回后服务端高 CPU。 | context.WithTimeout(1ms) 调 Overview 观察 CPU。 | ★★★★☆ |
| 26 | fu-defer-00 | defer | md5util.Hasher 循环处理多文件忘记 Reset，Sum 返回串联结果。 | pkg/md5util/md5util.go:Hasher + internal/service/fileop_service.go:saveReader | 服务器记录 MD5 与本地独立计算值不一致。 | 连续上传两个文件，比对 MD5。 | ★★★★☆ |
| 27 | fu-defer-01 | defer | 固件上传失败分支 fh.Open 句柄无 close defer，句柄泄漏。 | internal/handler/firmware_handler.go:Upload + internal/service/fileop_service.go:SaveMultipartFile | `lsof -p <pid>` deleted 临时文件累积。 | 2000 次空 multipart 上传后查看进程句柄。 | ★★★☆☆ |
| 28 | fu-defer-02 | defer | RecoveryMiddleware defer 内写响应，若 handler 已 WriteHeader，出现 "superfluous response.WriteHeader"。 | internal/handler/middleware.go:RecoveryMiddleware + pkg/response/response.go:JSON | 访问日志出现 superfluous WriteHeader 警告。 | 构造 handler 先 WriteHeader(200) 再 panic。 | ★★★☆☆ |
| 29 | fu-other-00 | other | fileutil.SafeJoin baseDir 末尾无分隔符时前缀匹配漏检，文件名含 dirname+"/../" 可写上级目录。 | pkg/fileutil/fileutil.go:SafeJoin + internal/service/fileop_service.go:buildUniquePath + internal/handler/firmware_handler.go:Download | 路径逃逸。 | 上传文件名含 baseDir 名 + `/../etc/passwd`，检查落点。 | ★★★★★ |

---

## 缺陷类别统计（编号 0-29，共 30 条）

| 类别 | 数量 | 占比 |
|------|------|------|
| concurrency | 8（#0–#7） | 26.7% |
| nil | 5（#8–#12） | 16.7% |
| slice | 5（#13–#17） | 16.7% |
| error | 5（#18–#22） | 16.7% |
| context | 3（#23–#25） | 10.0% |
| defer | 3（#26–#28） | 10.0% |
| other | 1（#29） | 3.3% |

> 类别最高占比 concurrency=26.7% ≤ 30%，符合约束。

## 单文件缺陷占比校验（编号 0-29，共 30 条）

统计方法：对每条缺陷的「植入位置」按 `+` 拆分，取每个 `包/文件.go:方法` 前缀统计：

| 文件名 | 次数 | 涉及缺陷编号（#） | 占比 |
|--------|------|-------------------|------|
| internal/service/progress_service.go | 5 | #4, #10, #12, #17, #23 | 16.7% |
| internal/service/task_service.go | 5 | #2, #14, #15, #21, #24 | 16.7% |
| internal/store/device_store.go | 5 | #0, #13, #15, #16, #22 | 16.7% |
| pkg/response/response.go | 4 | #9, #11, #28, #29 | 13.3% |
| internal/handler/firmware_handler.go | 3 | #11, #19, #27 | 10.0% |
| internal/service/poll_service.go | 3 | #6, #8, #15 | 10.0% |
| internal/service/stats_service.go | 3 | #1, #13, #25 | 10.0% |
| internal/service/gray_service.go | 3 | #2, #14, #24 | 10.0% |
| pkg/fileutil/fileutil.go | 3 | #11, #20, #29 | 10.0% |
| pkg/cache/cache.go | 2 | #5 | 6.7% |
| pkg/safemap/safemap.go | 2 | #6, #7 | 6.7% |
| 其余 30+ 个文件 | 各 ≤2 | 分散 | <7% |

> 单文件最高占比：16.7% ≤ 30%，符合约束。
