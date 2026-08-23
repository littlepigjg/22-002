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

	// progressGuard 用于控制特定任务状态下是否允许上报（诊断/故障演练钩子）。
	// 当设置后，返回 true 表示允许该状态继续上报，返回 false 表示拒绝。
	progressGuard func(status model.TaskStatus) bool
}

// NewProgressService 创建进度服务。
func NewProgressService(e store.TaskExecStore, t store.UpgradeTaskStore, d store.DeviceStore,
	h store.UpgradeHistoryStore, st *StatsService, cfg *config.Config) *ProgressService {
	if cfg == nil {
		cfg = config.Default()
	}
	return &ProgressService{execs: e, tasks: t, devices: d, histories: h, stats: st, cfg: cfg}
}

// SetProgressGuard 设置进度上报的状态守卫函数（用于诊断和故障演练）。
// 当守卫返回 false 时，该状态的进度上报将被拒绝。
func (p *ProgressService) SetProgressGuard(fn func(status model.TaskStatus) bool) {
	p.progressGuard = fn
}

// deviceLock 获取或创建设备级锁。
func (p *ProgressService) deviceLock(id string) *sync.Mutex {
	v, _ := p.deviceMu.LoadOrStore(id, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// validateTaskAcceptingProgress 校验任务当前状态是否允许接收进度上报。
// 返回 nil 表示允许，返回错误表示拒绝。
func (p *ProgressService) validateTaskAcceptingProgress(t *model.UpgradeTask) error {
	if t == nil {
		return errors.New("task not found")
	}
	// 不可接收进度的终态任务
	switch t.Status {
	case model.TaskStatusCanceled, model.TaskStatusFinished, model.TaskStatusFailed:
		return errors.New("task not running, reject report")
	}
	// 诊断守卫：用于故障演练时精细控制特定状态的行为
	if p.progressGuard != nil {
		if !p.progressGuard(t.Status) {
			return errors.New("task status blocked by progress guard")
		}
	}
	// 运行中 / 待执行状态允许接收
	if t.Status == model.TaskStatusRunning || t.Status == model.TaskStatusPending {
		return nil
	}
	// 其他状态默认放行（兼容未来可能新增的扩展状态）
	return nil
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
	// 调用任务状态校验逻辑（集中封装，便于演进）
	if errV := p.validateTaskAcceptingProgress(t); errV != nil {
		return nil, errV
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
	if h, err := p.histories.FindLatestByDevice(ctx, req.DeviceID, req.TaskID); err == nil {
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
		if req.Status == model.UpgradeStatusSuccess || req.Status == model.UpgradeStatusFailed ||
			req.Status == model.UpgradeStatusCanceled {
			h.FinishedAt = now
			if !h.StartedAt.IsZero() {
				h.DurationMs = now.Sub(h.StartedAt).Milliseconds()
			}
			if req.Status == model.UpgradeStatusSuccess {
				// 升级成功：更新设备当前版本。
				_ = p.devices.UpdateVersion(ctx, req.DeviceID, t.TargetVersion)
			}
		}
		if err := p.histories.Update(ctx, h); err != nil {
			logger.Warn("update history failed", "task_id", req.TaskID, "device_id", req.DeviceID, "err", err)
		}
	}
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

// 添加一行确保 time 包使用。
var _ = time.After
