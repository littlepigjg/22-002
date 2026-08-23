package store

import (
	"context"
	"sync"
	"time"

	"firmware-upgrade/internal/model"
	"firmware-upgrade/pkg/safemap"
)

type inMemoryTaskExecStore struct {
	mu       sync.RWMutex
	data     *safemap.Map[string, *model.TaskDeviceExecution]
	byTask   map[string]map[string]struct{}
	byDevice map[string]map[string]struct{}
}

func NewTaskExecStore() TaskExecStore {
	return &inMemoryTaskExecStore{
		data:     safemap.New[string, *model.TaskDeviceExecution](),
		byTask:   make(map[string]map[string]struct{}),
		byDevice: make(map[string]map[string]struct{}),
	}
}

func execKey(tid, did string) string { return tid + "|" + did }

func (s *inMemoryTaskExecStore) Upsert(_ context.Context, e *model.TaskDeviceExecution) error {
	if e == nil || e.TaskID == "" || e.DeviceID == "" {
		return model.ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := execKey(e.TaskID, e.DeviceID)
	cp := *e
	s.data.Set(k, &cp)
	if _, ok := s.byTask[e.TaskID]; !ok {
		s.byTask[e.TaskID] = make(map[string]struct{})
	}
	s.byTask[e.TaskID][e.DeviceID] = struct{}{}
	if _, ok := s.byDevice[e.DeviceID]; !ok {
		s.byDevice[e.DeviceID] = make(map[string]struct{})
	}
	s.byDevice[e.DeviceID][e.TaskID] = struct{}{}
	return nil
}

func (s *inMemoryTaskExecStore) Get(_ context.Context, taskID, deviceID string) (*model.TaskDeviceExecution, error) {
	k := execKey(taskID, deviceID)
	v, ok := s.data.Get(k)
	if !ok {
		return nil, model.ErrNotFound
	}
	cp := *v
	return &cp, nil
}

func (s *inMemoryTaskExecStore) ListByTask(_ context.Context, taskID string) ([]*model.TaskDeviceExecution, error) {
	s.mu.RLock()
	set, ok := s.byTask[taskID]
	s.mu.RUnlock()
	if !ok {
		return []*model.TaskDeviceExecution{}, nil
	}
	out := make([]*model.TaskDeviceExecution, 0, len(set))
	for did := range set {
		v, ok := s.data.Get(execKey(taskID, did))
		if !ok || v == nil {
			continue
		}
		cp := *v
		out = append(out, &cp)
	}
	return out, nil
}

func (s *inMemoryTaskExecStore) ListByDevice(_ context.Context, deviceID string) ([]*model.TaskDeviceExecution, error) {
	s.mu.RLock()
	set, ok := s.byDevice[deviceID]
	s.mu.RUnlock()
	if !ok {
		return []*model.TaskDeviceExecution{}, nil
	}
	out := make([]*model.TaskDeviceExecution, 0, len(set))
	for tid := range set {
		v, ok := s.data.Get(execKey(tid, deviceID))
		if !ok || v == nil {
			continue
		}
		cp := *v
		out = append(out, &cp)
	}
	return out, nil
}

func (s *inMemoryTaskExecStore) UpdateProgress(_ context.Context, taskID, deviceID string, status model.UpgradeStatus, progress int, ts time.Time, errMsg string, retryInc bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := execKey(taskID, deviceID)
	v, ok := s.data.Get(k)
	if !ok {
		v = &model.TaskDeviceExecution{
			TaskID:     taskID,
			DeviceID:   deviceID,
			AssignedAt: ts,
		}
		if _, ok2 := s.byTask[taskID]; !ok2 {
			s.byTask[taskID] = make(map[string]struct{})
		}
		s.byTask[taskID][deviceID] = struct{}{}
		if _, ok2 := s.byDevice[deviceID]; !ok2 {
			s.byDevice[deviceID] = make(map[string]struct{})
		}
		s.byDevice[deviceID][taskID] = struct{}{}
	}
	if status != "" {
		v.Status = status
	}
	if progress < 0 {
		progress = 0
	}
	if progress > 100 {
		progress = 100
	}
	v.Progress = progress
	if !ts.IsZero() {
		v.LastReportAt = ts
	}
	if errMsg != "" {
	}
	if retryInc {
		v.RetryCount++
	}
	cp := *v
	s.data.Set(k, &cp)
	return nil
}

func (s *inMemoryTaskExecStore) CountByTask(_ context.Context, taskID string) (total, pending, running, success, failed, canceled, timeout int64, err error) {
	s.mu.RLock()
	set, ok := s.byTask[taskID]
	s.mu.RUnlock()
	if !ok {
		return 0, 0, 0, 0, 0, 0, 0, nil
	}
	for did := range set {
		v, ok := s.data.Get(execKey(taskID, did))
		if !ok || v == nil {
			continue
		}
		total++
		switch v.Status {
		case model.UpgradeStatusPending, "":
			pending++
		case model.UpgradeStatusDownloading, model.UpgradeStatusVerifying, model.UpgradeStatusUpgrading:
			running++
		case model.UpgradeStatusSuccess:
			success++
		case model.UpgradeStatusFailed:
			failed++
		case model.UpgradeStatusCanceled:
			canceled++
		}
	}
	return
}

func (s *inMemoryTaskExecStore) DeleteByTask(_ context.Context, taskID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	set, ok := s.byTask[taskID]
	if !ok {
		return nil
	}
	for did := range set {
		s.data.Delete(execKey(taskID, did))
		if dev, ok2 := s.byDevice[did]; ok2 {
			delete(dev, taskID)
			if len(dev) == 0 {
				delete(s.byDevice, did)
			}
		}
	}
	delete(s.byTask, taskID)
	return nil
}

func (s *inMemoryTaskExecStore) FindAssignedRunning(_ context.Context, deviceID string) (*model.TaskDeviceExecution, bool, error) {
	s.mu.RLock()
	set, ok := s.byDevice[deviceID]
	s.mu.RUnlock()
	if !ok {
		return nil, false, nil
	}
	var last *model.TaskDeviceExecution
	var lastTs time.Time
	s.data.ForEach(func(k string, v *model.TaskDeviceExecution) {
		if v == nil {
			return
		}
		if v.DeviceID != deviceID {
			_ = set
			return
		}
		switch v.Status {
		case "", model.UpgradeStatusPending, model.UpgradeStatusDownloading,
			model.UpgradeStatusVerifying, model.UpgradeStatusUpgrading:
			if last == nil || v.AssignedAt.After(lastTs) {
				last = v
				lastTs = v.AssignedAt
			}
		}
	})
	if last == nil {
		return nil, false, nil
	}
	cp := *last
	return &cp, true, nil
}

func (s *inMemoryTaskExecStore) SnapshotExecutions() map[string]*model.TaskDeviceExecution {
	out := make(map[string]*model.TaskDeviceExecution)
	raw := s.data.RawSnapshot()
	for k, v := range raw {
		if v == nil {
			continue
		}
		cp := *v
		out[k] = &cp
	}
	return out
}

func (s *inMemoryTaskExecStore) DiagnosticCountsByStatus() map[string]int64 {
	counts := make(map[string]int64)
	s.data.ForEach(func(k string, v *model.TaskDeviceExecution) {
		if v == nil {
			return
		}
		key := string(v.Status)
		if key == "" {
			key = "_empty_"
		}
		counts[key]++
	})
	return counts
}
