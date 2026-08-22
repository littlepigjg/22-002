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

type progressPrecheck struct {
	task    *model.UpgradeTask
	taskErr error
	history *model.UpgradeHistory
	hasHist bool
}

func (p *ProgressService) precheckReport(ctx context.Context, req *model.ReportProgressRequest) *progressPrecheck {
	pc := &progressPrecheck{}
	pc.task, pc.taskErr = p.tasks.Get(ctx, req.TaskID)
	if h, err := p.histories.FindLatestByDevice(ctx, req.DeviceID, req.TaskID); err == nil {
		pc.history = h
		pc.hasHist = true
	}
	return pc
}

func (p *ProgressService) applyHistoryUpdates(h *model.UpgradeHistory, req *model.ReportProgressRequest, retryInc bool, now time.Time, t *model.UpgradeTask) {
	if h == nil || req == nil {
		return
	}
	h.Status = req.Status
	// 进度只前进不后退：避免迟到/乱序的上报把已到 100 的进度写回 0。
	if req.Progress > h.Progress {
		h.Progress = req.Progress
	}
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
	if req.Status == model.UpgradeStatusSuccess || req.Status == model.UpgradeStatusFailed ||
		req.Status == model.UpgradeStatusCanceled {
		h.FinishedAt = now
		if !h.StartedAt.IsZero() {
			h.DurationMs = now.Sub(h.StartedAt).Milliseconds()
		}
	}
}

func (p *ProgressService) finalizeTaskProgress(ctx context.Context, req *model.ReportProgressRequest, now time.Time) {
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
				_ = p.tasks.SetStatus(ctx, req.TaskID, model.TaskStatusFinished, now)
			}
		}
	}
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
	// 先取设备锁，再读 history：同一设备的并发上报在此串行化，
	// precheckReport 读到的 history 副本必为最新已落库值，避免 RetryCount
	// 因读旧副本而丢失。
	lock := p.deviceLock(req.DeviceID)
	lock.Lock()
	defer lock.Unlock()
	pc := p.precheckReport(ctx, req)
	if pc.taskErr != nil {
		return nil, pc.taskErr
	}
	t := pc.task
	if t == nil {
		return nil, model.ErrTaskNotFound
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
	if pc.hasHist && pc.history != nil {
		p.applyHistoryUpdates(pc.history, req, retryInc, now, t)
		if err := p.histories.Update(ctx, pc.history); err != nil {
			logger.Warn("update history failed", "task_id", req.TaskID, "device_id", req.DeviceID, "err", err)
		}
		if req.Status == model.UpgradeStatusSuccess {
			_ = p.devices.UpdateVersion(ctx, req.DeviceID, t.TargetVersion)
		}
	}
	p.finalizeTaskProgress(ctx, req, now)
	p.stats.Invalidate()
	return p.execs.Get(ctx, req.TaskID, req.DeviceID)
}

type timeoutCandidate struct {
	taskID   string
	deviceID string
	progress int
	history  *model.UpgradeHistory
	hasHist  bool
}

func (p *ProgressService) collectTimeoutCandidates(ctx context.Context, t *model.UpgradeTask, execs []*model.TaskDeviceExecution, now time.Time, timeout time.Duration) []timeoutCandidate {
	var out []timeoutCandidate
	for _, e := range execs {
		if e.Status == model.UpgradeStatusSuccess || e.Status == model.UpgradeStatusFailed ||
			e.Status == model.UpgradeStatusCanceled {
			continue
		}
		age := now.Sub(e.LastReportAt)
		if e.LastReportAt.IsZero() {
			age = now.Sub(e.AssignedAt)
		}
		if age <= timeout {
			continue
		}
		c := timeoutCandidate{
			taskID:   t.ID,
			deviceID: e.DeviceID,
			progress: e.Progress,
		}
		// 不在此处读 history：此函数在设备锁外执行，读到的 history 副本可能
		// 与并发 Report 交错。history 改在 ScanTimeout 取到设备锁后现读，
		// 保证与 Report 路径串行化。
		out = append(out, c)
	}
	return out
}

func (p *ProgressService) applyTimeoutHistory(h *model.UpgradeHistory, now time.Time) {
	if h == nil {
		return
	}
	h.Status = model.UpgradeStatusFailed
	h.ErrorMessage = "timeout: no progress report"
	h.FinishedAt = now
	if !h.StartedAt.IsZero() {
		h.DurationMs = now.Sub(h.StartedAt).Milliseconds()
	}
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
		timeoutDur := time.Duration(t.TimeoutSeconds) * time.Second
		candidates := p.collectTimeoutCandidates(ctx, t, execs, now, timeoutDur)
		for _, c := range candidates {
			dlock := p.deviceLock(c.deviceID)
			dlock.Lock()
			if err := p.execs.UpdateProgress(ctx, c.taskID, c.deviceID, model.UpgradeStatusFailed, c.progress, now, "timeout", false); err != nil {
				dlock.Unlock()
				continue
			}
			// 设备锁内现读 history，确保与 Report 路径串行化，
			// 拿到最新 RetryCount/Progress 后再回写超时终态。
			if h, err := p.histories.FindLatestByDevice(ctx, c.deviceID, t.ID); err == nil && h != nil {
				p.applyTimeoutHistory(h, now)
				_ = p.histories.Update(ctx, h)
			}
			dlock.Unlock()
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
