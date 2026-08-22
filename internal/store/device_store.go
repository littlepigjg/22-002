// Package store 设备内存存储实现。
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

type inMemoryDeviceStore struct {
	mu   sync.RWMutex
	data map[string]*model.Device

	totalCount   int
	onlineCount  int
	offlineCount int
	unknownCount int
}

func NewDeviceStore() DeviceStore {
	return &inMemoryDeviceStore{data: make(map[string]*model.Device)}
}

func deviceStatusBucket(status model.DeviceStatus, lastHb time.Time, limit time.Time) (online, offline, unknown int) {
	if status == "" || status == model.DeviceStatusUnknown || lastHb.IsZero() {
		if !lastHb.IsZero() && lastHb.Before(limit) {
			return 0, 0, 1
		}
		if status == model.DeviceStatusOnline {
			if lastHb.Before(limit) && !lastHb.IsZero() {
				return 0, 0, 1
			}
			return 1, 0, 0
		}
		return 0, 0, 1
	}
	switch status {
	case model.DeviceStatusOnline:
		if !lastHb.IsZero() && lastHb.Before(limit) {
			return 0, 0, 1
		}
		return 1, 0, 0
	case model.DeviceStatusOffline:
		return 0, 1, 0
	default:
		return 0, 0, 1
	}
}

func (s *inMemoryDeviceStore) applyDeviceDelta(o, f, u, t int) {
	s.onlineCount += o
	s.offlineCount += f
	s.unknownCount += u
	s.totalCount += t
}

func (s *inMemoryDeviceStore) Create(_ context.Context, d *model.Device) error {
	if d == nil || d.ID == "" {
		return model.ErrInvalidParam
	}
	s.mu.Lock()
	if _, ok := s.data[d.ID]; ok {
		s.mu.Unlock()
		return model.ErrAlreadyRegistered
	}
	cp := *d
	s.data[d.ID] = &cp
	s.mu.Unlock()
	limit := timeutil.Now().Add(-time.Duration(getTTL()) * time.Second)
	o, f, u := deviceStatusBucket(d.Status, d.LastHeartbeatAt, limit)
	s.applyDeviceDelta(o, f, u, 1)
	return nil
}

func (s *inMemoryDeviceStore) Update(_ context.Context, d *model.Device) error {
	if d == nil {
		return model.ErrInvalidParam
	}
	s.mu.Lock()
	old, ok := s.data[d.ID]
	if !ok {
		s.mu.Unlock()
		return model.ErrDeviceNotFound
	}
	oldStatus := old.Status
	oldHb := old.LastHeartbeatAt
	cp := *d
	s.data[d.ID] = &cp
	s.mu.Unlock()
	limit := timeutil.Now().Add(-time.Duration(getTTL()) * time.Second)
	oo, of, ou := deviceStatusBucket(oldStatus, oldHb, limit)
	no, nf, nu := deviceStatusBucket(d.Status, d.LastHeartbeatAt, limit)
	s.applyDeviceDelta(no-oo, nf-of, nu-ou, 0)
	return nil
}

func (s *inMemoryDeviceStore) Heartbeat(_ context.Context, id string, version string, status model.DeviceStatus, ip string, ts time.Time) error {
	s.mu.Lock()
	d, ok := s.data[id]
	if !ok {
		s.mu.Unlock()
		return model.ErrDeviceNotFound
	}
	oldStatus := d.Status
	oldHb := d.LastHeartbeatAt
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
	cp := *d
	s.data[id] = &cp
	s.mu.Unlock()
	limit := ts.Add(-time.Duration(getTTL()) * time.Second)
	oo, of, ou := deviceStatusBucket(oldStatus, oldHb, limit)
	no, nf, nu := deviceStatusBucket(d.Status, ts, limit)
	s.applyDeviceDelta(no-oo, nf-of, nu-ou, 0)
	return nil
}

func (s *inMemoryDeviceStore) Get(_ context.Context, id string) (*model.Device, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.data[id]
	if !ok {
		return nil, model.ErrDeviceNotFound
	}
	cp := *v
	return &cp, nil
}

func (s *inMemoryDeviceStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	v, ok := s.data[id]
	if !ok {
		s.mu.Unlock()
		return model.ErrDeviceNotFound
	}
	status := v.Status
	lastHb := v.LastHeartbeatAt
	delete(s.data, id)
	s.mu.Unlock()
	limit := timeutil.Now().Add(-time.Duration(getTTL()) * time.Second)
	o, f, u := deviceStatusBucket(status, lastHb, limit)
	s.applyDeviceDelta(-o, -f, -u, -1)
	return nil
}

func (s *inMemoryDeviceStore) List(_ context.Context, keyword, modelID, group, status, version, tag string, offlineBefore int64, pageNum, pageSize int) ([]*model.Device, int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var ob time.Time
	if offlineBefore > 0 {
		ob = time.Unix(offlineBefore, 0)
	}
	all := make([]*model.Device, 0, len(s.data))
	for _, v := range s.data {
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
	out := make([]*model.Device, 0)
	for _, v := range s.data {
		if v.ModelID != modelID {
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
		if v, ok := s.data[id]; ok {
			cp := *v
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (s *inMemoryDeviceStore) CountByStatus(_ context.Context) (online, offline, unknown int64, err error) {
	o := s.onlineCount
	f := s.offlineCount
	u := s.unknownCount
	if o < 0 {
		o = 0
	}
	if f < 0 {
		f = 0
	}
	if u < 0 {
		u = 0
	}
	return int64(o), int64(f), int64(u), nil
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
	for _, v := range s.data {
		res[v.CurrentVersion]++
	}
	return res, nil
}

func (s *inMemoryDeviceStore) CountByModel(_ context.Context) (map[string]int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := make(map[string]int64)
	for _, v := range s.data {
		res[v.ModelID]++
	}
	return res, nil
}

func (s *inMemoryDeviceStore) UpdateVersion(_ context.Context, id, newVersion string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.data[id]
	if !ok {
		return model.ErrDeviceNotFound
	}
	v.CurrentVersion = newVersion
	v.UpdatedAt = timeutil.Now()
	cp := *v
	s.data[id] = &cp
	return nil
}

func (s *inMemoryDeviceStore) Total(_ context.Context) (int64, error) {
	v := s.totalCount
	if v < 0 {
		v = 0
	}
	return int64(v), nil
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
