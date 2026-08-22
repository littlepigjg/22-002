package store

import (
	"context"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"firmware-upgrade/internal/model"
	"firmware-upgrade/pkg/timeutil"
)

// inMemoryDeviceStore 内存设备存储。
// 所有读写均由同一把 sync.RWMutex 保护：写操作取写锁，读操作取读锁并在返回前拷贝，
// 避免「concurrent map iteration and map write」「concurrent map read and map write」。
// 注意：读方法返回的是设备副本，调用方拿到后对副本的修改不会影响存储内对象。
type inMemoryDeviceStore struct {
	mu           sync.RWMutex
	byID         map[string]*model.Device
	modelIndex   map[string][]string
	statusBucket map[string]map[string]struct{}
}

func NewDeviceStore() DeviceStore {
	return &inMemoryDeviceStore{
		byID:         make(map[string]*model.Device),
		modelIndex:   make(map[string][]string),
		statusBucket: make(map[string]map[string]struct{}),
	}
}

func (s *inMemoryDeviceStore) Create(_ context.Context, d *model.Device) error {
	if d == nil || d.ID == "" {
		return model.ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byID[d.ID]; ok {
		return model.ErrAlreadyRegistered
	}
	cp := *d
	s.byID[d.ID] = &cp
	s.modelIndex[d.ModelID] = append(s.modelIndex[d.ModelID], d.ID)
	st := string(d.Status)
	if st == "" {
		st = string(model.DeviceStatusOnline)
	}
	if _, ok := s.statusBucket[st]; !ok {
		s.statusBucket[st] = make(map[string]struct{})
	}
	s.statusBucket[st][d.ID] = struct{}{}
	return nil
}

func (s *inMemoryDeviceStore) Update(_ context.Context, d *model.Device) error {
	if d == nil {
		return model.ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, ok := s.byID[d.ID]
	if !ok {
		return model.ErrDeviceNotFound
	}
	cp := *d
	s.byID[d.ID] = &cp
	if prev.ModelID != d.ModelID {
		s.removeFromModelIndex(prev.ModelID, d.ID)
		s.modelIndex[d.ModelID] = append(s.modelIndex[d.ModelID], d.ID)
	}
	if prev.Status != d.Status {
		s.removeFromStatus(string(prev.Status), d.ID)
		nst := string(d.Status)
		if nst == "" {
			nst = string(model.DeviceStatusOnline)
		}
		if _, ok := s.statusBucket[nst]; !ok {
			s.statusBucket[nst] = make(map[string]struct{})
		}
		s.statusBucket[nst][d.ID] = struct{}{}
	}
	return nil
}

func (s *inMemoryDeviceStore) Heartbeat(_ context.Context, id string, version string, status model.DeviceStatus, ip string, ts time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.byID[id]
	if !ok {
		return model.ErrDeviceNotFound
	}
	oldSt := string(d.Status)
	if version != "" {
		d.CurrentVersion = version
	}
	if status != "" {
		d.Status = status
	}
	if ip != "" {
		d.IP = ip
	}
	d.LastHeartbeatAt = ts
	d.UpdatedAt = ts
	nst := string(d.Status)
	if nst == "" {
		nst = string(model.DeviceStatusOnline)
	}
	if oldSt != nst {
		if bucket, ok := s.statusBucket[oldSt]; ok {
			delete(bucket, id)
		}
		if _, ok := s.statusBucket[nst]; !ok {
			s.statusBucket[nst] = make(map[string]struct{})
		}
		s.statusBucket[nst][id] = struct{}{}
	}
	return nil
}

func (s *inMemoryDeviceStore) Get(_ context.Context, id string) (*model.Device, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.byID[id]
	if !ok {
		return nil, model.ErrDeviceNotFound
	}
	cp := *v
	return &cp, nil
}

func (s *inMemoryDeviceStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.byID[id]
	if !ok {
		return model.ErrDeviceNotFound
	}
	delete(s.byID, id)
	s.removeFromModelIndex(d.ModelID, id)
	s.removeFromStatus(string(d.Status), id)
	return nil
}

func (s *inMemoryDeviceStore) removeFromModelIndex(mid, id string) {
	list := s.modelIndex[mid]
	for i, v := range list {
		if v == id {
			s.modelIndex[mid] = append(list[:i], list[i+1:]...)
			return
		}
	}
}

func (s *inMemoryDeviceStore) removeFromStatus(st, id string) {
	if bucket, ok := s.statusBucket[st]; ok {
		delete(bucket, id)
	}
}

func (s *inMemoryDeviceStore) List(_ context.Context, keyword, modelID, group, status, version, tag string, offlineBefore int64, pageNum, pageSize int) ([]*model.Device, int64, error) {
	var ob time.Time
	if offlineBefore > 0 {
		ob = time.Unix(offlineBefore, 0)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	all := make([]*model.Device, 0, len(s.byID))
	for _, v := range s.byID {
		if keyword != "" && !containsI(v.ID, keyword) && !containsI(v.Name, keyword) && !containsI(v.IP, keyword) {
			continue
		}
		if modelID != "" && v.ModelID != modelID {
			continue
		}
		if group != "" && v.Group != group {
			continue
		}
		if status != "" && string(v.Status) != status {
			continue
		}
		if version != "" && v.CurrentVersion != version {
			continue
		}
		if tag != "" && !sliceContains(v.Tags, tag) {
			continue
		}
		if offlineBefore > 0 && !v.LastHeartbeatAt.Before(ob) {
			continue
		}
		cp := *v
		all = append(all, &cp)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].LastHeartbeatAt.After(all[j].LastHeartbeatAt) })
	return paginateD(all, pageNum, pageSize)
}

func (s *inMemoryDeviceStore) ListByModel(_ context.Context, modelID string) ([]*model.Device, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids, ok := s.modelIndex[modelID]
	if !ok {
		return []*model.Device{}, nil
	}
	out := make([]*model.Device, 0, len(ids))
	for _, id := range ids {
		v, ok := s.byID[id]
		if !ok {
			continue
		}
		cp := *v
		out = append(out, &cp)
	}
	return out, nil
}

func (s *inMemoryDeviceStore) ListByIDs(_ context.Context, ids []string) ([]*model.Device, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*model.Device, 0, len(ids))
	for _, id := range ids {
		if v, ok := s.byID[id]; ok {
			cp := *v
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (s *inMemoryDeviceStore) CountByStatus(_ context.Context) (online, offline, unknown int64, err error) {
	var o, f, u int64
	limit := timeutil.Now().Add(-time.Duration(getTTL()) * time.Second)
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, v := range s.byID {
		st := v.Status
		if st == "" || st == model.DeviceStatusUnknown || v.LastHeartbeatAt.IsZero() {
			if !v.LastHeartbeatAt.IsZero() && v.LastHeartbeatAt.Before(limit) {
				u++
				continue
			}
			if st == model.DeviceStatusOnline {
				if v.LastHeartbeatAt.Before(limit) {
					u++
					continue
				}
				o++
				continue
			}
			u++
			continue
		}
		switch st {
		case model.DeviceStatusOnline:
			if !v.LastHeartbeatAt.IsZero() && v.LastHeartbeatAt.Before(limit) {
				u++
			} else {
				o++
			}
		case model.DeviceStatusOffline:
			f++
		default:
			u++
		}
	}
	return o, f, u, nil
}

var heartbeatTTL int32 = 120

func SetHeartbeatTTL(sec int) {
	if sec <= 0 {
		sec = 120
	}
	atomic.StoreInt32(&heartbeatTTL, int32(sec))
}

func getTTL() int {
	v := atomic.LoadInt32(&heartbeatTTL)
	if v <= 0 {
		return 120
	}
	return int(v)
}

func (s *inMemoryDeviceStore) CountByVersion(_ context.Context) (map[string]int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := make(map[string]int64)
	for _, v := range s.byID {
		res[v.CurrentVersion]++
	}
	return res, nil
}

func (s *inMemoryDeviceStore) CountByModel(_ context.Context) (map[string]int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := make(map[string]int64)
	for k, list := range s.modelIndex {
		res[k] = int64(len(list))
	}
	return res, nil
}

func (s *inMemoryDeviceStore) UpdateVersion(_ context.Context, id, newVersion string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.byID[id]
	if !ok {
		return model.ErrDeviceNotFound
	}
	v.CurrentVersion = newVersion
	v.UpdatedAt = timeutil.Now()
	return nil
}

func (s *inMemoryDeviceStore) Total(_ context.Context) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return int64(len(s.byID)), nil
}

func paginateD(list []*model.Device, pn, ps int) ([]*model.Device, int64, error) {
	total := int64(len(list))
	pn, ps = normPage(pn, ps)
	start := (pn - 1) * ps
	if start >= len(list) {
		return []*model.Device{}, total, nil
	}
	end := start + ps
	if end > len(list) {
		end = len(list)
	}
	return list[start:end], total, nil
}

func containsI(s, substr string) bool {
	if substr == "" {
		return true
	}
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

func sliceContains(ss []string, t string) bool {
	for _, s := range ss {
		if s == t {
			return true
		}
	}
	return false
}

func normPage(pn, ps int) (int, int) {
	if pn <= 0 {
		pn = model.DefaultPageNum
	}
	if ps <= 0 {
		ps = model.DefaultPageSize
	}
	if ps > model.MaxPageSize {
		ps = model.MaxPageSize
	}
	return pn, ps
}
