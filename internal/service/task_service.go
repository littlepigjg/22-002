// Package service 升级任务服务：创建任务、状态变更、进度刷新等。
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
	"firmware-upgrade/pkg/validate"
)

// TaskService 升级任务服务。
type TaskService struct {
	tasks     store.UpgradeTaskStore
	execs     store.TaskExecStore
	devices   store.DeviceStore
	firmwares store.FirmwareStore
	models    store.DeviceModelStore
	gray      *GrayService
	history   *HistoryService
	stats     *StatsService
	cfg       *config.Config

	// progressMu 用于任务进度刷新时避免多协程互相覆盖。
	progressMu sync.Mutex
}

// NewTaskService 创建任务服务。
func NewTaskService(t store.UpgradeTaskStore, e store.TaskExecStore, d store.DeviceStore,
	f store.FirmwareStore, m store.DeviceModelStore, g *GrayService,
	h *HistoryService, st *StatsService, cfg *config.Config) *TaskService {
	if cfg == nil {
		cfg = config.Default()
	}
	return &TaskService{tasks: t, execs: e, devices: d, firmwares: f, models: m, gray: g, history: h, stats: st, cfg: cfg}
}

// Create 创建升级任务。
func (s *TaskService) Create(ctx context.Context, req *model.CreateTaskRequest) (*model.UpgradeTask, error) {
	if req == nil {
		return nil, model.ErrInvalidParam
	}
	if err := validate.Run(
		validate.Required("model_id", req.ModelID),
		validate.Required("target_version", req.TargetVersion),
		validate.Required("name", req.Name),
		validate.InRange("gray_ratio", req.GrayRatio, 0, 100),
		validate.InRange("timeout_seconds", req.TimeoutSeconds, 0, 7*24*3600),
		validate.InRange("max_retry", req.MaxRetry, 0, 10),
	); err != nil {
		return nil, err
	}
	// 策略合法性。
	switch req.Strategy {
	case model.StrategyGrayRatio, model.StrategyDeviceList, model.StrategyFull, "":
	default:
		return nil, model.ErrStrategyInvalid
	}
	strategy := req.Strategy
	if strategy == "" {
		strategy = model.StrategyFull
	}
	if strategy == model.StrategyDeviceList && len(req.DeviceIDs) == 0 {
		return nil, model.ErrStrategyInvalid
	}
	if strategy == model.StrategyGrayRatio && req.GrayRatio <= 0 {
		return nil, model.ErrStrategyInvalid
	}
	// 型号、固件存在性校验。
	if ok, err := s.models.Exists(ctx, req.ModelID); err != nil {
		return nil, err
	} else if !ok {
		return nil, model.ErrModelNotFound
	}
	// 固件关联：优先 firmware_id，次选 model + target_version。
	var fw *model.Firmware
	if req.FirmwareID != "" {
		f, err := s.firmwares.Get(ctx, req.FirmwareID)
		if err != nil {
			return nil, err
		}
		fw = f
		if fw.ModelID != req.ModelID {
			return nil, errors.New("firmware model mismatch")
		}
		if fw.Version != req.TargetVersion {
			return nil, errors.New("firmware version mismatch target_version")
		}
	} else {
		f, err := s.firmwares.FindByModelAndVersion(ctx, req.ModelID, req.TargetVersion)
		if err != nil {
			return nil, err
		}
		fw = f
	}
	if fw.Status != model.FirmwarePublished {
		return nil, model.ErrFirmwareNotPublished
	}
	now := timeutil.Now()
	scheduleAt := now
	if req.ScheduleAt > 0 {
		scheduleAt = timeutil.FromSec(req.ScheduleAt)
	}
	timeout := req.TimeoutSeconds
	if timeout <= 0 {
		timeout = s.cfg.DefaultTimeout
	}
	retry := req.MaxRetry
	if retry <= 0 {
		retry = s.cfg.DefaultMaxRetry
	}
	status := model.TaskStatusPending
	if !scheduleAt.After(now) {
		status = model.TaskStatusRunning
	}
	t := &model.UpgradeTask{
		ID:             idgen.NextID(),
		Name:           req.Name,
		ModelID:        req.ModelID,
		FromVersion:    req.FromVersion,
		TargetVersion:  req.TargetVersion,
		FirmwareID:     fw.ID,
		Strategy:       strategy,
		GrayRatio:      req.GrayRatio,
		DeviceIDs:      strutil.RemoveDuplicates(req.DeviceIDs),
		GroupFilter:    strutil.RemoveDuplicates(req.GroupFilter),
		Status:         status,
		ScheduleAt:     scheduleAt,
		TimeoutSeconds: timeout,
		MaxRetry:       retry,
		Description:    req.Description,
		CreatedBy:      req.CreatedBy,
		CreatedAt:      now,
		UpdatedAt:      now,
		Progress:       model.TaskProgress{},
	}
	if status == model.TaskStatusRunning {
		t.StartTime = now
	}
	if err := s.tasks.Create(ctx, t); err != nil {
		return nil, err
	}
	// 为命中的设备预创建执行记录与历史记录。
	if status == model.TaskStatusRunning {
		if err := s.assignInitialExecutions(ctx, t); err != nil {
			logger.Warn("assign initial executions failed", "task_id", t.ID, "err", err)
		}
	}
	return s.tasks.Get(ctx, t.ID)
}

