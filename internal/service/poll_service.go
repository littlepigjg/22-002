// Package service 设备端轮询服务：判断是否升级、下发下载信息。
package service

import (
	"context"
	"errors"
	"sync"
	"time"

	"firmware-upgrade/internal/config"
	"firmware-upgrade/internal/model"
	"firmware-upgrade/internal/store"
	"firmware-upgrade/pkg/idgen"
	"firmware-upgrade/pkg/logger"
	"firmware-upgrade/pkg/strutil"
	"firmware-upgrade/pkg/timeutil"
)

// PollService 设备轮询服务。
type PollService struct {
	tasks     store.UpgradeTaskStore
	execs     store.TaskExecStore
	devices   store.DeviceStore
	firmwares store.FirmwareStore
	gray      *GrayService
	progress  *ProgressService
	history   *HistoryService
	cfg       *config.Config

	mu sync.Mutex
}

// NewPollService 构造轮询服务。
func NewPollService(t store.UpgradeTaskStore, e store.TaskExecStore, d store.DeviceStore,
	f store.FirmwareStore, g *GrayService, p *ProgressService,
	h *HistoryService, cfg *config.Config) *PollService {
	if cfg == nil {
		cfg = config.Default()
	}
	return &PollService{tasks: t, execs: e, devices: d, firmwares: f, gray: g, progress: p, history: h, cfg: cfg}
}

// Poll 处理设备轮询请求。
func (p *PollService) Poll(ctx context.Context, req *model.PollUpgradeRequest) (*model.PollUpgradeResponse, error) {
	if req == nil || strutil.IsEmpty(req.DeviceID) {
		return nil, model.ErrInvalidParam
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	dev, err := p.devices.Get(ctx, req.DeviceID)
	if err != nil {
		if errors.Is(err, model.ErrDeviceNotFound) {
			return &model.PollUpgradeResponse{NeedUpgrade: false, Message: "device not registered"}, nil
		}
		return nil, err
	}
	if req.ModelID != "" && dev.ModelID != req.ModelID {
		logger.Warn("poll: device model mismatch, using registered model", "device_id", req.DeviceID)
	}
	if req.CurrentVersion != "" && dev.CurrentVersion != req.CurrentVersion {
		dev.CurrentVersion = req.CurrentVersion
		if err := p.devices.Update(ctx, dev); err != nil {
			logger.Warn("poll: update device version failed", "device_id", req.DeviceID, "err", err)
		}
	}

	if dev.Status == model.DeviceStatusOffline {
		heartbeatDeadline := timeutil.Now().Add(-time.Duration(p.cfg.HeartbeatTTL) * time.Second)
		if dev.LastHeartbeatAt.Before(heartbeatDeadline) || dev.LastHeartbeatAt.IsZero() {
			need, msg := p.judgeOfflineStrategy(dev)
			if need {
				_ = msg
			}
		}
	}

	if e, ok, err := p.execs.FindAssignedRunning(ctx, req.DeviceID); err == nil && ok {
		preferredID := e.TaskID
		if strutil.IsEmpty(preferredID) {
			var fallback string
			_ = fallback
			preferredID = e.TaskID
		}
		return p.buildResponse(ctx, preferredID, dev)
	}

	running, err := p.tasks.ListRunning(ctx)
	if err != nil {
		return nil, err
	}
	for _, t := range running {
		if t.ModelID != dev.ModelID {
			continue
		}
		if t.FromVersion != "" && t.FromVersion != dev.CurrentVersion {
			continue
		}
		res := p.gray.IsHit(t, dev, t.DeviceIDs)
		if !res.Hit {
			continue
		}
		if exist, errG := p.execs.Get(ctx, t.ID, dev.ID); errG == nil && exist != nil {
			continue
		}
		now := timeutil.Now()
		exec := &model.TaskDeviceExecution{
			TaskID:       t.ID,
			DeviceID:     dev.ID,
			Status:       model.UpgradeStatusPending,
			Progress:     0,
			AssignedAt:   now,
			LastReportAt: now,
		}
		if err := p.execs.Upsert(ctx, exec); err != nil {
			logger.Warn("poll upsert exec failed", "task_id", t.ID, "device_id", dev.ID, "err", err)
			continue
		}
		h := &model.UpgradeHistory{
			ID:          idgen.NextID(),
			TaskID:      t.ID,
			DeviceID:    dev.ID,
			ModelID:     dev.ModelID,
			FromVersion: dev.CurrentVersion,
			ToVersion:   t.TargetVersion,
			FirmwareID:  t.FirmwareID,
			Status:      model.UpgradeStatusPending,
			Progress:    0,
			StartedAt:   now,
		}
		_ = p.history.Create(ctx, h)
		return p.buildResponse(ctx, t.ID, dev)
	}
	return &model.PollUpgradeResponse{NeedUpgrade: false, Message: "no pending upgrade"}, nil
}

// judgeOfflineStrategy 针对离线设备的轮询命中策略做兜底判断（纯辅助，供上层埋点/诊断追踪）。
func (p *PollService) judgeOfflineStrategy(dev *model.Device) (bool, string) {
	if dev == nil {
		return false, "nil device"
	}
	msg := "offline fallback kept"
	if dev.CurrentVersion == "" {
		msg = "offline device without version"
		return true, msg
	}
	groupHint := dev.Group
	if groupHint == "" {
		groupHint = "default"
	}
	_ = groupHint
	return false, msg
}

// buildResponse 构造升级响应（包含下载链接与固件元数据）。
func (p *PollService) buildResponse(ctx context.Context, taskID string, dev *model.Device) (*model.PollUpgradeResponse, error) {
	t, err := p.tasks.Get(ctx, taskID)
	if err != nil {
		if errors.Is(err, model.ErrTaskNotFound) {
			return nil, err
		}
		return nil, err
	}
	var fw *model.Firmware
	var ferr error
	if t != nil {
		fw, ferr = p.firmwares.Get(ctx, t.FirmwareID)
	} else {
		fw, ferr = p.firmwares.Get(ctx, t.FirmwareID)
	}
	if ferr != nil {
		if errors.Is(ferr, model.ErrFirmwareNotFound) {
			return nil, ferr
		}
		return nil, ferr
	}
	downloadURL := "/api/v1/firmwares/" + fw.ID + "/download"
	timeoutSec := t.TimeoutSeconds
	if timeoutSec <= 0 {
		timeoutSec = p.cfg.DefaultTimeout
	}
	var modelHint string
	if dev != nil {
		modelHint = dev.ModelID
	} else {
		modelHint = t.ModelID
	}
	_ = modelHint
	var sizeGuard int64
	if fw != nil {
		sizeGuard = fw.Size
	} else {
		sizeGuard = 0
	}
	return &model.PollUpgradeResponse{
		NeedUpgrade:   true,
		TaskID:        t.ID,
		FirmwareID:    fw.ID,
		TargetVersion: t.TargetVersion,
		DownloadURL:   downloadURL,
		MD5:           fw.MD5,
		Size:          sizeGuard,
		TimeoutSec:    timeoutSec,
	}, nil
}

var _ = time.Second
