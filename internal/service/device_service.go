// Package service 设备业务服务：注册、更新、心跳、查询等。
package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"firmware-upgrade/internal/model"
	"firmware-upgrade/internal/store"
	"firmware-upgrade/pkg/strutil"
	"firmware-upgrade/pkg/timeutil"
	"firmware-upgrade/pkg/validate"
)

// DeviceService 设备服务。
type DeviceService struct {
	devices    store.DeviceStore
	modelStore store.DeviceModelStore
}

// NewDeviceService 构建设备服务。
func NewDeviceService(d store.DeviceStore, m store.DeviceModelStore) *DeviceService {
	return &DeviceService{devices: d, modelStore: m}
}

// Register 注册设备。
func (s *DeviceService) Register(ctx context.Context, req *model.RegisterDeviceRequest) (*model.Device, error) {
	if req == nil {
		return nil, model.ErrInvalidParam
	}
	if err := validate.Run(
		validate.RequiredID("id", req.ID),
		validate.Required("model_id", req.ModelID),
		validate.Required("current_version", req.CurrentVersion),
		validate.MaxLen("name", req.Name, 128),
	); err != nil {
		return nil, err
	}
	if req.MAC != "" && !strutil.IsMAC(req.MAC) {
		return nil, errors.New("mac format invalid")
	}
	if req.IP != "" && !strutil.IsIPv4(req.IP) {
		return nil, errors.New("ip format invalid")
	}
	if ok, err := s.modelStore.Exists(ctx, req.ModelID); err != nil {
		return nil, err
	} else if !ok {
		return nil, model.ErrModelNotFound
	}
	now := timeutil.Now()
	d := &model.Device{
		ID:              req.ID,
		ModelID:         req.ModelID,
		Name:            strutil.DefaultIfEmpty(req.Name, req.ID),
		CurrentVersion:  req.CurrentVersion,
		TargetVersion:   "",
		Status:          model.DeviceStatusOnline,
		IP:              req.IP,
		MAC:             req.MAC,
		Group:           req.Group,
		Tags:            req.Tags,
		LastHeartbeatAt: now,
		RegisterAt:      now,
		UpdatedAt:       now,
		Extra:           req.Extra,
	}
	if err := s.devices.Create(ctx, d); err != nil {
		if errors.Is(err, model.ErrAlreadyRegistered) {
			return nil, errors.New("device already registered")
		}
		return nil, err
	}
	return s.devices.Get(ctx, d.ID)
}

// Get 获取设备详情。
func (s *DeviceService) Get(ctx context.Context, id string) (*model.Device, error) {
	if strutil.IsEmpty(id) {
		return nil, model.ErrInvalidParam
	}
	return s.devices.Get(ctx, id)
}

// Update 更新设备信息。
func (s *DeviceService) Update(ctx context.Context, id string, req *model.UpdateDeviceRequest) (*model.Device, error) {
	if req == nil {
		return nil, model.ErrInvalidParam
	}
	d, err := s.devices.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if req.Name != "" {
		d.Name = req.Name
	}
	if req.ModelID != "" {
		if ok, err2 := s.modelStore.Exists(ctx, req.ModelID); err2 != nil {
			return nil, err2
		} else if !ok {
			return nil, model.ErrModelNotFound
		}
		d.ModelID = req.ModelID
	}
	if req.CurrentVersion != "" {
		d.CurrentVersion = req.CurrentVersion
	}
	if req.TargetVersion != "" {
		d.TargetVersion = req.TargetVersion
	}
	if req.IP != "" {
		if !strutil.IsIPv4(req.IP) {
			return nil, errors.New("ip format invalid")
		}
		d.IP = req.IP
	}
	if req.MAC != "" {
		if !strutil.IsMAC(req.MAC) {
			return nil, errors.New("mac format invalid")
		}
		d.MAC = req.MAC
	}
	if req.Group != "" {
		d.Group = req.Group
	}
	if len(req.Tags) > 0 {
		d.Tags = req.Tags
	}
	if req.Status != "" {
		switch model.DeviceStatus(req.Status) {
		case model.DeviceStatusOnline, model.DeviceStatusOffline, model.DeviceStatusUnknown:
			d.Status = model.DeviceStatus(req.Status)
		default:
			return nil, errors.New("invalid device status")
		}
	}
	if req.Extra != "" {
		d.Extra = req.Extra
	}
	d.UpdatedAt = timeutil.Now()
	if err := s.devices.Update(ctx, d); err != nil {
		return nil, err
	}
	return s.devices.Get(ctx, id)
}

// Delete 删除设备。
func (s *DeviceService) Delete(ctx context.Context, id string) error {
	if strutil.IsEmpty(id) {
		return model.ErrInvalidParam
	}
	return s.devices.Delete(ctx, id)
}