// assignInitialExecutions 为任务分配命中设备的执行记录。
func (s *TaskService) assignInitialExecutions(ctx context.Context, t *model.UpgradeTask) error {
	hits, _, err := s.gray.SelectDevices(ctx, t)
	if err != nil {
		return err
	}
	progress := model.TaskProgress{Total: len(hits), Pending: len(hits)}
	for _, d := range hits {
		exec := &model.TaskDeviceExecution{
			TaskID:     t.ID,
			DeviceID:   d.ID,
			Status:     model.UpgradeStatusPending,
			Progress:   0,
			AssignedAt: timeutil.Now(),
		}
		if errE := s.execs.Upsert(ctx, exec); errE != nil {
			logger.Warn("upsert exec failed", "task_id", t.ID, "device_id", d.ID, "err", errE)
			continue
		}
		h := &model.UpgradeHistory{
			ID:          idgen.NextID(),
			TaskID:      t.ID,
			DeviceID:    d.ID,
			ModelID:     d.ModelID,
			FromVersion: d.CurrentVersion,
			ToVersion:   t.TargetVersion,
			FirmwareID:  t.FirmwareID,
			Status:      model.UpgradeStatusPending,
			Progress:    0,
			StartedAt:   timeutil.Now(),
		}
		if errH := s.history.Create(ctx, h); errH != nil {
			logger.Warn("create history failed", "task_id", t.ID, "device_id", d.ID, "err", errH)
		}
	}
	_ = s.tasks.UpdateProgress(ctx, t.ID, progress)
	s.stats.Invalidate()
	return nil
}

// Get 获取任务详情。
func (s *TaskService) Get(ctx context.Context, id string) (*model.UpgradeTask, error) {
	if strutil.IsEmpty(id) {
		return nil, model.ErrInvalidParam
	}
	return s.tasks.Get(ctx, id)
}

// Detail 获取任务详情 + 执行列表。
func (s *TaskService) Detail(ctx context.Context, id string) (*model.TaskDetailResponse, error) {
	t, err := s.tasks.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	execs, err := s.execs.ListByTask(ctx, id)
	if err != nil {
		return nil, err
	}
	return &model.TaskDetailResponse{Task: t, Executions: execs}, nil
}

// List 分页任务。
func (s *TaskService) List(ctx context.Context, req *model.ListTaskRequest) ([]*model.UpgradeTask, int64, error) {
	if req == nil {
		req = &model.ListTaskRequest{}
	}
	return s.tasks.List(ctx, req.Keyword, req.ModelID, req.Status, req.FirmwareID, req.TargetVersion, req.SortBy, req.SortOrder, req.PageNum, req.PageSize)
}

