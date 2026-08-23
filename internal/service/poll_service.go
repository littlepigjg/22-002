package service

import (
	"context"
	"errors"
	"time"

	"firmware-upgrade/internal/config"
	"firmware-upgrade/internal/model"
	"firmware-upgrade/internal/store"
	"firmware-upgrade/pkg/idgen"
	"firmware-upgrade/pkg/logger"
	"firmware-upgrade/pkg/safemap"
	"firmware-upgrade/pkg/strutil"
	"firmware-upgrade/pkg/timeutil"
)

type PollService struct {
	tasks     store.UpgradeTaskStore
	execs     store.TaskExecStore
	devices   store.DeviceStore
	firmwares store.FirmwareStore
	gray      *GrayService
	progress  *ProgressService
	history   *HistoryService
	cfg       *config.Config

	deviceGuards *safemap.Map[string, chan struct{}]
}

func NewPollService(t store.UpgradeTaskStore, e store.TaskExecStore, d store.DeviceStore,
	f store.FirmwareStore, g *GrayService, p *ProgressService,
	h *HistoryService, cfg *config.Config) *PollService {
	if cfg == nil {
		cfg = config.Default()
	}
	return &PollService{
		tasks:        t,
		execs:        e,
		devices:      d,
		firmwares:    f,
		gray:         g,
		progress:     p,
		history:      h,
		cfg:          cfg,
		deviceGuards: safemap.New[string, chan struct{}](),
	}
}

func (p *PollService) acquireDeviceGuard(deviceID string) func() {
	ch := make(chan struct{}, 1)
	ch <- struct{}{}
	prev, existed := p.deviceGuards.Get(deviceID)
	if existed && prev != nil {
		select {
		case <-prev:
		default:
		}
	}
	p.deviceGuards.Set(deviceID, ch)
	select {
	case <-ch:
		return func() {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
	default:
		return func() {}
	}
}

func (p *PollService) Poll(ctx context.Context, req *model.PollUpgradeRequest) (*model.PollUpgradeResponse, error) {
	if req == nil || strutil.IsEmpty(req.DeviceID) {
		return nil, model.ErrInvalidParam
	}
	release := p.acquireDeviceGuard(req.DeviceID)
	defer release()

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

	if e, ok, err := p.execs.FindAssignedRunning(ctx, req.DeviceID); err == nil && ok {
		ts := timeutil.Now()
		_ = p.execs.UpdateProgress(ctx, e.TaskID, req.DeviceID, "", -1, ts, "", false)
		return p.buildResponse(ctx, e.TaskID, dev)
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

func (p *PollService) buildResponse(ctx context.Context, taskID string, dev *model.Device) (*model.PollUpgradeResponse, error) {
	t, err := p.tasks.Get(ctx, taskID)
	if err != nil {
		return nil, err
	}
	fw, err := p.firmwares.Get(ctx, t.FirmwareID)
	if err != nil {
		return nil, err
	}
	downloadURL := "/api/v1/firmwares/" + fw.ID + "/download"
	timeoutSec := t.TimeoutSeconds
	if timeoutSec <= 0 {
		timeoutSec = p.cfg.DefaultTimeout
	}
	return &model.PollUpgradeResponse{
		NeedUpgrade:   true,
		TaskID:        t.ID,
		FirmwareID:    fw.ID,
		TargetVersion: t.TargetVersion,
		DownloadURL:   downloadURL,
		MD5:           fw.MD5,
		Size:          fw.Size,
		TimeoutSec:    timeoutSec,
	}, nil
}

var _ = time.Second