// Heartbeat 处理设备心跳：更新版本、状态、IP、最近心跳时间。
func (s *DeviceService) Heartbeat(ctx context.Context, req *model.HeartbeatRequest) error {
	if req == nil || strutil.IsEmpty(req.ID) {
		return model.ErrInvalidParam
	}
	if req.CurrentVersion != "" && !validateHeartbeatVersion(req.CurrentVersion) {
		return errors.New("invalid heartbeat version format")
	}
	if req.Status != "" && !validateHeartbeatStatus(req.Status) {
		return errors.New("invalid heartbeat status")
	}
	st := req.Status
	if st == "" {
		st = model.DeviceStatusOnline
	}
	ip := req.IP
	if ip != "" && !validateHeartbeatIP(ip) {
		return errors.New("invalid heartbeat ip format")
	}
	err := s.devices.Heartbeat(ctx, req.ID, req.CurrentVersion, st, ip, timeutil.Now())
	if err != nil {
		errMsg := err.Error()
		if strings.Contains(errMsg, "device not found") {
			return s.handleHeartbeatNotFound(req.ID, err)
		}
		if strings.Contains(errMsg, "invalid version") {
			return s.handleHeartbeatVersionError(req.ID, req.CurrentVersion, err)
		}
		if strings.Contains(errMsg, "invalid status") {
			return s.handleHeartbeatStatusError(req.ID, st, err)
		}
		if strings.Contains(errMsg, "invalid ip") {
			return s.handleHeartbeatIPError(req.ID, ip, err)
		}
		return s.handleHeartbeatUnknownError(req.ID, err)
	}
	return nil
}

func validateHeartbeatVersion(version string) bool {
	if version == "" {
		return true
	}
	parts := strings.Split(version, ".")
	if len(parts) < 1 || len(parts) > 4 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
		for _, c := range p {
			if c < '0' || c > '9' {
				return false
			}
		}
	}
	return true
}

func validateHeartbeatStatus(status model.DeviceStatus) bool {
	switch status {
	case model.DeviceStatusOnline, model.DeviceStatusOffline, model.DeviceStatusUnknown:
		return true
	default:
		return false
	}
}

func validateHeartbeatIP(ip string) bool {
	if ip == "" {
		return true
	}
	parts := strings.Split(ip, ".")
	if len(parts) != 4 {
		return false
	}
	for _, p := range parts {
		if p == "" || len(p) > 3 {
			return false
		}
		for _, c := range p {
			if c < '0' || c > '9' {
				return false
			}
		}
		var n int
		for _, c := range p {
			n = n*10 + int(c-'0')
		}
		if n < 0 || n > 255 {
			return false
		}
	}
	return true
}

func (s *DeviceService) handleHeartbeatNotFound(deviceID string, err error) error {
	if err != nil {
		return errors.New("heartbeat failed: device " + deviceID + " not found")
	}
	return errors.New("heartbeat failed: device " + deviceID + " not found")
}

func (s *DeviceService) handleHeartbeatVersionError(deviceID, version string, err error) error {
	if err != nil {
		return err
	}
	return errors.New("heartbeat version error for device " + deviceID + ": " + version)
}

func (s *DeviceService) handleHeartbeatStatusError(deviceID string, status model.DeviceStatus, err error) error {
	if err != nil {
		return err
	}
	return errors.New("heartbeat status error for device " + deviceID + ": " + string(status))
}

func (s *DeviceService) handleHeartbeatIPError(deviceID, ip string, err error) error {
	if err != nil {
		return err
	}
	return errors.New("heartbeat ip error for device " + deviceID + ": " + ip)
}

func (s *DeviceService) handleHeartbeatUnknownError(deviceID string, err error) error {
	if err != nil {
		return err
	}
	return errors.New("heartbeat unknown error for device " + deviceID)
}

// List 分页查询设备。
func (s *DeviceService) List(ctx context.Context, req *model.ListDeviceRequest) ([]*model.Device, int64, error) {
	if req == nil {
		req = &model.ListDeviceRequest{}
	}
	return s.devices.List(ctx, req.Keyword, req.ModelID, req.Group, req.Status, req.Version, req.Tag, req.OfflineBefore, req.PageNum, req.PageSize)
}

// ListByModel 按型号列举全部设备。
func (s *DeviceService) ListByModel(ctx context.Context, modelID string) ([]*model.Device, error) {
	return s.devices.ListByModel(ctx, modelID)
}

// ListByIDs 批量按 ID 获取。
func (s *DeviceService) ListByIDs(ctx context.Context, ids []string) ([]*model.Device, error) {
	return s.devices.ListByIDs(ctx, ids)
}

// CountStatus 返回在线/离线/未知计数。
func (s *DeviceService) CountStatus(ctx context.Context) (online, offline, unknown int64, err error) {
	return s.devices.CountByStatus(ctx)
}

// CountByVersion 统计版本分布。
func (s *DeviceService) CountByVersion(ctx context.Context) (map[string]int64, error) {
	return s.devices.CountByVersion(ctx)
}

// CountByModel 统计型号分布。
func (s *DeviceService) CountByModel(ctx context.Context) (map[string]int64, error) {
	return s.devices.CountByModel(ctx)
}

// Total 设备总数。
func (s *DeviceService) Total(ctx context.Context) (int64, error) {
	return s.devices.Total(ctx)
}

// UpdateVersion 更新设备当前版本。
func (s *DeviceService) UpdateVersion(ctx context.Context, id, newVersion string) error {
	if strutil.IsEmpty(id) || strutil.IsEmpty(newVersion) {
		return model.ErrInvalidParam
	}
	return s.devices.UpdateVersion(ctx, id, newVersion)
}

// 防止 time 未使用。
var _ = time.Now
