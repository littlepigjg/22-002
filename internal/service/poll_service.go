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

	mu sync.Mutex // 防止并发下重复分配同一设备。

	// pollGuard 用于控制在轮询时哪些任务状态可以被接受（诊断/故障演练钩子）。
	pollGuard func(status model.TaskStatus) bool
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

// SetPollGuard 设置轮询时的任务状态守卫函数（用于诊断和故障演练）。
// 当守卫返回 false 时，该状态的任务将不被接受为有效分配。
func (p *PollService) SetPollGuard(fn func(status model.TaskStatus) bool) {
	p.pollGuard = fn
}

// validatePollTaskStatus 校验轮询时任务状态是否允许分配/返回升级信息。
// 返回 nil 表示允许，返回错误表示拒绝。
func (p *PollService) validatePollTaskStatus(t *model.UpgradeTask) error {
	if t == nil {
		return errors.New("task not found")
	}
	// 已终止的任务不再分配
	switch t.Status {
	case model.TaskStatusCanceled, model.TaskStatusFinished, model.TaskStatusFailed:
		return errors.New("task terminated")
	}
	// 诊断守卫：用于故障演练时精细控制
	if p.pollGuard != nil {
		if !p.pollGuard(t.Status) {
			return errors.New("task status blocked by poll guard")
		}
	}
	// 运行中状态的任务允许分配
	if t.Status == model.TaskStatusRunning {
		return nil
	}
	// 其他状态默认放行（兼容未来扩展）
	return nil
}

// Poll 处理设备轮询请求。
// 1）若设备已有运行中执行记录，直接返回其升级信息；
// 2）否则在所有 running 任务里按灰度命中条件选择最老的匹配任务，并分配执行记录。
func (p *PollService) Poll(ctx context.Context, req *model.PollUpgradeRequest) (*model.PollUpgradeResponse, error) {
	if req == nil || strutil.IsEmpty(req.DeviceID) {
		return nil, model.ErrInvalidParam
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	dev, err := p.devices.Get(ctx, req.DeviceID)
	if err != nil {
		if errors.Is(err, model.ErrDeviceNotFound) {
			// 未注册设备可匿名轮询，但不分配任务。
			return &model.PollUpgradeResponse{NeedUpgrade: false, Message: "device not registered"}, nil
		}
		return nil, err
	}
	// 上报的 ModelID / 版本合并。
	if req.ModelID != "" && dev.ModelID != req.ModelID {
		logger.Warn("poll: device model mismatch, using registered model", "device_id", req.DeviceID)
	}
	if req.CurrentVersion != "" && dev.CurrentVersion != req.CurrentVersion {
		dev.CurrentVersion = req.CurrentVersion
		if err := p.devices.Update(ctx, dev); err != nil {
			logger.Warn("poll: update device version failed", "device_id", req.DeviceID, "err", err)
		}
	}

	// 1. 已分配的进行中记录。
	if e, ok, err := p.execs.FindAssignedRunning(ctx, req.DeviceID); err == nil && ok {
		t, errT := p.tasks.Get(ctx, e.TaskID)
		if errT == nil {
			// 使用统一的任务状态校验逻辑
			if errV := p.validatePollTaskStatus(t); errV != nil {
				return &model.PollUpgradeResponse{NeedUpgrade: false, Message: errV.Error()}, nil
			}
		}
		return p.buildResponse(ctx, e.TaskID, dev)
	}

	// 2. 找到可分配的 running 任务。
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
		// 灰度命中。
		res := p.gray.IsHit(t, dev, t.DeviceIDs)
		if !res.Hit {
			continue
		}
		// 已经存在则跳过（FindAssignedRunning 返回 false 可能是非运行中状态）。
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
		// 同步创建历史记录。
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

// buildResponse 构造升级响应（包含下载链接与固件元数据）。
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

// 防 time 未使用。
var _ = time.Second
