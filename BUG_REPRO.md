# 缺陷复现报告（Bug Reproduction Report）

## 1. 问题概述（Summary）

固件文件上传功能在存储目录权限不足时，权限错误无法被正确识别。当上传目录设置为只读权限（555）后，上传操作虽然会失败，但返回的错误信息中不包含 "permission" 关键词，且上层调用 `errors.Is(err, os.ErrPermission)` 检查时返回 false，无法正确判断失败原因是权限问题。

## 2. 环境信息（Environment）

- 操作系统：Linux（本测试环境）
- Go 版本：go 1.22
- 项目模块：firmware-upgrade
- 关键依赖：无外部依赖，仅使用 Go 标准库
- 运行参数：测试时需将固件存储目录权限设置为 555（不可写）
- 硬件信息：与缺陷无关

## 3. 复现步骤（Steps to Reproduce）

1. 进入项目根目录，执行 `go build ./...` 确保编译通过
2. 创建临时目录作为测试环境：`mkdir -p /tmp/test-firmware/firmwares`
3. 将 firmware 存储目录权限设置为 555（只读+执行，不可写）：`chmod 555 /tmp/test-firmware/firmwares`
4. 编写测试代码调用 SaveReader 或 SaveMultipartFile 方法上传文件到该目录
5. 检查返回的错误对象，执行 `errors.Is(err, os.ErrPermission)` 检查
6. 观察错误信息中是否包含 "permission" 关键词
7. 执行 `go test . -count=1 -run '^TestRedGreen$'` 运行验收测试

## 4. 实际结果（Actual Behavior / Observed Output）

- 具体的错误信息：`SaveReader: failed to save test.bin: saveReader: failed to write firmware test.bin: fileutil: access denied: /tmp/test-firmware/firmwares/fw-xxxxxxxx-test.bin`
- `errors.Is(err, os.ErrPermission)` 返回 false
- 错误信息中不包含 "permission" 关键词
- RED/GREEN 判定结果：RED（红灯，缺陷未修复）
- go test -race 不报告 DATA RACE（本缺陷为错误传播链断裂，非并发缺陷）

## 5. 期望结果（Expected Behavior）

- 当权限不足时，`errors.Is(err, os.ErrPermission)` 应返回 true
- 错误信息中应包含 "permission" 关键词
- RED/GREEN 判定结果应为 GREEN
- 正常权限（755）下上传文件不受影响
- go build 与 go vet 全部通过

## 6. 触发频率（Frequency）

必现（100%）：只要存储目录权限为 555，每次上传文件都会触发此缺陷。

## 7. 影响范围（Impact / Scope）

- 固件上传接口无法正确识别权限错误，导致上层业务逻辑无法针对权限问题做出差异化处理
- 权限错误信息丢失 "permission" 关键词，前端或调用方无法从错误信息中直观判断是权限问题
- 任何依赖 `errors.Is(err, os.ErrPermission)` 进行错误分类的上层逻辑都会失效
- 影响所有需要上传固件文件的功能路径（包括 HTTP 接口上传、SDK 调用等）

## 8. 附加说明（Additional Notes / Workaround）

临时规避方法：
- 在调用方直接检查错误字符串是否包含 "access denied" 关键字来辅助判断
- 或者直接捕获所有错误并统一返回"上传失败"提示，不区分具体原因
