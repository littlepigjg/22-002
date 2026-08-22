// Package model 请求/响应 DTO 定义。
package model

// ============== 设备型号 DTO ==============

// CreateModelRequest 创建设备型号请求。
type CreateModelRequest struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Vendor      string `json:"vendor"`
	Arch        string `json:"arch"`
	MemoryMB    int    `json:"memory_mb"`
	FlashMB     int    `json:"flash_mb"`
	Enabled     *bool  `json:"enabled"`
	Extra       string `json:"extra"`
}

// UpdateModelRequest 更新设备型号请求。
type UpdateModelRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Vendor      string `json:"vendor"`
	Arch        string `json:"arch"`
	MemoryMB    int    `json:"memory_mb"`
	FlashMB     int    `json:"flash_mb"`
	Enabled     *bool  `json:"enabled"`
	Extra       string `json:"extra"`
}

// ListModelRequest 型号列表请求。
type ListModelRequest struct {
	Keyword  string `json:"keyword"`
	Arch     string `json:"arch"`
	Vendor   string `json:"vendor"`
	Enabled  *bool  `json:"enabled"`
	PageNum  int    `json:"page_num"`
	PageSize int    `json:"page_size"`
}

// ============== 固件 DTO ==============

// CreateFirmwareRequest 新建固件元数据请求（上传文件之后调用）。
type CreateFirmwareRequest struct {
	ModelID        string `json:"model_id"`
	Version        string `json:"version"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	MD5            string `json:"md5"`
	Size           int64  `json:"size"`
	FilePath       string `json:"file_path"`
	FileName       string `json:"file_name"`
	MinFromVersion string `json:"min_from_version"`
	Signature      string `json:"signature"`
	CreatedBy      string `json:"created_by"`
	ReleaseDate    string `json:"release_date"` // YYYY-MM-DD 或 YYYY-MM-DD HH:mm:ss
}

// PublishFirmwareRequest 发布固件请求。
type PublishFirmwareRequest struct {
	Action string `json:"action"` // publish / deprecate / draft
}

// ListFirmwareRequest 固件列表请求。
type ListFirmwareRequest struct {
	ModelID   string `json:"model_id"`
	Version   string `json:"version"`
	Keyword   string `json:"keyword"`
	Status    string `json:"status"` // draft/published/deprecated, 空为全部
	PageNum   int    `json:"page_num"`
	PageSize  int    `json:"page_size"`
	SortBy    string `json:"sort_by"`    // created_at / version / size
	SortOrder string `json:"sort_order"` // asc / desc
}

// ============== 设备 DTO ==============

// RegisterDeviceRequest 设备注册请求。
type RegisterDeviceRequest struct {
	ID             string   `json:"id"`
	ModelID        string   `json:"model_id"`
	Name           string   `json:"name"`
	CurrentVersion string   `json:"current_version"`
	IP             string   `json:"ip"`
	MAC            string   `json:"mac"`
	Group          string   `json:"group"`
	Tags           []string `json:"tags"`
	Extra          string   `json:"extra"`
}

// UpdateDeviceRequest 设备更新请求。
type UpdateDeviceRequest struct {
	Name           string   `json:"name"`
	ModelID        string   `json:"model_id"`
	CurrentVersion string   `json:"current_version"`
	TargetVersion  string   `json:"target_version"`
	IP             string   `json:"ip"`
	MAC            string   `json:"mac"`
	Group          string   `json:"group"`
	Tags           []string `json:"tags"`
	Status         string   `json:"status"`
	Extra          string   `json:"extra"`
}

// HeartbeatRequest 设备心跳/上报请求。
type HeartbeatRequest struct {
	ID             string       `json:"id"`
	CurrentVersion string       `json:"current_version"`
	Status         DeviceStatus `json:"status"`
	IP             string       `json:"ip"`
	FreeSpaceMB    int64        `json:"free_space_mb"`
	CPUPercent     int          `json:"cpu_percent"`
	MemoryPercent  int          `json:"memory_percent"`
}

// ListDeviceRequest 设备列表请求。
type ListDeviceRequest struct {
	Keyword       string `json:"keyword"`
	ModelID       string `json:"model_id"`
	Group         string `json:"group"`
	Status        string `json:"status"`
	Version       string `json:"version"`
	Tag           string `json:"tag"`
	PageNum       int    `json:"page_num"`
	PageSize      int    `json:"page_size"`
	OfflineBefore int64  `json:"offline_before"` // 时间戳秒，仅返回心跳早于此时间的设备
}

// ============== 升级任务 DTO ==============

// CreateTaskRequest 创建升级任务请求。
type CreateTaskRequest struct {
	Name           string       `json:"name"`
	ModelID        string       `json:"model_id"`
	FromVersion    string       `json:"from_version"`
	TargetVersion  string       `json:"target_version"`
	FirmwareID     string       `json:"firmware_id"`
	Strategy       TaskStrategy `json:"strategy"`
	GrayRatio      int          `json:"gray_ratio"`
	DeviceIDs      []string     `json:"device_ids"`
	GroupFilter    []string     `json:"group_filter"`
	ScheduleAt     int64        `json:"schedule_at"`     // Unix 秒
	TimeoutSeconds int          `json:"timeout_seconds"` // 0 使用默认
	MaxRetry       int          `json:"max_retry"`
	Description    string       `json:"description"`
	CreatedBy      string       `json:"created_by"`
}

// UpdateTaskRequest 更新任务请求（暂停/恢复/取消）。
type UpdateTaskRequest struct {
	Action string `json:"action"` // pause / resume / cancel / finish
	Reason string `json:"reason"`
}

// ListTaskRequest 任务列表请求。
type ListTaskRequest struct {
	Keyword       string `json:"keyword"`
	ModelID       string `json:"model_id"`
	Status        string `json:"status"`
	FirmwareID    string `json:"firmware_id"`
	TargetVersion string `json:"target_version"`
	PageNum       int    `json:"page_num"`
	PageSize      int    `json:"page_size"`
	SortBy        string `json:"sort_by"`
	SortOrder     string `json:"sort_order"`
}

// TaskDetailResponse 任务详情（含执行情况）。
type TaskDetailResponse struct {
	Task       *UpgradeTask          `json:"task"`
	Executions []*TaskDeviceExecution `json:"executions"`
}

// ============== 设备轮询/进度 DTO ==============

// PollUpgradeRequest 设备轮询是否需要升级请求。
type PollUpgradeRequest struct {
	DeviceID       string `json:"device_id"`
	CurrentVersion string `json:"current_version"`
	ModelID        string `json:"model_id"`
	FreeSpaceMB    int64  `json:"free_space_mb"`
}

// PollUpgradeResponse 设备轮询响应。
type PollUpgradeResponse struct {
	NeedUpgrade   bool   `json:"need_upgrade"`
	TaskID        string `json:"task_id,omitempty"`
	FirmwareID    string `json:"firmware_id,omitempty"`
	TargetVersion string `json:"target_version,omitempty"`
	DownloadURL   string `json:"download_url,omitempty"`
	MD5           string `json:"md5,omitempty"`
	Size          int64  `json:"size,omitempty"`
	TimeoutSec    int    `json:"timeout_sec,omitempty"`
	Message       string `json:"message,omitempty"`
}

// ReportProgressRequest 上报升级进度请求。
type ReportProgressRequest struct {
	TaskID        string        `json:"task_id"`
	DeviceID      string        `json:"device_id"`
	Status        UpgradeStatus `json:"status"`
	Progress      int           `json:"progress"`       // 0-100
	ErrorMessage  string        `json:"error_message"`
	DownloadSpeed int64         `json:"download_speed"` // B/s
	MD5Verified   bool          `json:"md5_verified"`
}

// ============== 升级历史 DTO ==============

// ListHistoryRequest 升级历史查询。
type ListHistoryRequest struct {
	TaskID    string `json:"task_id"`
	DeviceID  string `json:"device_id"`
	ModelID   string `json:"model_id"`
	Status    string `json:"status"` // pending/success/failed...
	StartTs   int64  `json:"start_ts"`
	EndTs     int64  `json:"end_ts"`
	Keyword   string `json:"keyword"`
	PageNum   int    `json:"page_num"`
	PageSize  int    `json:"page_size"`
	SortBy    string `json:"sort_by"`
	SortOrder string `json:"sort_order"`
}

// ============== 通用 ==============

// IDResponse 返回单一 ID。
type IDResponse struct {
	ID string `json:"id"`
}

// MessageResponse 返回简单消息。
type MessageResponse struct {
	Message string `json:"message"`
}

// HealthResponse 健康检查响应。
type HealthResponse struct {
	Status    string `json:"status"`
	Timestamp int64  `json:"timestamp"`
	Uptime    string `json:"uptime"`
	Version   string `json:"version"`
}

// ReadyResponse 就绪检查响应。
type ReadyResponse struct {
	Ready   bool              `json:"ready"`
	Message string            `json:"message,omitempty"`
	Checks  map[string]string `json:"checks"`
}
