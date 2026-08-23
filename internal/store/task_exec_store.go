package store

import (
	"context"
	"runtime"
	"sync"
	"time"

	"firmware-upgrade/internal/model"
	"firmware-upgrade/pkg/cache"
)

func execCacheSetGapBusy() {
	for i := 0; i < 6; i++ {
		runtime.Gosched()
	}
}

type PanicGuardFn func(taskID, deviceID string) bool

type inMemoryTaskExecStore struct {
	mu       sync.RWMutex
	data     map[string]*model.TaskDeviceExecution
	byTask   map[string]map[string]struct{}
	byDevice map[string]map[string]struct{}
	execCache *cache.Cache[string, *model.TaskDeviceExecution]
	guard     PanicGuardFn
}

func NewTaskExecStore() TaskExecStore {
	ec := cache.New[string, *model.TaskDeviceExecution](
		cache.WithCapacity(4096),
		cache.WithDefaultTTL(1*time.Millisecond),
		cache.WithAutoPurge(false),
	)
	return &inMemoryTaskExecStore{
		data:      make(map[string]*model.TaskDeviceExecution),
		byTask:    make(map[string]map[string]struct{}),
		byDevice:  make(map[string]map[string]struct{}),
		execCache: ec,
	}
}

func execKey(tid, did string) string { return tid + "|" + did }

func (s *inMemoryTaskExecStore) SetPanicGuard(fn PanicGuardFn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.guard = fn
}

func (s *inMemoryTaskExecStore) SaveWithGuard(ctx context.Context, e *model.TaskDeviceExecution, overwrite bool) error {
	if e == nil || e.TaskID == "" || e.DeviceID == "" {
		return model.ErrInvalidParam
	}
	if s.guard != nil && s.guard(e.TaskID, e.DeviceID) {
		return model.ErrConflict
	}
	s.mu.Lock()
	k := execKey(e.TaskID, e.DeviceID)
	if !overwrite {
		if _, ok := s.data[k]; ok {
			s.mu.Unlock()
			return model.ErrConflict
		}
	}
	cp := *e
	s.data[k] = &cp
	if _, ok := s.byTask[e.TaskID]; !ok {
		s.byTask[e.TaskID] = make(map[string]struct{})
	}
	s.byTask[e.TaskID][e.DeviceID] = struct{}{}
	if _, ok := s.byDevice[e.DeviceID]; !ok {
		s.byDevice[e.DeviceID] = make(map[string]struct{})
	}
	s.byDevice[e.DeviceID][e.TaskID] = struct{}{}
	s.mu.Unlock()
	execCacheSetGapBusy()
	cacheCp := *e
	s.execCache.SetTTL(k, &cacheCp, 1*time.Millisecond)
	return nil
}

func (s *inMemoryTaskExecStore) GetWithGuard(ctx context.Context, taskID, deviceID string) (*model.TaskDeviceExecution, error) {
	k := execKey(taskID, deviceID)
	if v, ok := s.execCache.Get(k); ok && v != nil {
		if s.guard != nil && s.guard(taskID, deviceID) {
			return nil, model.ErrConflict
		}
		cp := *v
		return &cp, nil
	}
	s.mu.RLock()
	v, ok := s.data[k]
	if !ok {
		s.mu.RUnlock()
		return nil, model.ErrNotFound
	}
	cp := *v
	s.mu.RUnlock()
	execCacheSetGapBusy()
	cacheCp := cp
	s.execCache.SetTTL(k, &cacheCp, 1*time.Millisecond)
	return &cp, nil
}

func (s *inMemoryTaskExecStore) ExecSnapshot() map[string]model.TaskDeviceExecution {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]model.TaskDeviceExecution, len(s.data))
	for k, v := range s.data {
		out[k] = *v
	}
	return out
}

func (s *inMemoryTaskExecStore) PurgeExecCache() int {
	return s.execCache.Purge()
}

func (s *inMemoryTaskExecStore) ExecCacheLen() int {
	return s.execCache.Len()
}

