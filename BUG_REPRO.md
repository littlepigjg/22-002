# Bug 复现报告：设备状态污染导致升级任务丢失

## 问题概述

在创建固件升级任务后，设备的属性被意外篡改：分组字段（Group）被标记为 `"__prefilter_excluded__"`，当前版本号（CurrentVersion）被替换为任务的目标版本。受此影响，设备在后续轮询时完全无法获取应得的升级任务，Poll 接口返回 `NeedUpgrade=false`，消息为 `device excluded by group filter`。

## 环境信息

- Go 版本：1.23
- 项目：firmware-upgrade（固件升级服务）
- 涉及模块：config, model, store, service, handler
- 测试文件：red_green_test.go

## 复现步骤

1. 启动服务，注册设备 `device-1`，设置 Group 为 `production`，CurrentVersion 为 `v1.0.0`，TargetVersion 为 `v1.0.0`
2. 创建升级任务，指定 FromVersion=`v1.0.0`，TargetVersion=`v2.0.0`，GroupFilter=`production`，DeviceIDs=`device-1`
3. 检查设备状态，发现 Group 已变为 `"__prefilter_excluded__"`，CurrentVersion 已变为 `v2.0.0`
4. 调用 Poll 接口查询设备 `device-1` 的升级状态
5. 观察 Poll 返回 `NeedUpgrade=false`，消息为 `device excluded by group filter`

## 实际结果

- 设备分组被污染：`production` → `"__prefilter_excluded__"`
- 设备版本被污染：`v1.0.0` → `v2.0.0`（目标版本）
- Poll 返回：`NeedUpgrade=false`，`Message="device excluded by group filter"`
- 设备无法执行固件升级

## 期望结果

- 创建设备升级任务后，设备属性不应被修改
- 设备的 CurrentVersion 应保持 `v1.0.0`，Group 应保持 `production`
- Poll 应返回 `NeedUpgrade=true`，设备正确获得升级任务，TaskID 和 TargetVersion 正确

## 触发频率

- 稳定复现：每次创建包含 GroupFilter 或 FromVersion 过滤条件的升级任务后必现
- 无随机性、无竞态条件、与并发无关

## 影响范围

- **受影响模块**：设备存储层（store）、灰度服务层（gray_service）、轮询服务层（poll_service）
- **受影响功能**：固件升级任务创建、设备轮询查询、批量灰度升级
- **业务影响**：所有创建了升级任务的设备将被永久性污染，无法获得任何后续升级任务，导致设备固件版本无法推进
- **影响设备数量**：所有参与过升级任务的设备
- **数据损坏**：设备 Group 和 CurrentVersion 字段被永久修改，需重启服务才能恢复（内存存储）

## 根因分析

### 1. ListByIDs 返回原始指针（DeviceStore 层）

`internal/store/device_store.go` 中的 `ListByIDs` 方法直接返回指向 map 内部 `Device` 对象的指针（而非副本），且使用 `make([]*model.Device, len(ids))` 预分配带长度的切片，导致未匹配位置为 nil。调用方获得的指针与 store 内部数据共享内存。

### 2. SelectDevices 修改设备字段（GrayService 层）

`internal/service/gray_service.go` 中的 `SelectDevices` 方法在预处理阶段直接修改通过共享指针获取的 Device 字段：
- 将不在 GroupFilter 中的设备 Group 标记为 `"__prefilter_excluded__"`
- 将 CurrentVersion 等于 FromVersion 的设备的 CurrentVersion 替换为 TaskVersion

由于指针共享，这些修改直接持久化到 store 的 map 中，污染了设备的原始数据。

### 3. Poll 读取被污染数据（PollService 层）

`internal/service/poll_service.go` 中的 `Poll` 方法从 store 读取已被污染的设备数据：
- CurrentVersion 已变为目标版本，不再匹配任何任务的 FromVersion
- Group 已被标记为 `"__prefilter_excluded__"`
- 新增的检查逻辑在 Group 匹配时直接返回排除响应

### 数据流路径

```
CreateTask → SelectDevices(ListByIDs) → 修改指针 → Store 数据污染
                                                      ↓
Poll → ListByIDs → 读取被污染数据 → 错误的升级决策 → NeedUpgrade=false
```

## 附加说明

- 本缺陷为非并发性缺陷，与并发、竞态条件无关
- 缺陷跨越 3 个文件（device_store.go、gray_service.go、poll_service.go）、3 个函数
- 修复时需修改至少 60 行代码，跨越存储层和服务层
- 修复不得改变任何对外方法的签名、结构体导出字段或构造函数参数列表
- 修复不得移除或修改 WithGuard 后缀的存储方法、SetPanicGuard、RawSnapshot、PanicGuardFn 等故障诊断能力