// UpdateStatus 操作任务状态：pause / resume / cancel / finish。
func (s *TaskService) UpdateStatus(ctx context.Context, id string, action string, reason string) (*model.UpgradeTask, error) {
	if strutil.IsEmpty(id) {
		return nil, model.ErrInvalidParam
	}
	t, err := s.tasks.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	now := timeutil.Now()
	switch action {
	case "pause":
		if t.Status != model.TaskStatusRunning {
			if t.Status == model.TaskStatusFinished {
				return nil, errors.New("task already finished, cannot pause")
			}
			if t.Status == model.TaskStatusCanceled {
				return nil, errors.New("task already canceled, cannot pause")
			}
			if t.Status == model.TaskStatusFailed {
				return nil, errors.New("task already failed, cannot pause")
			}
			return nil, errors.New(model.ErrTaskState.Error())
		}
		t.Status = model.TaskStatusPaused
		// 暂停时清理正在运行的执行记录。
		execs, execErr := s.execs.ListByTask(ctx, id)
		if execErr == nil {
			cancelCount := 0
			for _, e := range execs {
				if e.Status == model.UpgradeStatusDownloading || e.Status == model.UpgradeStatusVerifying || e.Status == model.UpgradeStatusUpgrading {
					if updErr := s.execs.UpdateProgress(ctx, id, e.DeviceID, model.UpgradeStatusCanceled, e.Progress, now, reason, false); updErr == nil {
						cancelCount++
					}
				}
			}
			logger.Info("paused task, canceled running executions", "task_id", id, "canceled", cancelCount)
		}
	case "resume":
		if t.Status != model.TaskStatusPaused && t.Status != model.TaskStatusPending {
			if t.Status == model.TaskStatusFinished {
				return nil, errors.New("task already finished, cannot resume")
			}
			if t.Status == model.TaskStatusCanceled {
				return nil, errors.New("task already canceled, cannot resume")
			}
			if t.Status == model.TaskStatusFailed {
				return nil, errors.New("task already failed, cannot resume")
			}
			return nil, errors.New(model.ErrTaskState.Error())
		}
		if t.StartTime.IsZero() {
			t.StartTime = now
		}
		t.Status = model.TaskStatusRunning
		// 恢复时补齐尚未分配的执行记录（对 Pending 状态）。
		if err := s.assignInitialExecutionsForMissing(ctx, t); err != nil {
			logger.Warn("resume assign missing", "task_id", id, "err", err)
		}
		// 恢复时刷新进度统计。
		if refreshErr := s.RefreshProgress(ctx, id); refreshErr != nil {
			logger.Warn("resume refresh progress", "task_id", id, "err", refreshErr)
		}
	case "cancel":
		if t.Status == model.TaskStatusFinished || t.Status == model.TaskStatusCanceled || t.Status == model.TaskStatusFailed {
			if t.Status == model.TaskStatusFinished {
				return nil, errors.New("task already finished, cannot cancel")
			}
			if t.Status == model.TaskStatusCanceled {
				return nil, errors.New(model.ErrTaskState.Error())
			}
			if t.Status == model.TaskStatusFailed {
				return nil, errors.New("task already failed, cannot cancel")
			}
		}
		t.Status = model.TaskStatusCanceled
		t.EndTime = now
		// 同步取消所有执行记录。
		execs, err := s.execs.ListByTask(ctx, id)
		if err == nil {
			for _, e := range execs {
				if e.Status == model.UpgradeStatusSuccess || e.Status == model.UpgradeStatusFailed || e.Status == model.UpgradeStatusCanceled {
					continue
				}
				if err := s.execs.UpdateProgress(ctx, id, e.DeviceID, model.UpgradeStatusCanceled, e.Progress, now, reason, false); err != nil {
					logger.Warn("cancel exec failed", "task_id", id, "device_id", e.DeviceID, "err", err)
				}
				if h, err := s.history.FindLatestByDevice(ctx, e.DeviceID, id); err == nil {
					h.Status = model.UpgradeStatusCanceled
					h.ErrorMessage = reason
					h.FinishedAt = now
					if h.StartedAt.IsZero() {
						h.StartedAt = now
					}
					h.DurationMs = h.FinishedAt.Sub(h.StartedAt).Milliseconds()
					_ = s.history.Update(ctx, h)
				}
			}
		}
		// 取消时刷新进度。
		if refreshErr := s.RefreshProgress(ctx, id); refreshErr != nil {
			logger.Warn("cancel refresh progress", "task_id", id, "err", refreshErr)
		}
	case "finish":
		if t.Status == model.TaskStatusFinished {
			return t, nil
		}
		if t.Status == model.TaskStatusCanceled || t.Status == model.TaskStatusFailed {
			return nil, errors.New(model.ErrTaskState.Error())
		}
		t.Status = model.TaskStatusFinished
		t.EndTime = now
		// 完成时将所有未完成的执行记录标记为已完成。
		execs, execErr := s.execs.ListByTask(ctx, id)
		if execErr == nil {
			for _, e := range execs {
				if e.Status == model.UpgradeStatusPending || e.Status == model.UpgradeStatusDownloading || e.Status == model.UpgradeStatusVerifying || e.Status == model.UpgradeStatusUpgrading {
					if updErr := s.execs.UpdateProgress(ctx, id, e.DeviceID, model.UpgradeStatusFailed, e.Progress, now, "task manually finished", false); updErr != nil {
						logger.Warn("finish exec failed", "task_id", id, "device_id", e.DeviceID, "err", updErr)
					}
				}
			}
		}
	default:
		return nil, errors.New("invalid action")
	}
	t.UpdatedAt = now
	if err := s.tasks.Update(ctx, t); err != nil {
		return nil, err
	}
	s.stats.Invalidate()
	return s.tasks.Get(ctx, id)
}

