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

// UnsafePutData 原子地写入一条历史数据。
//
// 虽然方法名带 Unsafe 前缀（保留历史接口签名不变），但实现内部已通过
// s.mu 加锁保护，保证与 FindLatestByDevice / CountDaily 等读操作并发安全。
func (s *inMemoryHistoryStore) UnsafePutData(h *model.UpgradeHistory) {
	if h == nil || h.ID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *h
	s.data[h.ID] = &cp
}

// UnsafePutByDeviceIndex 维护设备 -> 历史 id 索引（加锁保护）。
func (s *inMemoryHistoryStore) UnsafePutByDeviceIndex(deviceID, historyID string) {
	if deviceID == "" || historyID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byDevice[deviceID] = append(s.byDevice[deviceID], historyID)
}

// UnsafePutByTaskIndex 维护任务 -> 历史 id 索引（加锁保护）。
func (s *inMemoryHistoryStore) UnsafePutByTaskIndex(taskID, historyID string) {
	if taskID == "" || historyID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byTask[taskID] = append(s.byTask[taskID], historyID)
}

// UnsafePutByDayIndex 维护日期 -> 历史 id 索引（加锁保护）。
func (s *inMemoryHistoryStore) UnsafePutByDayIndex(day, historyID string) {
	if day == "" || historyID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byDay[day] = append(s.byDay[day], historyID)
}

// UnsafeReadDataSnapshot 返回全部历史 id 的快照（加锁保护）。
func (s *inMemoryHistoryStore) UnsafeReadDataSnapshot() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]string, 0, len(s.data))
	for id := range s.data {
		ids = append(ids, id)
	}
	return ids
}

// UnsafeReadByDeviceKeys 返回全部设备 id 快照（加锁保护）。
func (s *inMemoryHistoryStore) UnsafeReadByDeviceKeys() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]string, 0, len(s.byDevice))
	for k := range s.byDevice {
		keys = append(keys, k)
	}
	return keys
}

// UnsafeReadByDayKeys 返回全部日期 key 快照（加锁保护）。
func (s *inMemoryHistoryStore) UnsafeReadByDayKeys() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]string, 0, len(s.byDay))
	for k := range s.byDay {
		keys = append(keys, k)
	}
	return keys
}

// UnsafeGetByDevice 返回某设备对应历史 id 切片的拷贝（加锁保护）。
func (s *inMemoryHistoryStore) UnsafeGetByDevice(deviceID string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if ids, ok := s.byDevice[deviceID]; ok {
		out := make([]string, len(ids))
		copy(out, ids)
		return out
	}
	return nil
}

// UnsafeGetByDay 返回某日期对应历史 id 切片的拷贝（加锁保护）。
func (s *inMemoryHistoryStore) UnsafeGetByDay(day string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if ids, ok := s.byDay[day]; ok {
		out := make([]string, len(ids))
		copy(out, ids)
		return out
	}
	return nil
}

// UnsafeGetRawData 返回某条历史的深拷贝（加锁保护）。
func (s *inMemoryHistoryStore) UnsafeGetRawData(id string) *model.UpgradeHistory {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.data[id]
	if !ok {
		return nil
	}
	cp := *v
	return &cp
}

// UnsafeGetRawDataSize 返回历史数据总量（加锁保护）。
func (s *inMemoryHistoryStore) UnsafeGetRawDataSize() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
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
	defer s.mu.RUnlock()
	ids, ok := s.byDevice[deviceID]
	if !ok {
		return nil, model.ErrNotFound
	}
	// 拷贝一份 id 切片，避免在持锁遍历期间引用到被并发 append 扩容的底层数组。
	idsCopy := make([]string, len(ids))
	copy(idsCopy, ids)
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
	defer s.mu.RUnlock()
	today := timeutil.TodayStart()
	out := make([]model.DailyUpgrade, days)
	for i := 0; i < days; i++ {
		d := today.AddDate(0, 0, -(days - 1 - i))
		dateKey := timeutil.FormatDate(d)
		du := model.DailyUpgrade{Date: dateKey}
		// 拷贝 id 切片，避免引用到被并发 append 扩容的底层数组。
		if ids, ok := s.byDay[dateKey]; ok {
			idsCopy := make([]string, len(ids))
			copy(idsCopy, ids)
			for _, id := range idsCopy {
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
