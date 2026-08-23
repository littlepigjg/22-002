# 缺陷复现报告（Bug Reproduction Report）

## 1. 问题概述（Summary）
固件升级平台的统计总览功能存在数据污染问题：在短时间内连续两次请求统计总览接口，中间插入新设备注册操作后，第一次请求返回的版本分布数据（version_distribution）会被第二次请求的数据覆盖，导致第一次响应的数据失真。

## 2. 环境信息（Environment）
- 操作系统：Linux
- Go 版本：go 1.22
- 项目模块：firmware-upgrade
- 运行参数：使用内存存储，进程内缓存（TTL 默认 5 秒）
- 硬件信息：与并发/性能无关

## 3. 复现步骤（Steps to Reproduce）
1. 进入项目根目录，执行 go build ./... 确保编译通过
2. 创建设备存储容器和统计服务
3. 注册 3 台设备，其中 2 台版本为 v1.0，1 台版本为 v2.0
4. 调用统计服务的 Get 方法获取第一次统计数据，记录 version_distribution 内容
5. 调用 Invalidate 方法使缓存失效（或等待 TTL 过期）
6. 新增 3 台设备，全部版本为 v3.0
7. 再次调用统计服务的 Get 方法获取第二次统计数据
8. 对比第一次返回的 version_distribution 与原始记录是否一致

## 4. 实际结果（Actual Behavior / Observed Output）
- 第一次返回的 version_distribution 内容被污染：原本应为 {v1.0: 2, v2.0: 1}（含派生统计条目），但实际变成了第二次调用的结果 {v3.0: 3}（含新的派生统计条目）
- 两次调用的 version_distribution 指向同一份数据对象，第二次调用修改了第一次调用返回的数据
- 测试判定：RED（红灯，缺陷未修复）
- go test 输出：RED（红灯，缺陷未修复）：version_distribution 在两次请求之间发生了数据污染

## 5. 期望结果（Expected Behavior）
- 第一次返回的 version_distribution 在第二次调用后保持不变，仍为原始的版本分布数据
- 两次调用的统计数据相互独立，互不影响
- 测试判定：GREEN（绿灯，缺陷已修复）
- go test 输出：GREEN（绿灯，缺陷已修复）：version_distribution 在两次请求间保持独立
- go build ./... 编译通过，go vet ./... 无静态警告

## 6. 触发频率（Frequency）
必现（100%）：只要在缓存有效期内进行两次统计请求并中间修改设备数据使缓存重建，就会触发该缺陷。

## 7. 影响范围（Impact / Scope）
- 统计总览接口返回的数据不可靠，可能展示错误的版本分布信息
- 依赖统计数据的管理后台、监控仪表盘会显示错误的数据
- 运维人员根据错误的统计数据做出的决策可能被误导
- 版本分布数据被污染也可能影响其他依赖同一缓存的统计字段

## 8. 附加说明（Additional Notes / Workaround）
临时规避方法：在两次统计请求之间增加足够的间隔时间，或在第一次请求完成后立即复制 version_distribution 数据到独立变量中使用，避免持有原始引用。
