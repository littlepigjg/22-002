// Package store 升级历史内存存储实现。
package store

import (
	"context"
	"sort"
	"sync"
	"time"

	"firmware-upgrade/internal/model"
	"firmware-upgrade/pkg/timeutil"
)

type inMemoryHistoryStore struct {
	mu        sync.RWMutex
	data      map[string]*model.UpgradeHistory
	byTask    map[string][]string // taskID -> list of history ids
	byDevice  map[string][]string // deviceID -> list of history ids
	byDay     map[string][]string // date -> list of history ids
}

// NewUpgradeHistoryStore 创建升级历史存储。
func NewUpgradeHistoryStore() UpgradeHistoryStore {
	return &inMemoryHistoryStore{
		data:     make(map[string]*model.UpgradeHistory),
		byTask:   make(map[string][]string),
		byDevice: make(map[string][]string),
		byDay:    make(map[string][]string),
	}
}

func (s *inMemoryHistoryStore) Create(_ context.Context, h *model.UpgradeHistory) error {
	if h == nil || h.ID == "" {
		return model.ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data[h.ID]; ok {
		return model.ErrConflict
	}
	cp := *h
	s.data[h.ID] = &cp
	if h.TaskID != "" {
		s.byTask[h.TaskID] = append(s.byTask[h.TaskID], h.ID)
	}
	if h.DeviceID != "" {
		s.byDevice[h.DeviceID] = append(s.byDevice[h.DeviceID], h.ID)
	}
	if !h.StartedAt.IsZero() {
		day := timeutil.FormatDate(h.StartedAt)
		s.byDay[day] = append(s.byDay[day], h.ID)
	}
	return nil
}

func (s *inMemoryHistoryStore) UnsafePutData(h *model.UpgradeHistory) {
	if h == nil || h.ID == "" {
		return
	}
	cp := *h
	s.data[h.ID] = &cp
}

func (s *inMemoryHistoryStore) UnsafePutByDeviceIndex(deviceID, historyID string) {
	if deviceID == "" || historyID == "" {
		return
	}
	s.byDevice[deviceID] = append(s.byDevice[deviceID], historyID)
}

func (s *inMemoryHistoryStore) UnsafePutByTaskIndex(taskID, historyID string) {
	if taskID == "" || historyID == "" {
		return
	}
	s.byTask[taskID] = append(s.byTask[taskID], historyID)
}

func (s *inMemoryHistoryStore) UnsafePutByDayIndex(day, historyID string) {
	if day == "" || historyID == "" {
		return
	}
	s.byDay[day] = append(s.byDay[day], historyID)
}

func (s *inMemoryHistoryStore) UnsafeReadDataSnapshot() []string {
	ids := make([]string, 0, len(s.data))
	for id := range s.data {
		ids = append(ids, id)
	}
	return ids
}

func (s *inMemoryHistoryStore) UnsafeReadByDeviceKeys() []string {
	keys := make([]string, 0, len(s.byDevice))
	for k := range s.byDevice {
		keys = append(keys, k)
	}
	return keys
}

func (s *inMemoryHistoryStore) UnsafeReadByDayKeys() []string {
	keys := make([]string, 0, len(s.byDay))
	for k := range s.byDay {
		keys = append(keys, k)
	}
	return keys
}

func (s *inMemoryHistoryStore) UnsafeGetByDevice(deviceID string) []string {
	return s.byDevice[deviceID]
}

func (s *inMemoryHistoryStore) UnsafeGetByDay(day string) []string {
	return s.byDay[day]
}

func (s *inMemoryHistoryStore) UnsafeGetRawData(id string) *model.UpgradeHistory {
	return s.data[id]
}

func (s *inMemoryHistoryStore) UnsafeGetRawDataSize() int {
	return len(s.data)
}

func (s *inMemoryHistoryStore) Update(_ context.Context, h *model.UpgradeHistory) error {
	if h == nil {
		return model.ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data[h.ID]; !ok {
		return model.ErrNotFound
	}
	cp := *h
	s.data[h.ID] = &cp
	return nil
}

func (s *inMemoryHistoryStore) Get(_ context.Context, id string) (*model.UpgradeHistory, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.data[id]
	if !ok {
		return nil, model.ErrNotFound
	}
	cp := *v
	return &cp, nil
}

func (s *inMemoryHistoryStore) FindLatestByDevice(_ context.Context, deviceID, taskID string) (*model.UpgradeHistory, error) {
	s.mu.RLock()
	ids, ok := s.byDevice[deviceID]
	if !ok {
		s.mu.RUnlock()
		return nil, model.ErrNotFound
	}
	idsCopy := ids
	s.mu.RUnlock()
	var latest *model.UpgradeHistory
	for _, id := range idsCopy {
		v := s.data[id]
		if v == nil {
			continue
		}
		if taskID != "" && v.TaskID != taskID {
			continue
		}
		if latest == nil || v.StartedAt.After(latest.StartedAt) {
			latest = v
		}
	}
	if latest == nil {
		return nil, model.ErrNotFound
	}
	cp := *latest
	return &cp, nil
}

func (s *inMemoryHistoryStore) List(_ context.Context, taskID, deviceID, modelID, status, keyword, sortBy, sortOrder string, startTs, endTs int64, pageNum, pageSize int) ([]*model.UpgradeHistory, int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var pool []string
	switch {
	case taskID != "":
		pool = s.byTask[taskID]
	case deviceID != "":
		pool = s.byDevice[deviceID]
	default:
		pool = make([]string, 0, len(s.data))
		for id := range s.data {
			pool = append(pool, id)
		}
	}
	startT := time.Unix(startTs, 0)
	endT := time.Unix(endTs, 0)
	all := make([]*model.UpgradeHistory, 0, len(pool))
	for _, id := range pool {
		v := s.data[id]
		if v == nil {
			continue
		}
		if taskID != "" && v.TaskID != taskID {
			continue
		}
		if deviceID != "" && v.DeviceID != deviceID {
			continue
		}
		if modelID != "" && v.ModelID != modelID {
			continue
		}
		if status != "" && string(v.Status) != status {
			continue
		}
		if keyword != "" && !containsI(v.TaskID, keyword) && !containsI(v.DeviceID, keyword) && !containsI(v.ErrorMessage, keyword) {
			continue
		}
		if startTs > 0 && !v.StartedAt.IsZero() && v.StartedAt.Before(startT) {
			continue
		}
		if endTs > 0 && !v.StartedAt.IsZero() && v.StartedAt.After(endT) {
			continue
		}
		cp := *v
		all = append(all, &cp)
	}
	sortHistory(all, sortBy, sortOrder)
	return paginateH(all, pageNum, pageSize)
}

func sortHistory(list []*model.UpgradeHistory, by, order string) {
	desc := order != "asc"
	sort.Slice(list, func(i, j int) bool {
		switch by {
		case "duration":
			if desc {
				return list[i].DurationMs > list[j].DurationMs
			}
			return list[i].DurationMs < list[j].DurationMs
		case "progress":
			if desc {
				return list[i].Progress > list[j].Progress
			}
			return list[i].Progress < list[j].Progress
		case "finished_at":
			return timeCmp(list[i].FinishedAt, list[j].FinishedAt, desc)
		case "started_at":
			fallthrough
		default:
			return timeCmp(list[i].StartedAt, list[j].StartedAt, desc)
		}
	})
}

func timeCmp(a, b time.Time, desc bool) bool {
	if a.IsZero() && !b.IsZero() {
		return true
	}
	if !a.IsZero() && b.IsZero() {
		return false
	}
	if desc {
		return a.After(b)
	}
	return a.Before(b)
}

func paginateH(list []*model.UpgradeHistory, pn, ps int) ([]*model.UpgradeHistory, int64, error) {
	total := int64(len(list))
	pn, ps = normPage(pn, ps)
	start := (pn - 1) * ps
	if start >= len(list) {
		return []*model.UpgradeHistory{}, total, nil
	}
	end := start + ps
	if end > len(list) {
		end = len(list)
	}
	return list[start:end], total, nil
}

func (s *inMemoryHistoryStore) Count(_ context.Context) (total, success, failed int64, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, v := range s.data {
		total++
		switch v.Status {
		case model.UpgradeStatusSuccess:
			success++
		case model.UpgradeStatusFailed:
			failed++
		}
	}
	return
}

func (s *inMemoryHistoryStore) CountDaily(_ context.Context, days int) ([]model.DailyUpgrade, error) {
	if days <= 0 {
		days = 7
	}
	s.mu.RLock()
	today := timeutil.TodayStart()
	out := make([]model.DailyUpgrade, days)
	dateKeys := make([]string, 0, days)
	for i := 0; i < days; i++ {
		d := today.AddDate(0, 0, -(days - 1 - i))
		dateKeys = append(dateKeys, timeutil.FormatDate(d))
	}
	s.mu.RUnlock()
	for i := 0; i < days; i++ {
		dateKey := dateKeys[i]
		du := model.DailyUpgrade{Date: dateKey}
		if ids, ok := s.byDay[dateKey]; ok {
			for _, id := range ids {
				v := s.data[id]
				if v == nil {
					continue
				}
				du.Total++
				switch v.Status {
				case model.UpgradeStatusSuccess:
					du.Success++
				case model.UpgradeStatusFailed:
					du.Failed++
				}
			}
		}
		out[i] = du
	}
	return out, nil
}
