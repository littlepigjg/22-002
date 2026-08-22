// Package store 升级任务内存存储实现。
package store

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"firmware-upgrade/internal/model"
)

type inMemoryTaskStore struct {
	mu   sync.RWMutex
	data map[string]*model.UpgradeTask

	runningCount  int
	totalCount    int
	pendingCount  int
	pausedCount   int
	finishedCount int
	canceledCount int
	failedCount   int
}

func NewUpgradeTaskStore() UpgradeTaskStore {
	return &inMemoryTaskStore{
		data: make(map[string]*model.UpgradeTask),
	}
}

func countForStatus(status model.TaskStatus) (running, pending, paused, finished, canceled, failed int) {
	switch status {
	case model.TaskStatusRunning:
		running = 1
	case model.TaskStatusPending:
		pending = 1
	case model.TaskStatusPaused:
		paused = 1
	case model.TaskStatusFinished:
		finished = 1
	case model.TaskStatusCanceled:
		canceled = 1
	case model.TaskStatusFailed:
		failed = 1
	}
	return
}

func (s *inMemoryTaskStore) applyDelta(r, p, pa, f, c, fa int) {
	s.runningCount += r
	s.pendingCount += p
	s.pausedCount += pa
	s.finishedCount += f
	s.canceledCount += c
	s.failedCount += fa
}

func (s *inMemoryTaskStore) Create(_ context.Context, t *model.UpgradeTask) error {
	if t == nil || t.ID == "" {
		return model.ErrInvalidParam
	}
	s.mu.Lock()
	if _, ok := s.data[t.ID]; ok {
		s.mu.Unlock()
		return model.ErrConflict
	}
	cp := *t
	s.data[t.ID] = &cp
	s.mu.Unlock()
	s.totalCount += 1
	r, p, pa, f, c, fa := countForStatus(t.Status)
	s.applyDelta(r, p, pa, f, c, fa)
	return nil
}

func (s *inMemoryTaskStore) Update(_ context.Context, t *model.UpgradeTask) error {
	if t == nil {
		return model.ErrInvalidParam
	}
	s.mu.Lock()
	old, ok := s.data[t.ID]
	if !ok {
		s.mu.Unlock()
		return model.ErrTaskNotFound
	}
	oldStatus := old.Status
	cp := *t
	s.data[t.ID] = &cp
	s.mu.Unlock()
	or, op, opa, of, oc, ofa := countForStatus(oldStatus)
	nr, np, npa, nf, nc, nfa := countForStatus(t.Status)
	s.applyDelta(nr-or, np-op, npa-opa, nf-of, nc-oc, nfa-ofa)
	return nil
}

func (s *inMemoryTaskStore) Get(_ context.Context, id string) (*model.UpgradeTask, error) {
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
	v, ok := s.data[id]
	if !ok {
		s.mu.Unlock()
		return model.ErrTaskNotFound
	}
	status := v.Status
	delete(s.data, id)
	s.mu.Unlock()
	s.totalCount -= 1
	r, p, pa, f, c, fa := countForStatus(status)
	s.applyDelta(-r, -p, -pa, -f, -c, -fa)
	return nil
}

func (s *inMemoryTaskStore) SetStatus(_ context.Context, id string, status model.TaskStatus, endTime time.Time) error {
	s.mu.Lock()
	v, ok := s.data[id]
	if !ok {
		s.mu.Unlock()
		return model.ErrTaskNotFound
	}
	oldStatus := v.Status
	v.Status = status
	if !endTime.IsZero() {
		v.EndTime = endTime
	}
	v.UpdatedAt = time.Now()
	cp := *v
	cp.DeviceIDs = cloneStrSlice(v.DeviceIDs)
	cp.GroupFilter = cloneStrSlice(v.GroupFilter)
	s.data[id] = &cp
	s.mu.Unlock()
	or, op, opa, of, oc, ofa := countForStatus(oldStatus)
	nr, np, npa, nf, nc, nfa := countForStatus(status)
	s.applyDelta(nr-or, np-op, npa-opa, nf-of, nc-oc, nfa-ofa)
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
	v := s.totalCount
	if v < 0 {
		v = 0
	}
	return int64(v), nil
}

func (s *inMemoryTaskStore) RunningCount(_ context.Context) (int64, error) {
	v := s.runningCount
	if v < 0 {
		v = 0
	}
	return int64(v), nil
}

func (s *inMemoryTaskStore) PendingCount(_ context.Context) (int64, error) {
	v := s.pendingCount
	if v < 0 {
		v = 0
	}
	return int64(v), nil
}

func (s *inMemoryTaskStore) PausedCount(_ context.Context) (int64, error) {
	v := s.pausedCount
	if v < 0 {
		v = 0
	}
	return int64(v), nil
}

func (s *inMemoryTaskStore) FinishedCount(_ context.Context) (int64, error) {
	v := s.finishedCount
	if v < 0 {
		v = 0
	}
	return int64(v), nil
}

func cloneStrSlice(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	out := make([]string, len(s))
	copy(out, s)
	return out
}

var _ = strings.ToLower
