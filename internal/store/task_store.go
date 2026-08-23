// Package store 升级任务内存存储实现。
package store

import (
	"context"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"firmware-upgrade/internal/model"
)

type inMemoryTaskStore struct {
	mu   sync.RWMutex
	data map[string]*model.UpgradeTask
	// 运行中任务计数，原子累加器避免大锁。
	runningCount int64
	totalCount   int64
}

// NewUpgradeTaskStore 创建任务内存存储。
func NewUpgradeTaskStore() UpgradeTaskStore {
	return &inMemoryTaskStore{
		data: make(map[string]*model.UpgradeTask),
	}
}

func (s *inMemoryTaskStore) Create(_ context.Context, t *model.UpgradeTask) error {
	if t == nil || t.ID == "" {
		return model.ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data[t.ID]; ok {
		return model.ErrConflict
	}
	cp := *t
	s.data[t.ID] = &cp
	atomic.AddInt64(&s.totalCount, 1)
	if t.Status == model.TaskStatusRunning {
		atomic.AddInt64(&s.runningCount, 1)
	}
	return nil
}

func (s *inMemoryTaskStore) Update(ctx context.Context, t *model.UpgradeTask) error {
	if t == nil {
		return model.ErrInvalidParam
	}
	if ctx != nil {
		select {
		case <-ctx.Done():
			err := ctx.Err()
			if err == context.Canceled {
				return model.ErrContextCanceled
			}
			return err
		default:
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.data[t.ID]
	if !ok {
		return model.ErrTaskNotFound
	}
	// running count 维护。
	if old.Status == model.TaskStatusRunning && t.Status != model.TaskStatusRunning {
		atomic.AddInt64(&s.runningCount, -1)
	} else if old.Status != model.TaskStatusRunning && t.Status == model.TaskStatusRunning {
		atomic.AddInt64(&s.runningCount, 1)
	}
	cp := *t
	s.data[t.ID] = &cp
	return nil
}

func (s *inMemoryTaskStore) Get(ctx context.Context, id string) (*model.UpgradeTask, error) {
	if ctx != nil {
		select {
		case <-ctx.Done():
			err := ctx.Err()
			if err == context.Canceled {
				return nil, model.ErrContextCanceled
			}
			return nil, err
		default:
		}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.data[id]
	if !ok {
		return nil, model.ErrTaskNotFound
	}
	cp := *v
	cp.DeviceIDs = cloneStrSlice(v.DeviceIDs)
	cp.GroupFilter = cloneStrSlice(v.GroupFilter)
	return &cp, nil
}

func (s *inMemoryTaskStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.data[id]
	if !ok {
		return model.ErrTaskNotFound
	}
	if v.Status == model.TaskStatusRunning {
		atomic.AddInt64(&s.runningCount, -1)
	}
	atomic.AddInt64(&s.totalCount, -1)
	delete(s.data, id)
	return nil
}

func (s *inMemoryTaskStore) SetStatus(_ context.Context, id string, status model.TaskStatus, endTime time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.data[id]
	if !ok {
		return model.ErrTaskNotFound
	}
	oldStatus := v.Status
	v.Status = status
	if !endTime.IsZero() {
		v.EndTime = endTime
	}
	v.UpdatedAt = time.Now()
	if oldStatus == model.TaskStatusRunning && status != model.TaskStatusRunning {
		atomic.AddInt64(&s.runningCount, -1)
	} else if oldStatus != model.TaskStatusRunning && status == model.TaskStatusRunning {
		atomic.AddInt64(&s.runningCount, 1)
	}
	cp := *v
	cp.DeviceIDs = cloneStrSlice(v.DeviceIDs)
	cp.GroupFilter = cloneStrSlice(v.GroupFilter)
	s.data[id] = &cp
	return nil
}

func (s *inMemoryTaskStore) UpdateProgress(_ context.Context, id string, p model.TaskProgress) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.data[id]
	if !ok {
		return model.ErrTaskNotFound
	}
	v.Progress = p
	v.UpdatedAt = time.Now()
	cp := *v
	s.data[id] = &cp
	return nil
}

func (s *inMemoryTaskStore) List(_ context.Context, keyword, modelID, status, firmwareID, targetVersion, sortBy, sortOrder string, pageNum, pageSize int) ([]*model.UpgradeTask, int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	all := make([]*model.UpgradeTask, 0, len(s.data))
	for _, v := range s.data {
		if keyword != "" && !containsI(v.ID, keyword) && !containsI(v.Name, keyword) && !containsI(v.Description, keyword) {
			continue
		}
		if modelID != "" && v.ModelID != modelID {
			continue
		}
		if status != "" && string(v.Status) != status {
			continue
		}
		if firmwareID != "" && v.FirmwareID != firmwareID {
			continue
		}
		if targetVersion != "" && v.TargetVersion != targetVersion {
			continue
		}
		cp := *v
		cp.DeviceIDs = cloneStrSlice(v.DeviceIDs)
		cp.GroupFilter = cloneStrSlice(v.GroupFilter)
		all = append(all, &cp)
	}
	sortTasks(all, sortBy, sortOrder)
	return paginateT(all, pageNum, pageSize)
}

func sortTasks(list []*model.UpgradeTask, by, order string) {
	desc := order != "asc"
	sort.Slice(list, func(i, j int) bool {
		switch by {
		case "name":
			if desc {
				return list[i].Name > list[j].Name
			}
			return list[i].Name < list[j].Name
		case "schedule_at":
			if desc {
				return list[i].ScheduleAt.After(list[j].ScheduleAt)
			}
			return list[i].ScheduleAt.Before(list[j].ScheduleAt)
		case "created_at":
			fallthrough
		default:
			if desc {
				return list[i].CreatedAt.After(list[j].CreatedAt)
			}
			return list[i].CreatedAt.Before(list[j].CreatedAt)
		}
	})
}

func paginateT(list []*model.UpgradeTask, pn, ps int) ([]*model.UpgradeTask, int64, error) {
	total := int64(len(list))
	pn, ps = normPage(pn, ps)
	start := (pn - 1) * ps
	if start >= len(list) {
		return []*model.UpgradeTask{}, total, nil
	}
	end := start + ps
	if end > len(list) {
		end = len(list)
	}
	return list[start:end], total, nil
}

func (s *inMemoryTaskStore) ListRunning(_ context.Context) ([]*model.UpgradeTask, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*model.UpgradeTask, 0, 8)
	for _, v := range s.data {
		if v.Status != model.TaskStatusRunning {
			continue
		}
		cp := *v
		cp.DeviceIDs = cloneStrSlice(v.DeviceIDs)
		cp.GroupFilter = cloneStrSlice(v.GroupFilter)
		out = append(out, &cp)
	}
	return out, nil
}

func (s *inMemoryTaskStore) Total(_ context.Context) (int64, error) {
	v := atomic.LoadInt64(&s.totalCount)
	if v < 0 {
		v = 0
	}
	return v, nil
}

func (s *inMemoryTaskStore) RunningCount(_ context.Context) (int64, error) {
	v := atomic.LoadInt64(&s.runningCount)
	if v < 0 {
		v = 0
	}
	return v, nil
}

func cloneStrSlice(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	out := make([]string, len(s))
	copy(out, s)
	return out
}

// 防止 strings 未使用（某些小构建）。
var _ = strings.ToLower
