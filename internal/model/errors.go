// Package model 通用错误与常量。
package model

import "errors"

// 通用业务错误。
var (
	// ErrNotFound 资源不存在。
	ErrNotFound = errors.New("resource not found")
	// ErrConflict 资源冲突（唯一键）。
	ErrConflict = errors.New("resource conflict")
	// ErrInvalidParam 参数非法。
	ErrInvalidParam = errors.New("invalid param")
	// ErrUnauthorized 未授权。
	ErrUnauthorized = errors.New("unauthorized")
	// ErrForbidden 无权限。
	ErrForbidden = errors.New("forbidden")
	// ErrInternal 内部错误。
	ErrInternal = errors.New("internal error")
	// ErrFirmwareNotFound 固件不存在。
	ErrFirmwareNotFound = errors.New("firmware not found")
	// ErrDeviceNotFound 设备不存在。
	ErrDeviceNotFound = errors.New("device not found")
	// ErrModelNotFound 型号不存在。
	ErrModelNotFound = errors.New("model not found")
	// ErrTaskNotFound 任务不存在。
	ErrTaskNotFound = errors.New("task not found")
	// ErrTaskState 任务状态非法转换。
	ErrTaskState = errors.New("task state transition illegal")
	// ErrFirmwareNotPublished 固件未发布无法创建任务。
	ErrFirmwareNotPublished = errors.New("firmware not published")
	// ErrVersionMismatch 版本号不匹配。
	ErrVersionMismatch = errors.New("version mismatch")
	// ErrMD5Mismatch MD5 不一致。
	ErrMD5Mismatch = errors.New("md5 mismatch")
	// ErrExceedQuota 超出配额。
	ErrExceedQuota = errors.New("exceed quota")
	// ErrAlreadyRegistered 设备已注册。
	ErrAlreadyRegistered = errors.New("device already registered")
	// ErrUploadTooLarge 上传文件过大。
	ErrUploadTooLarge = errors.New("upload file too large")
	// ErrUploadFileEmpty 上传文件为空。
	ErrUploadFileEmpty = errors.New("upload file empty")
	// ErrContextCanceled 上下文已取消。
	ErrContextCanceled = errors.New("context canceled")
	// ErrContextDeadline 上下文超时。
	ErrContextDeadline = errors.New("context deadline exceeded")
	// ErrStrategyInvalid 灰度策略不合法。
	ErrStrategyInvalid = errors.New("strategy invalid")
	// ErrDeviceExcluded 设备未被本任务选中（灰度未命中）。
	ErrDeviceExcluded = errors.New("device excluded from task")
)

// 常量定义。
const (
	// DefaultPageNum 默认页码。
	DefaultPageNum = 1
	// DefaultPageSize 默认页大小。
	DefaultPageSize = 20
	// MaxPageSize 最大页大小。
	MaxPageSize = 500
	// DefaultTimeoutSec 默认升级超时（秒）。
	DefaultTimeoutSec = 30 * 60
	// DefaultMaxRetry 默认升级重试次数。
	DefaultMaxRetry = 3
	// HeartbeatTTLSeconds 心跳超时秒数（超过此时间视为离线）。
	HeartbeatTTLSeconds = 120
	// FirmwareMaxSizeBytes 固件最大上传大小（256MB）。
	FirmwareMaxSizeBytes = 256 * 1024 * 1024
)
