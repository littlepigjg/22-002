// Package model 定义本项目所有业务数据模型与 DTO：
// DeviceModel（设备型号）、Firmware（固件版本）、Device（设备实例）、
// UpgradeTask（升级任务）、UpgradeHistory（升级历史）、Statistics（统计视图）
// 以及所有请求/响应 DTO。
package model

import "time"

// ============== 基础枚举 ==============

// DeviceStatus 设备在线状态。
type DeviceStatus string

const (
	// DeviceStatusOnline 在线。
	DeviceStatusOnline DeviceStatus = "online"
	// DeviceStatusOffline 离线。
	DeviceStatusOffline DeviceStatus = "offline"
	// DeviceStatusUnknown 未知（未心跳超过阈值）。
	DeviceStatusUnknown DeviceStatus = "unknown"
)

// TaskStatus 升级任务状态。
type TaskStatus string

const (
	// TaskStatusPending 待执行。
	TaskStatusPending TaskStatus = "pending"
	// TaskStatusRunning 进行中。
	TaskStatusRunning TaskStatus = "running"
	// TaskStatusPaused 暂停。
	TaskStatusPaused TaskStatus = "paused"
	// TaskStatusFinished 已完成（含全部成功或达到终态）。
	TaskStatusFinished TaskStatus = "finished"
	// TaskStatusCanceled 已取消。
	TaskStatusCanceled TaskStatus = "canceled"
	// TaskStatusFailed 任务整体失败。
	TaskStatusFailed TaskStatus = "failed"
)

// UpgradeStatus 单设备升级状态。
type UpgradeStatus string

const (
	// UpgradeStatusPending 等待升级。
	UpgradeStatusPending UpgradeStatus = "pending"
	// UpgradeStatusDownloading 下载中。
	UpgradeStatusDownloading UpgradeStatus = "downloading"
	// UpgradeStatusVerifying 校验固件中。
	UpgradeStatusVerifying UpgradeStatus = "verifying"
	// UpgradeStatusUpgrading 升级中。
	UpgradeStatusUpgrading UpgradeStatus = "upgrading"
	// UpgradeStatusSuccess 升级成功。
	UpgradeStatusSuccess UpgradeStatus = "success"
	// UpgradeStatusFailed 升级失败。
	UpgradeStatusFailed UpgradeStatus = "failed"
	// UpgradeStatusCanceled 升级被取消。
	UpgradeStatusCanceled UpgradeStatus = "canceled"
)

// TaskStrategy 灰度策略类型。
type TaskStrategy string

const (
	// StrategyGrayRatio 按比例灰度（如 10%）。
	StrategyGrayRatio TaskStrategy = "gray_ratio"
	// StrategyDeviceList 指定具体设备列表。
	StrategyDeviceList TaskStrategy = "device_list"
	// StrategyFull 全量升级。
	StrategyFull TaskStrategy = "full"
)

// FirmwareStatus 固件状态。
type FirmwareStatus string

const (
	// FirmwareDraft 草稿（未发布）。
	FirmwareDraft FirmwareStatus = "draft"
	// FirmwarePublished 已发布（可下发）。
	FirmwarePublished FirmwareStatus = "published"
	// FirmwareDeprecated 已废弃。
	FirmwareDeprecated FirmwareStatus = "deprecated"
)

// ============== 实体 ==============

// DeviceModel 设备型号定义。
type DeviceModel struct {
	ID          string    `json:"id"`           // 型号 ID（业务主键，如 GW-100）
	Name        string    `json:"name"`         // 型号显示名
	Description string    `json:"description"`  // 描述
	Vendor      string    `json:"vendor"`       // 厂商
	Arch        string    `json:"arch"`         // CPU 架构，如 arm64、x86_64
	MemoryMB    int       `json:"memory_mb"`    // 典型内存大小（MB）
	FlashMB     int       `json:"flash_mb"`     // Flash 容量（MB）
	Enabled     bool      `json:"enabled"`      // 是否启用
	CreatedAt   time.Time `json:"created_at"`   // 创建时间
	UpdatedAt   time.Time `json:"updated_at"`   // 更新时间
	Extra       string    `json:"extra"`        // 扩展 JSON 字段
}

