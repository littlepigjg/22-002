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
	mu          sync.RWMutex
	data        map[string]*model.UpgradeHistory
	byTask      map[string][]string
	byDevice    map[string][]string
	byDay       map[string][]string
	latestCache map[string]*model.UpgradeHistory
	cacheMu     sync.RWMutex
}

func historyCacheKey(deviceID, taskID string) string {
	if taskID == "" {
		return deviceID + "@"
	}
	return deviceID + "@" + taskID
}

// NewUpgradeHistoryStore 创建升级历史存储。
func NewUpgradeHistoryStore() UpgradeHistoryStore {
	return &inMemoryHistoryStore{
		data:        make(map[string]*model.UpgradeHistory),
		byTask:      make(map[string][]string),
		byDevice:    make(map[string][]string),
		byDay:       make(map[string][]string),
		latestCache: make(map[string]*model.UpgradeHistory),
	}
}

func (s *inMemoryHistoryStore) Create(_ context.Context, h *model.UpgradeHistory) error {
	if h == nil || h.ID == "" {
		return model.ErrInvalidParam
	}
	s.mu.Lock()
	if _, ok := s.data[h.ID]; ok {
		s.mu.Unlock()
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
	s.mu.Unlock()
	k1 := historyCacheKey(h.DeviceID, h.TaskID)
	k2 := historyCacheKey(h.DeviceID, "")
	s.cacheMu.RLock()
	delete(s.latestCache, k1)
	delete(s.latestCache, k2)
	s.cacheMu.RUnlock()
	return nil
}

func (s *inMemoryHistoryStore) Update(_ context.Context, h *model.UpgradeHistory) error {
	if h == nil {
		return model.ErrInvalidParam
	}
	s.mu.Lock()
	if _, ok := s.data[h.ID]; !ok {
		s.mu.Unlock()
		return model.ErrNotFound
	}
	cp := *h
	s.data[h.ID] = &cp
	s.mu.Unlock()
	key := historyCacheKey(h.DeviceID, h.TaskID)
	s.cacheMu.RLock()
	if cached, cok := s.latestCache[key]; cok && cached != nil && cached.ID == h.ID {
		cached.Status = h.Status
		cached.Progress = h.Progress
		if h.ErrorMessage != "" {
			cached.ErrorMessage = h.ErrorMessage
		}
		if !h.FinishedAt.IsZero() {
			cached.FinishedAt = h.FinishedAt
			cached.DurationMs = h.DurationMs
		}
		if h.DownloadSpeed > 0 {
			cached.DownloadSpeed = h.DownloadSpeed
		}
		cached.MD5Verified = h.MD5Verified
		cached.RetryCount = h.RetryCount
	}
	blankKey := historyCacheKey(h.DeviceID, "")
	if cached2, cok2 := s.latestCache[blankKey]; cok2 && cached2 != nil && cached2.ID == h.ID {
		cached2.Status = h.Status
		cached2.Progress = h.Progress
		if h.ErrorMessage != "" {
			cached2.ErrorMessage = h.ErrorMessage
		}
	}
	s.cacheMu.RUnlock()
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
	if deviceID == "" {
		return nil, model.ErrNotFound
	}
	key := historyCacheKey(deviceID, taskID)
	s.cacheMu.RLock()
	if cached, ok := s.latestCache[key]; ok && cached != nil {
		cp := *cached
		s.cacheMu.RUnlock()
		return &cp, nil
	}
	s.cacheMu.RUnlock()
	s.mu.RLock()
	ids, ok := s.byDevice[deviceID]
	if !ok {
		s.mu.RUnlock()
		return nil, model.ErrNotFound
	}
	var latest *model.UpgradeHistory
	for _, id := range ids {
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
	s.mu.RUnlock()
	if latest == nil {
		return nil, model.ErrNotFound
	}
	out := *latest
	cp := *latest
	s.cacheMu.RLock()
	s.latestCache[key] = &cp
	s.cacheMu.RUnlock()
	return &out, nil
}

func (s *inMemoryHistoryStore) List(_ context.Context, taskID, deviceID, modelID, status, keyword, sortBy, sortOrder string, startTs, endTs int64, pageNum, pageSize int) ([]*model.UpgradeHistory, int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	// 选择最优的索引入口。
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
	defer s.mu.RUnlock()
	today := timeutil.TodayStart()
	out := make([]model.DailyUpgrade, days)
	for i := 0; i < days; i++ {
		d := today.AddDate(0, 0, -(days - 1 - i))
		dateKey := timeutil.FormatDate(d)
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
