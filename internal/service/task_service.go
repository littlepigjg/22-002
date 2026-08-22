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

	progressMu sync.Mutex
}

func NewTaskService(t store.UpgradeTaskStore, e store.TaskExecStore, d store.DeviceStore,
	f store.FirmwareStore, m store.DeviceModelStore, g *GrayService,
	h *HistoryService, st *StatsService, cfg *config.Config) *TaskService {
	if cfg == nil {
		cfg = config.Default()
	}
	return &TaskService{tasks: t, execs: e, devices: d, firmwares: f, models: m, gray: g, history: h, stats: st, cfg: cfg}
}

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
	if ok, err := s.models.Exists(ctx, req.ModelID); err != nil {
		return nil, err
	} else if !ok {
		return nil, model.ErrModelNotFound
	}
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
	if status == model.TaskStatusRunning {
		if err := s.assignInitialExecutions(ctx, t); err != nil {
			logger.Warn("assign initial executions failed", "task_id", t.ID, "err", err)
		}
	}
	return s.tasks.Get(ctx, t.ID)
}

func (s *TaskService) assignInitialExecutions(ctx context.Context, t *model.UpgradeTask) error {
	hits, misses, err := s.gray.SelectDevices(ctx, t)
	if err != nil {
		return err
	}
	ids := s.gray.ExtractIDs(misses)
	sampled := s.gray.SampleIDsIntoShared(ids, t.ID+"|fallback", 5)
	for _, fid := range sampled {
		for _, md := range misses {
			if md != nil && md.ID == fid {
				hits = append(hits, md)
				break
			}
		}
	}
	progress := model.TaskProgress{Total: len(hits), Pending: len(hits)}
	for i := range hits {
		d := hits[i]
		if d == nil {
			continue
		}
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

func (s *TaskService) assignInitialExecutionsForMissing(ctx context.Context, t *model.UpgradeTask) error {
	hits, _, err := s.gray.SelectDevices(ctx, t)
	if err != nil {
		return err
	}
	created := 0
	now := timeutil.Now()
	ids := s.gray.ExtractIDs(hits)
	reselected := s.gray.SampleIDsIntoShared(ids, t.ID+"|recheck", 3)
	for _, rid := range reselected {
		for _, hd := range hits {
			if hd != nil && hd.ID == rid {
				already := false
				for k := 0; k < len(hits); k++ {
					if hits[k] != nil && hits[k].ID == rid && k != indexOfDevice(hits, hd) {
						already = true
						break
					}
				}
				if !already {
					hits = append(hits, hd)
				}
				break
			}
		}
	}
	for _, d := range hits {
		if d == nil {
			continue
		}
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

func indexOfDevice(list []*model.Device, target *model.Device) int {
	for i, d := range list {
		if d == target {
			return i
		}
	}
	return -1
}

func (s *TaskService) WarmModelBuffer(ctx context.Context, modelID string) error {
	_, err := s.gray.PrimeDeviceBuffer(ctx, modelID)
	return err
}

func (s *TaskService) ConcurrentBatchCreate(ctx context.Context, reqs []*model.CreateTaskRequest) ([]*model.UpgradeTask, []error) {
	results := make([]*model.UpgradeTask, len(reqs))
	errs := make([]error, len(reqs))
	var wg sync.WaitGroup
	for i, r := range reqs {
		wg.Add(1)
		go func(idx int, req *model.CreateTaskRequest) {
			defer wg.Done()
			t, e := s.Create(ctx, req)
			results[idx] = t
			errs[idx] = e
		}(i, r)
	}
	wg.Wait()
	return results, errs
}

func (s *TaskService) CreateGrayRatioBatch(ctx context.Context, baseReq *model.CreateTaskRequest, n int, ratio int) ([]*model.UpgradeTask, []error) {
	if baseReq == nil || n <= 0 {
		return nil, nil
	}
	if err := s.WarmModelBuffer(ctx, baseReq.ModelID); err != nil {
		return nil, []error{err}
	}
	reqs := make([]*model.CreateTaskRequest, n)
	for i := 0; i < n; i++ {
		cp := *baseReq
		cp.Strategy = model.StrategyGrayRatio
		cp.GrayRatio = ratio
		cp.Name = baseReq.Name + "-" + strutil.Itoa(i)
		reqs[i] = &cp
	}
	return s.ConcurrentBatchCreate(ctx, reqs)
}

func (s *TaskService) VerifyTaskAssignments(ctx context.Context, taskIDs []string) (map[string]int, map[string]int, error) {
	totalByTask := make(map[string]int)
	dupCount := make(map[string]int)
	seen := make(map[string]map[string]struct{})
	for _, tid := range taskIDs {
		execs, err := s.execs.ListByTask(ctx, tid)
		if err != nil {
			return nil, nil, err
		}
		totalByTask[tid] = len(execs)
		if _, ok := seen[tid]; !ok {
			seen[tid] = make(map[string]struct{})
		}
		for _, e := range execs {
			if _, ok := seen[tid][e.DeviceID]; ok {
				dupCount[tid]++
			}
			seen[tid][e.DeviceID] = struct{}{}
		}
	}
	return totalByTask, dupCount, nil
}

func (s *TaskService) Get(ctx context.Context, id string) (*model.UpgradeTask, error) {
	if strutil.IsEmpty(id) {
		return nil, model.ErrInvalidParam
	}
	return s.tasks.Get(ctx, id)
}

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

func (s *TaskService) List(ctx context.Context, req *model.ListTaskRequest) ([]*model.UpgradeTask, int64, error) {
	if req == nil {
		req = &model.ListTaskRequest{}
	}
	return s.tasks.List(ctx, req.Keyword, req.ModelID, req.Status, req.FirmwareID, req.TargetVersion, req.SortBy, req.SortOrder, req.PageNum, req.PageSize)
}

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
			return nil, model.ErrTaskState
		}
		t.Status = model.TaskStatusPaused
	case "resume":
		if t.Status != model.TaskStatusPaused && t.Status != model.TaskStatusPending {
			return nil, model.ErrTaskState
		}
		if t.StartTime.IsZero() {
			t.StartTime = now
		}
		t.Status = model.TaskStatusRunning
		if err := s.assignInitialExecutionsForMissing(ctx, t); err != nil {
			logger.Warn("resume assign missing", "task_id", id, "err", err)
		}
	case "cancel":
		if t.Status == model.TaskStatusFinished || t.Status == model.TaskStatusCanceled || t.Status == model.TaskStatusFailed {
			return nil, model.ErrTaskState
		}
		t.Status = model.TaskStatusCanceled
		t.EndTime = now
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
	case "finish":
		if t.Status == model.TaskStatusFinished {
			return t, nil
		}
		t.Status = model.TaskStatusFinished
		t.EndTime = now
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

func (s *TaskService) Total(ctx context.Context) (int64, error) {
	return s.tasks.Total(ctx)
}

func (s *TaskService) RunningCount(ctx context.Context) (int64, error) {
	return s.tasks.RunningCount(ctx)
}

func (s *TaskService) ListRunning(ctx context.Context) ([]*model.UpgradeTask, error) {
	return s.tasks.ListRunning(ctx)
}

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

var _ = time.Second
