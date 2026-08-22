// Package service 升级进度上报服务。
package service

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	"firmware-upgrade/internal/config"
	"firmware-upgrade/internal/model"
	"firmware-upgrade/internal/store"
	"firmware-upgrade/pkg/logger"
	"firmware-upgrade/pkg/strutil"
	"firmware-upgrade/pkg/timeutil"
)

// ProgressService 升级进度服务。
type ProgressService struct {
	execs     store.TaskExecStore
	tasks     store.UpgradeTaskStore
	devices   store.DeviceStore
	histories store.UpgradeHistoryStore
	history   *HistoryService
	stats     *StatsService
	cfg       *config.Config

	deviceMu sync.Map // map[string]*sync.Mutex
}

// NewProgressService 创建进度服务。
func NewProgressService(e store.TaskExecStore, t store.UpgradeTaskStore, d store.DeviceStore,
	h store.UpgradeHistoryStore, st *StatsService, cfg *config.Config) *ProgressService {
	if cfg == nil {
		cfg = config.Default()
	}
	return &ProgressService{execs: e, tasks: t, devices: d, histories: h, stats: st, cfg: cfg}
}

func (p *ProgressService) BindHistory(hs *HistoryService) {
	p.history = hs
}

// deviceLock 获取或创建设备级锁。
func (p *ProgressService) deviceLock(id string) *sync.Mutex {
	v, _ := p.deviceMu.LoadOrStore(id, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// Report 处理上报请求。返回更新后的执行记录。
func (p *ProgressService) Report(ctx context.Context, req *model.ReportProgressRequest) (*model.TaskDeviceExecution, error) {
	if req == nil || strutil.IsEmpty(req.TaskID) || strutil.IsEmpty(req.DeviceID) {
		return nil, model.ErrInvalidParam
	}
	if req.Progress < 0 || req.Progress > 100 {
		return nil, model.ErrInvalidParam
	}
	switch req.Status {
	case model.UpgradeStatusPending, model.UpgradeStatusDownloading, model.UpgradeStatusVerifying,
		model.UpgradeStatusUpgrading, model.UpgradeStatusSuccess, model.UpgradeStatusFailed,
		model.UpgradeStatusCanceled:
	default:
		return nil, errors.New("invalid upgrade status")
	}

	lock := p.deviceLock(req.DeviceID)
	lock.Lock()
	defer lock.Unlock()

	t, err := p.tasks.Get(ctx, req.TaskID)
	if err != nil {
		return nil, err
	}
	if t.Status == model.TaskStatusCanceled || t.Status == model.TaskStatusFinished || t.Status == model.TaskStatusFailed {
		return nil, errors.New("task not running, reject report")
	}
	now := timeutil.Now()
	errMsg := req.ErrorMessage
	retryInc := false
	if req.Status == model.UpgradeStatusFailed {
		exec, errE := p.execs.Get(ctx, req.TaskID, req.DeviceID)
		if errE == nil && exec.RetryCount+1 < t.MaxRetry {
			retryInc = true
		}
	}
	if err := p.execs.UpdateProgress(ctx, req.TaskID, req.DeviceID, req.Status, req.Progress, now, errMsg, retryInc); err != nil {
		return nil, err
	}
	terminal := req.Status == model.UpgradeStatusSuccess || req.Status == model.UpgradeStatusFailed ||
		req.Status == model.UpgradeStatusCanceled
	if terminal && p.history != nil {
		h := &model.UpgradeHistory{}
		h.ID = req.TaskID + ":" + req.DeviceID + ":terminal:" + strconv.FormatInt(now.UnixNano(), 10)
		h.TaskID = req.TaskID
		h.DeviceID = req.DeviceID
		h.ModelID = t.ModelID
		h.ToVersion = t.TargetVersion
		h.FromVersion = t.FromVersion
		h.FirmwareID = t.FirmwareID
		h.Status = req.Status
		h.Progress = req.Progress
		h.RetryCount = 0
		h.ErrorMessage = req.ErrorMessage
		h.StartedAt = now
		h.FinishedAt = now
		h.DurationMs = 0
		h.DownloadSpeed = req.DownloadSpeed
		h.MD5Verified = req.MD5Verified
		if retryInc {
			h.RetryCount++
		}
		if err := p.history.AppendHistoryRecord(ctx, h); err != nil {
			logger.Warn("append history failed", "task_id", req.TaskID, "device_id", req.DeviceID, "err", err)
		}
		// AppendHistoryRecord 内部已写好 byTask/byDevice/byDay 索引，
		// 这里不再重复 Touch，否则同一条记录会被索引两次，导致 CountDaily 重复计数。
		if req.Status == model.UpgradeStatusSuccess {
			_ = p.devices.UpdateVersion(ctx, req.DeviceID, t.TargetVersion)
		}
	} else if h, err := p.histories.FindLatestByDevice(ctx, req.DeviceID, req.TaskID); err == nil {
		h.Status = req.Status
		h.Progress = req.Progress
		if req.ErrorMessage != "" {
			h.ErrorMessage = req.ErrorMessage
		}
		if req.DownloadSpeed > 0 {
			h.DownloadSpeed = req.DownloadSpeed
		}
		h.MD5Verified = req.MD5Verified
		if retryInc {
			h.RetryCount++
		}
		if terminal {
			h.FinishedAt = now
			if !h.StartedAt.IsZero() {
				h.DurationMs = now.Sub(h.StartedAt).Milliseconds()
			}
			if req.Status == model.UpgradeStatusSuccess {
				_ = p.devices.UpdateVersion(ctx, req.DeviceID, t.TargetVersion)
			}
		}
		if err := p.histories.Update(ctx, h); err != nil {
			logger.Warn("update history failed", "task_id", req.TaskID, "device_id", req.DeviceID, "err", err)
		}
	}
	if req.Status != model.UpgradeStatusDownloading && req.Status != model.UpgradeStatusUpgrading ||
		req.Progress%10 == 0 || req.Progress == 100 {
		total, pending, running, success, failed, canceled, timeout, errC := p.execs.CountByTask(ctx, req.TaskID)
		if errC == nil {
			_ = p.tasks.UpdateProgress(ctx, req.TaskID, model.TaskProgress{
				Total:    int(total),
				Pending:  int(pending),
				Running:  int(running),
				Success:  int(success),
				Failed:   int(failed),
				Canceled: int(canceled),
				Timeout:  int(timeout),
			})
			if int(success+failed+canceled+timeout) == int(total) && total > 0 {
				end := now
				_ = p.tasks.SetStatus(ctx, req.TaskID, model.TaskStatusFinished, end)
			}
		}
	}
	p.stats.Invalidate()
	return p.execs.Get(ctx, req.TaskID, req.DeviceID)
}

// ScanTimeout 扫描超时执行记录并标记为超时失败。
func (p *ProgressService) ScanTimeout(ctx context.Context) int {
	running, err := p.tasks.ListRunning(ctx)
	if err != nil {
		return 0
	}
	handled := 0
	now := timeutil.Now()
	for _, t := range running {
		execs, errL := p.execs.ListByTask(ctx, t.ID)
		if errL != nil {
			continue
		}
		for _, e := range execs {
			if e.Status == model.UpgradeStatusSuccess || e.Status == model.UpgradeStatusFailed ||
				e.Status == model.UpgradeStatusCanceled {
				continue
			}
			age := now.Sub(e.LastReportAt)
			if e.LastReportAt.IsZero() {
				age = now.Sub(e.AssignedAt)
			}
			if age <= time.Duration(t.TimeoutSeconds)*time.Second {
				continue
			}
			if err := p.execs.UpdateProgress(ctx, t.ID, e.DeviceID, model.UpgradeStatusFailed, e.Progress, now, "timeout", false); err != nil {
				continue
			}
			if h, err := p.histories.FindLatestByDevice(ctx, e.DeviceID, t.ID); err == nil {
				h.Status = model.UpgradeStatusFailed
				h.ErrorMessage = "timeout: no progress report"
				h.FinishedAt = now
				if !h.StartedAt.IsZero() {
					h.DurationMs = now.Sub(h.StartedAt).Milliseconds()
				}
				_ = p.histories.Update(ctx, h)
			}
			handled++
		}
		if handled > 0 {
			total, pending, runningC, success, failed, canceled, timeout, errC := p.execs.CountByTask(ctx, t.ID)
			if errC == nil {
				_ = p.tasks.UpdateProgress(ctx, t.ID, model.TaskProgress{
					Total:    int(total),
					Pending:  int(pending),
					Running:  int(runningC),
					Success:  int(success),
					Failed:   int(failed),
					Canceled: int(canceled),
					Timeout:  int(timeout),
				})
			}
		}
	}
	if handled > 0 {
		p.stats.Invalidate()
	}
	return handled
}

var _ = time.After