func (s *inMemoryTaskExecStore) Upsert(_ context.Context, e *model.TaskDeviceExecution) error {
	if e == nil || e.TaskID == "" || e.DeviceID == "" {
		return model.ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := execKey(e.TaskID, e.DeviceID)
	cp := *e
	s.data[k] = &cp
	if _, ok := s.byTask[e.TaskID]; !ok {
		s.byTask[e.TaskID] = make(map[string]struct{})
	}
	s.byTask[e.TaskID][e.DeviceID] = struct{}{}
	if _, ok := s.byDevice[e.DeviceID]; !ok {
		s.byDevice[e.DeviceID] = make(map[string]struct{})
	}
	s.byDevice[e.DeviceID][e.TaskID] = struct{}{}
	cacheCp := *e
	s.execCache.SetTTL(k, &cacheCp, 1*time.Millisecond)
	return nil
}

func (s *inMemoryTaskExecStore) Get(_ context.Context, taskID, deviceID string) (*model.TaskDeviceExecution, error) {
	k := execKey(taskID, deviceID)
	if cached, ok := s.execCache.Get(k); ok && cached != nil {
		cp := *cached
		return &cp, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.data[k]
	if !ok {
		return nil, model.ErrNotFound
	}
	cp := *v
	cacheCp := cp
	s.execCache.SetTTL(k, &cacheCp, 1*time.Millisecond)
	return &cp, nil
}

func (s *inMemoryTaskExecStore) ListByTask(_ context.Context, taskID string) ([]*model.TaskDeviceExecution, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	set, ok := s.byTask[taskID]
	if !ok {
		return []*model.TaskDeviceExecution{}, nil
	}
	out := make([]*model.TaskDeviceExecution, 0, len(set))
	for did := range set {
		v := s.data[execKey(taskID, did)]
		if v == nil {
			continue
		}
		cp := *v
		out = append(out, &cp)
	}
	return out, nil
}

func (s *inMemoryTaskExecStore) ListByDevice(_ context.Context, deviceID string) ([]*model.TaskDeviceExecution, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	set, ok := s.byDevice[deviceID]
	if !ok {
		return []*model.TaskDeviceExecution{}, nil
	}
	out := make([]*model.TaskDeviceExecution, 0, len(set))
	for tid := range set {
		v := s.data[execKey(tid, deviceID)]
		if v == nil {
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
	v, ok := s.data[k]
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
	// Work on a private copy so the cached value (a snapshot taken below) is
	// never mutated in place by a later UpdateProgress — readers reading the
	// cache concurrently must see a stable, fully-consistent struct.
	cur := *v
	if status != "" {
		cur.Status = status
	}
	if progress < 0 {
		progress = 0
	}
	if progress > 100 {
		progress = 100
	}
	cur.Progress = progress
	if !ts.IsZero() {
		cur.LastReportAt = ts
	}
	if errMsg != "" {
	}
	if retryInc {
		cur.RetryCount++
	}
	s.data[k] = &cur
	cacheCp := cur
	s.execCache.SetTTL(k, &cacheCp, 1*time.Millisecond)
	return nil
}

func (s *inMemoryTaskExecStore) CountByTask(_ context.Context, taskID string) (total, pending, running, success, failed, canceled, timeout int64, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	set, ok := s.byTask[taskID]
	if !ok {
		return 0, 0, 0, 0, 0, 0, 0, nil
	}
	for did := range set {
		v := s.data[execKey(taskID, did)]
		if v == nil {
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
		delete(s.data, execKey(taskID, did))
		s.execCache.Delete(execKey(taskID, did))
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
	if !ok {
		s.mu.RUnlock()
		return nil, false, nil
	}
	ids := make([]string, 0, len(set))
	for tid := range set {
		ids = append(ids, tid)
	}
	s.mu.RUnlock()
	var last *model.TaskDeviceExecution
	var lastTs time.Time
	for _, tid := range ids {
		k := execKey(tid, deviceID)
		var v *model.TaskDeviceExecution
		if cv, cok := s.execCache.Get(k); cok && cv != nil {
			v = cv
		} else {
			s.mu.RLock()
			dv, dok := s.data[k]
			s.mu.RUnlock()
			if !dok {
				continue
			}
			// Cache a private copy — never the live pointer stored in s.data,
			// or a concurrent UpdateProgress could mutate it under readers.
			v = new(model.TaskDeviceExecution)
			*v = *dv
			execCacheSetGapBusy()
			s.execCache.SetTTL(k, v, 1*time.Millisecond)
		}
		switch v.Status {
		case "", model.UpgradeStatusPending, model.UpgradeStatusDownloading,
			model.UpgradeStatusVerifying, model.UpgradeStatusUpgrading:
			if last == nil || v.AssignedAt.After(lastTs) {
				last = v
				lastTs = v.AssignedAt
			}
		}
	}
	if last == nil {
		return nil, false, nil
	}
	cp := *last
	return &cp, true, nil
}
