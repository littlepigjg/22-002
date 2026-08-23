# BUG_REPRO.md - 固件上传系统级错误被误判为 413

## 缺陷概述
- **缺陷ID**: fu-error-01
- **缺陷类别**: error
- **严重程度**: Medium
- **影响范围**: 固件上传接口（POST /api/firmware）

## 问题描述
固件上传接口在遇到系统级错误（磁盘空间不足、临时目录权限拒绝）时，错误地返回 HTTP 413 Request Entity Too Large，而不是预期的 HTTP 500 Internal Server Error。这导致客户端将服务端内部错误误解为自身请求问题，无法正确定位故障原因。

## 根因分析

### 跨文件缺陷链路

```
ParseMultipartForm 失败
        │
        ▼
firmware_handler.go: classifyMultipartFormError()
        │ 仅检测 ENOSPC/EDQUOT，未覆盖 os.IsPermission(EACCES)
        │ buildTypedUploadError() 丢弃 unwrapToRootCause 结果
        ▼
util.go: WriteError() → resolveUploadHTTPStatus()
        │ 未调用 isSystemError()，所有非 ErrInvalidParam 错误一律返回 413
        ▼
  返回 413 而非 500
```

### 缺陷涉及文件
1. **internal/handler/firmware_handler.go**
   - `classifyMultipartFormError()`: 使用 `isErrorKind` 仅检测 `syscall.ENOSPC` 和 `syscall.EDQUOT`，未检测 `os.IsPermission` 覆盖的权限错误
   - `buildTypedUploadError()`: 调用 `unwrapToRootCause(err)` 获取根因但通过 `_ = rootCause` 丢弃结果，未将根因信息传递给下游
   - 辅助函数 `unwrapToRootCause()` 和 `isErrorKind()` 已正确实现但未被充分利用

2. **internal/handler/util.go**
   - `resolveUploadHTTPStatus()`: 仅检查 `model.ErrInvalidParam` 一种错误类型，其余一律返回 413，且从未调用已正确实现的 `isSystemError()` 函数
   - `buildUploadErrorMessage()`: 未根据错误类型构建差异化消息
   - `isSystemError()`: 已正确实现系统错误检测（检查 `os.IsPermission`、`syscall.ENOSPC`、`syscall.EDQUOT`），但从未被调用

### 失效机制
系统级错误在跨文件错误传播链中的两个关键环节被误判：
1. **错误分类层**（firmware_handler.go）：`classifyMultipartFormError` 仅覆盖部分系统错误类型，权限错误（EACCES）未被识别
2. **状态映射层**（util.go）：`resolveUploadHTTPStatus` 将所有非参数错误一律映射为 413，完全忽略系统错误检测

## 复现步骤

### 场景1: 磁盘空间不足
```go
// 模拟磁盘空间不足
err := fmt.Errorf("write temp file: %w", syscall.ENOSPC)
// 触发 ParseMultipartForm 失败
// 当前行为: 返回 413 Request Entity Too Large
// 预期行为: 返回 500 Internal Server Error
```

### 场景2: 权限拒绝
```go
// 模拟临时目录权限不足
err := &os.PathError{
    Op:   "mkdir",
    Path: "/tmp/upload-xxx",
    Err:  syscall.EACCES,
}
// 触发 ParseMultipartForm 失败
// 当前行为: 返回 413 Request Entity Too Large
// 预期行为: 返回 500 Internal Server Error
```

## 实际结果
- HTTP 状态码: **413 Request Entity Too Large**
- 错误消息: "upload processing error" 或系统错误原始消息
- 退出码: 1（测试失败）

## 预期结果
- HTTP 状态码: **500 Internal Server Error**
- 错误消息: 系统级错误描述（如 "no space left on device"）

## 回归测试
- 文件过大场景（`model.ErrUploadTooLarge`）仍应正确返回 **413 Request Entity Too Large**
- 测试用例: `file_too_large_should_still_return_413`

## 修复指南

### 需要修改的函数
1. **`classifyMultipartFormError`** (firmware_handler.go): 
   - 添加 `os.IsPermission(err)` 检查，将权限错误正确识别为系统错误
   
2. **`buildTypedUploadError`** (firmware_handler.go): 
   - 使用 `unwrapToRootCause` 获取根因并存储到 `UploadProcessingError` 中
   
3. **`resolveUploadHTTPStatus`** (util.go): 
   - 调用 `isSystemError()` 检测系统错误，返回 500
   
4. **`buildUploadErrorMessage`** (util.go): 
   - 根据错误类型构建差异化错误消息

### 修复约束
- 不得修改外部 API 签名
- 不得修改结构体导出字段名称和类型
- 不得修改构造函数参数列表
- 不得删除或修改 `UploadProcessingError`、`isSystemError`、`unwrapToRootCause`、`isErrorKind` 的行为
- 不得修改 `WithGuard` 后缀方法、`SetPanicGuard`、`RawSnapshot`、`PanicGuardFn`

## 验证命令
```bash
# 验证缺陷存在
go test . -count=1 -run '^TestRedGreen$'

# 预期输出（修复前）:
# RED（红灯，缺陷未修复）
# disk_full_should_return_500_not_413: FAIL
# permission_denied_should_return_500_not_413: FAIL
# file_too_large_should_still_return_413: PASS

# 预期输出（修复后）:
# GREEN（绿灯，缺陷已修复）
# 所有场景通过
```
