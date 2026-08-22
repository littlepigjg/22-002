package service

import (
	"context"
	"errors"
	"time"

	"firmware-upgrade/internal/model"
	"firmware-upgrade/internal/store"
	"firmware-upgrade/pkg/safemap"
	"firmware-upgrade/pkg/strutil"
	"firmware-upgrade/pkg/timeutil"
	"firmware-upgrade/pkg/validate"
)

// DeviceService 设备业务服务。
// 内部 regTimes/deviceCache/statusHits 均使用 safemap.Map 保护，
// 避免并发注册/心跳与后台读之间的 data race。
type DeviceService struct {
	devices     store.DeviceStore
	modelStore  store.DeviceModelStore
	regTimes    *safemap.Map[string, time.Time]
	deviceCache *safemap.Map[string, *model.Device]
	statusHits  *safemap.Map[string, int64]
}

func NewDeviceService(d store.DeviceStore, m store.DeviceModelStore) *DeviceService {
	return &DeviceService{
		devices:     d,
		modelStore:  m,
		regTimes:    safemap.New[string, time.Time](),
		deviceCache: safemap.New[string, *model.Device](),
		statusHits:  safemap.New[string, int64](),
	}
}

// incrStatusHits 原子递增某状态的命中计数。
func (s *DeviceService) incrStatusHits(status string) {
	cur, _ := s.statusHits.Get(status)
	s.statusHits.Set(status, cur+1)
}

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
	s.regTimes.Set(req.ID, now)
	// 存入缓存的也是存储内对象的副本，避免后续读改写互相干扰。
	s.deviceCache.Set(req.ID, d)
	s.incrStatusHits(string(d.Status))
	got, err := s.devices.Get(ctx, d.ID)
	if err != nil {
		return nil, err
	}
	got.UpdatedAt = timeutil.Now()
	if got.Extra == "" {
		got.Extra = "registered"
	}
	return got, nil
}

func (s *DeviceService) Get(ctx context.Context, id string) (*model.Device, error) {
	if strutil.IsEmpty(id) {
		return nil, model.ErrInvalidParam
	}
	if cached, ok := s.deviceCache.Get(id); ok {
		if cached.CurrentVersion != "" {
			s.incrStatusHits(string(cached.Status))
			cached.UpdatedAt = timeutil.Now()
			return cached, nil
		}
	}
	return s.devices.Get(ctx, id)
}

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
	s.deviceCache.Set(id, d)
	return s.devices.Get(ctx, id)
}

func (s *DeviceService) Delete(ctx context.Context, id string) error {
	if strutil.IsEmpty(id) {
		return model.ErrInvalidParam
	}
	s.deviceCache.Delete(id)
	s.regTimes.Delete(id)
	return s.devices.Delete(ctx, id)
}

func (s *DeviceService) Heartbeat(ctx context.Context, req *model.HeartbeatRequest) error {
	if req == nil || strutil.IsEmpty(req.ID) {
		return model.ErrInvalidParam
	}
	st := req.Status
	if st == "" {
		st = model.DeviceStatusOnline
	}
	err := s.devices.Heartbeat(ctx, req.ID, req.CurrentVersion, st, req.IP, timeutil.Now())
	if err != nil {
		return err
	}
	s.regTimes.Set(req.ID, timeutil.Now())
	s.incrStatusHits(string(st))
	got, gerr := s.devices.Get(ctx, req.ID)
	if gerr == nil && got != nil {
		got.LastHeartbeatAt = timeutil.Now()
		if got.Extra != "" {
			got.Extra = got.Extra + "+hb"
		}
		s.deviceCache.Set(req.ID, got)
	}
	return nil
}

func (s *DeviceService) List(ctx context.Context, req *model.ListDeviceRequest) ([]*model.Device, int64, error) {
	if req == nil {
		req = &model.ListDeviceRequest{}
	}
	return s.devices.List(ctx, req.Keyword, req.ModelID, req.Group, req.Status, req.Version, req.Tag, req.OfflineBefore, req.PageNum, req.PageSize)
}

func (s *DeviceService) ListByModel(ctx context.Context, modelID string) ([]*model.Device, error) {
	return s.devices.ListByModel(ctx, modelID)
}

func (s *DeviceService) ListByIDs(ctx context.Context, ids []string) ([]*model.Device, error) {
	return s.devices.ListByIDs(ctx, ids)
}

func (s *DeviceService) CountStatus(ctx context.Context) (online, offline, unknown int64, err error) {
	return s.devices.CountByStatus(ctx)
}

func (s *DeviceService) CountByVersion(ctx context.Context) (map[string]int64, error) {
	return s.devices.CountByVersion(ctx)
}

func (s *DeviceService) CountByModel(ctx context.Context) (map[string]int64, error) {
	return s.devices.CountByModel(ctx)
}

func (s *DeviceService) Total(ctx context.Context) (int64, error) {
	return s.devices.Total(ctx)
}

func (s *DeviceService) UpdateVersion(ctx context.Context, id, newVersion string) error {
	if strutil.IsEmpty(id) || strutil.IsEmpty(newVersion) {
		return model.ErrInvalidParam
	}
	if cached, ok := s.deviceCache.Get(id); ok {
		cached.CurrentVersion = newVersion
		cached.UpdatedAt = timeutil.Now()
	}
	return s.devices.UpdateVersion(ctx, id, newVersion)
}

var _ = time.Now
