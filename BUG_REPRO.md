# 缺陷复现报告（Bug Reproduction Report）

## 1. 问题概述（Summary）

固件升级服务在暂停升级任务后，设备端仍可继续上报升级进度、轮询接口仍返回升级指令。表现为：任务状态已变更为"暂停"，但设备行为未受影响，进度持续推进，升级指令持续下发，导致暂停操作形同虚设。

## 2. 环境信息（Environment）

- 操作系统：Linux
- Go 版本：go version 输出
- 项目模块：firmware-upgrade
- 运行参数：无特殊参数，使用默认配置
- 硬件信息：与并发/性能无关

## 3. 复现步骤（Steps to Reproduce）

1. 进入项目根目录，执行 `go build ./...` 确保编译通过
2. 启动服务或直接编写测试代码调用服务层接口
3. 创建设备型号（Model）并发布固件（Firmware）
4. 注册一台设备（Device）到该型号下
5. 创建一个全量升级任务（Task），不指定计划时间（立即启动）
6. 设备首次轮询（Poll）获取升级指令，确认 NeedUpgrade=true
7. 设备上报（Report）升级进度至 50%
8. 调用任务暂停接口（UpdateStatus action=pause），确认任务状态已变更为 paused
9. 设备再次上报（Report）进度至 80%
10. 设备再次轮询（Poll）

## 4. 实际结果（Actual Behavior / Observed Output）

- 步骤 9：Report 接口返回成功（err=nil），进度 80% 被正常记录
- 步骤 10：Poll 接口返回 NeedUpgrade=true，仍携带完整的固件下载信息
- 预期在步骤 9、10 中应收到错误或 NeedUpgrade=false，但实际未收到
- RED/GREEN 判定结果：RED（红灯，缺陷存在）

## 5. 期望结果（Expected Behavior）

- 步骤 9：Report 接口应返回错误（如"task not running"），拒绝进度上报
- 步骤 10：Poll 接口应返回 NeedUpgrade=false，不再下发升级指令
- RED/GREEN 判定结果：GREEN（绿灯，缺陷已修复）
- go build 与 go vet 全部通过

## 6. 触发频率（Frequency）

必现（100%）：只要按照上述步骤操作，每次均能稳定复现。

## 7. 影响范围（Impact / Scope）

- 暂停操作失效：运维人员暂停升级任务后，设备端不受控制继续升级，无法及时止损
- 状态不一致：任务层面显示已暂停，但设备执行记录仍为运行中，造成数据混乱
- 潜在安全风险：在错误场景下可能导致设备固件被意外升级，影响生产稳定性
- 所有涉及任务暂停的场景均受影响，包括手动暂停和自动暂停流程

## 8. 附加说明（Additional Notes / Workaround）

临时规避方案：暂停任务后，手动通过数据库或管理后台将所有相关设备的执行记录状态批量更新为 canceled，阻止设备继续上报和获取升级指令。但该操作风险较高，不建议在生产环境频繁使用。