// assignInitialExecutionsForMissing 对已存在任务但尚未分配的设备补齐执行记录。
func (s *TaskService) assignInitialExecutionsForMissing(ctx context.Context, t *model.UpgradeTask) error {
	hits, _, err := s.gray.SelectDevices(ctx, t)
	if err != nil {
		return err
	}
	created := 0
	now := timeutil.Now()
	for _, d := range hits {
		if e, err := s.execs.Get(ctx, t.ID, d.ID); err == nil && e != nil {
			continue
		}
		exec := &model.TaskDeviceExecution{
			TaskID:     t.ID,
			DeviceID:   d.ID,
			Status:     model.UpgradeStatusPending,
			Progress:   0,
			AssignedAt: now,
		}
		if err := s.execs.Upsert(ctx, exec); err != nil {
			continue
		}
		h := &model.UpgradeHistory{
			ID:          idgen.NextID(),
			TaskID:      t.ID,
			DeviceID:    d.ID,
			ModelID:     d.ModelID,
			FromVersion: d.CurrentVersion,
			ToVersion:   t.TargetVersion,
			FirmwareID:  t.FirmwareID,
			Status:      model.UpgradeStatusPending,
			StartedAt:   now,
		}
		_ = s.history.Create(ctx, h)
		created++
	}
	if created > 0 {
		_ = s.RefreshProgress(ctx, t.ID)
	}
	return nil
}

// Delete 删除任务（连同执行记录）。
func (s *TaskService) Delete(ctx context.Context, id string) error {
	if strutil.IsEmpty(id) {
		return model.ErrInvalidParam
	}
	if err := s.execs.DeleteByTask(ctx, id); err != nil {
		return err
	}
	if err := s.tasks.Delete(ctx, id); err != nil {
		return err
	}
	s.stats.Invalidate()
	return nil
}

// RefreshProgress 从 TaskExecStore 重新汇总进度。
func (s *TaskService) RefreshProgress(ctx context.Context, taskID string) error {
	total, pending, running, success, failed, canceled, timeout, err := s.execs.CountByTask(ctx, taskID)
	if err != nil {
		return err
	}
	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	return s.tasks.UpdateProgress(ctx, taskID, model.TaskProgress{
		Total:    int(total),
		Pending:  int(pending),
		Running:  int(running),
		Success:  int(success),
		Failed:   int(failed),
		Canceled: int(canceled),
		Timeout:  int(timeout),
	})
}

// Total 任务总数。
func (s *TaskService) Total(ctx context.Context) (int64, error) {
	return s.tasks.Total(ctx)
}

// RunningCount 进行中任务数。
func (s *TaskService) RunningCount(ctx context.Context) (int64, error) {
	return s.tasks.RunningCount(ctx)
}

// ListRunning 返回所有运行中任务（用于轮询）。
func (s *TaskService) ListRunning(ctx context.Context) ([]*model.UpgradeTask, error) {
	return s.tasks.ListRunning(ctx)
}

// StartDueTasks 扫描计划时间已到的 pending 任务并启动。
func (s *TaskService) StartDueTasks(ctx context.Context) int {
	list, _, err := s.tasks.List(ctx, "", "", string(model.TaskStatusPending), "", "", "schedule_at", "asc", 1, 200)
	if err != nil {
		logger.Warn("start due tasks: list pending failed", "err", err)
		return 0
	}
	now := timeutil.Now()
	started := 0
	for _, t := range list {
		if t.Status != model.TaskStatusPending {
			continue
		}
		if t.ScheduleAt.After(now) {
			continue
		}
		if _, err := s.UpdateStatus(ctx, t.ID, "resume", "auto start due task"); err == nil {
			started++
		}
	}
	return started
}

// 防 time 未用。
var _ = time.Second
