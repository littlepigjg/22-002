// Package service 升级进度上报服务。
package service

import (
	"context"
	"errors"
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
	stats     *StatsService
	cfg       *config.Config

	// 设备级互斥：避免对同一设备的多次上报乱序写入进度。
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
	// 校验状态合法。
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
	// 失败时：重试计数累计（小于 MaxRetry 则回到 Pending，否则为最终 Failed）。
	if req.Status == model.UpgradeStatusFailed {
		exec, errE := p.execs.Get(ctx, req.TaskID, req.DeviceID)
		if errE == nil && exec.RetryCount+1 < t.MaxRetry {
			retryInc = true
		}
	}
	if err := p.execs.UpdateProgress(ctx, req.TaskID, req.DeviceID, req.Status, req.Progress, now, errMsg, retryInc); err != nil {
		return nil, err
	}
	// 更新历史记录。
	p.UpdateHistoryOnProgress(ctx, req.TaskID, req.DeviceID, req.Status, req.Progress,
		req.ErrorMessage, req.DownloadSpeed, req.MD5Verified, retryInc, now)
	// 刷新任务进度（串行：避免大量上报造成高频锁竞争）。
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
			// 全部达终态，自动结束任务。
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
			// 超时。
			if err := p.execs.UpdateProgress(ctx, t.ID, e.DeviceID, model.UpgradeStatusFailed, e.Progress, now, "timeout", false); err != nil {
				continue
			}
			p.MarkHistoryTimeout(ctx, t.ID, e.DeviceID, now)
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

// UpdateHistoryOnProgress 在进度更新时同步历史记录。
func (p *ProgressService) UpdateHistoryOnProgress(ctx context.Context, taskID, deviceID string, status model.UpgradeStatus, progress int, errMsg string, downloadSpeed int64, md5Verified bool, retryInc bool, now time.Time) {
	// FindLatestByDevice 在无历史记录时返回 (nil, nil)，需判空，避免 nil 解引用。
	h, err := p.histories.FindLatestByDevice(ctx, deviceID, taskID)
	if err != nil || h == nil {
		return
	}
	h.Status = status
	h.Progress = progress
	if errMsg != "" {
		h.ErrorMessage = errMsg
	}
	if downloadSpeed > 0 {
		h.DownloadSpeed = downloadSpeed
	}
	h.MD5Verified = md5Verified
	if retryInc {
		h.RetryCount++
	}
	if status == model.UpgradeStatusSuccess || status == model.UpgradeStatusFailed ||
		status == model.UpgradeStatusCanceled {
		h.FinishedAt = now
		if !h.StartedAt.IsZero() {
			h.DurationMs = now.Sub(h.StartedAt).Milliseconds()
		}
		if status == model.UpgradeStatusSuccess {
			_ = p.devices.UpdateVersion(ctx, deviceID, "")
		}
	}
	if errU := p.histories.Update(ctx, h); errU != nil {
		logger.Warn("update history failed", "task_id", taskID, "device_id", deviceID, "err", errU)
	}
}

// MarkHistoryTimeout 将指定设备的历史记录标记为超时失败。
func (p *ProgressService) MarkHistoryTimeout(ctx context.Context, taskID, deviceID string, now time.Time) {
	// 设备可能已被分配执行记录但尚无历史记录（如历史创建失败或被上报前触发超时扫描），
	// FindLatestByDevice 此时返回 (nil, nil)，需显式判空，避免 nil 解引用 panic。
	h, err := p.histories.FindLatestByDevice(ctx, deviceID, taskID)
	if err != nil || h == nil {
		return
	}
	h.Status = model.UpgradeStatusFailed
	h.ErrorMessage = "timeout: no progress report"
	h.FinishedAt = now
	if !h.StartedAt.IsZero() {
		h.DurationMs = now.Sub(h.StartedAt).Milliseconds()
	}
	_ = p.histories.Update(ctx, h)
}

// 添加一行确保 time 包使用。
var _ = time.After