// Firmware 固件版本实体。
type Firmware struct {
	ID           string         `json:"id"`             // 主键（雪花 ID）
	ModelID      string         `json:"model_id"`       // 所属型号
	Version      string         `json:"version"`        // 版本号，如 v1.2.3
	Name         string         `json:"name"`           // 显示名
	Description  string         `json:"description"`    // 版本说明
	MD5          string         `json:"md5"`            // 文件 MD5
	Size         int64          `json:"size"`           // 文件大小（字节）
	FilePath     string         `json:"file_path"`      // 文件存储路径
	FileName     string         `json:"file_name"`      // 文件名
	Status       FirmwareStatus `json:"status"`         // 草稿/已发布/废弃
	ReleaseDate  time.Time      `json:"release_date"`   // 发布日期
	MinFromVersion string       `json:"min_from_version"` // 最低可升级来源版本（空则不限）
	Signature    string         `json:"signature"`      // 数字签名（可选）
	CreatedBy    string         `json:"created_by"`     // 创建人
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

// Device 具体的设备实例。
type Device struct {
	ID              string       `json:"id"`               // 设备 SN
	ModelID         string       `json:"model_id"`         // 所属型号
	Name            string       `json:"name"`             // 显示名
	CurrentVersion  string       `json:"current_version"`  // 当前固件版本
	TargetVersion   string       `json:"target_version"`   // 目标固件版本（升级中使用）
	Status          DeviceStatus `json:"status"`           // 在线状态
	IP              string       `json:"ip"`               // 最近上报 IP
	MAC             string       `json:"mac"`              // MAC 地址
	Group           string       `json:"group"`            // 设备分组（用于灰度）
	Tags            []string     `json:"tags"`             // 标签
	LastHeartbeatAt time.Time    `json:"last_heartbeat_at"`// 最近心跳时间
	RegisterAt      time.Time    `json:"register_at"`      // 注册时间
	UpdatedAt       time.Time    `json:"updated_at"`
	Extra           string       `json:"extra"`
}

// UpgradeTask 升级任务。
type UpgradeTask struct {
	ID             string       `json:"id"`              // 任务 ID
	Name           string       `json:"name"`            // 任务名
	ModelID        string       `json:"model_id"`        // 目标型号
	FromVersion    string       `json:"from_version"`    // 来源版本（空则不限）
	TargetVersion  string       `json:"target_version"`  // 目标版本号
	FirmwareID     string       `json:"firmware_id"`     // 固件 ID
	Strategy       TaskStrategy `json:"strategy"`        // 灰度策略
	GrayRatio      int          `json:"gray_ratio"`      // 灰度比例 0-100
	DeviceIDs      []string     `json:"device_ids"`      // 指定设备列表
	GroupFilter    []string     `json:"group_filter"`    // 指定分组（空则不限制）
	Status         TaskStatus   `json:"status"`          // 状态
	ScheduleAt     time.Time    `json:"schedule_at"`     // 计划开始时间
	StartTime      time.Time    `json:"start_time"`      // 实际开始时间
	EndTime        time.Time    `json:"end_time"`        // 结束时间
	TimeoutSeconds int          `json:"timeout_seconds"` // 单设备超时（秒）
	MaxRetry       int          `json:"max_retry"`       // 最大重试次数
	Description    string       `json:"description"`
	CreatedBy      string       `json:"created_by"`
	CreatedAt      time.Time    `json:"created_at"`
	UpdatedAt      time.Time    `json:"updated_at"`
	Progress       TaskProgress `json:"progress"`        // 实时进度
}

// TaskProgress 任务进度视图。
type TaskProgress struct {
	Total     int `json:"total"`     // 总设备数
	Pending   int `json:"pending"`   // 等待
	Running   int `json:"running"`   // 进行中
	Success   int `json:"success"`   // 成功
	Failed    int `json:"failed"`    // 失败
	Canceled  int `json:"canceled"`  // 取消
	Timeout   int `json:"timeout"`   // 超时
}

// UpgradeHistory 单设备升级历史（每次升级一条）。
type UpgradeHistory struct {
	ID             string        `json:"id"`
	TaskID         string        `json:"task_id"`
	DeviceID       string        `json:"device_id"`
	ModelID        string        `json:"model_id"`
	FromVersion    string        `json:"from_version"`
	ToVersion      string        `json:"to_version"`
	FirmwareID     string        `json:"firmware_id"`
	Status         UpgradeStatus `json:"status"`
	Progress       int           `json:"progress"`        // 0-100
	RetryCount     int           `json:"retry_count"`
	ErrorMessage   string        `json:"error_message"`
	StartedAt      time.Time     `json:"started_at"`
	FinishedAt     time.Time     `json:"finished_at"`
	DurationMs     int64         `json:"duration_ms"`
	DownloadSpeed  int64         `json:"download_speed"`  // 下载速度 B/s
	MD5Verified    bool          `json:"md5_verified"`    // MD5 是否通过
}

// TaskDeviceExecution 任务中某台设备的执行记录（用于轮询判断是否应升级）。
type TaskDeviceExecution struct {
	TaskID       string        `json:"task_id"`
	DeviceID     string        `json:"device_id"`
	Status       UpgradeStatus `json:"status"`
	Progress     int           `json:"progress"`
	LastReportAt time.Time     `json:"last_report_at"`
	AssignedAt   time.Time     `json:"assigned_at"`
	RetryCount   int           `json:"retry_count"`
}

// Statistics 统计总览视图。
type Statistics struct {
	DeviceCount        int64            `json:"device_count"`
	OnlineCount        int64            `json:"online_count"`
	FirmwareCount      int64            `json:"firmware_count"`
	TaskCount          int64            `json:"task_count"`
	RunningTaskCount   int64            `json:"running_task_count"`
	TotalUpgradeCount  int64            `json:"total_upgrade_count"`
	SuccessUpgradeCount int64           `json:"success_upgrade_count"`
	FailUpgradeCount   int64            `json:"fail_upgrade_count"`
	SuccessRate        float64          `json:"success_rate"`        // 0-1
	VersionDistribution map[string]int64 `json:"version_distribution"` // 版本 -> 设备数
	ModelDistribution  map[string]int64 `json:"model_distribution"`   // 型号 -> 设备数
	DailyUpgradeHistory []DailyUpgrade  `json:"daily_upgrade_history"`
}

// DailyUpgrade 每日升级统计。
type DailyUpgrade struct {
	Date    string `json:"date"`     // YYYY-MM-DD
	Total   int64  `json:"total"`
	Success int64  `json:"success"`
	Failed  int64  `json:"failed"`
}
