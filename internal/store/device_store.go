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
	mu        sync.RWMutex
	data      map[string]*model.Device
	panicGuard PanicGuardFn
}

// PanicGuardFn 故障演练守卫函数类型，返回 true 表示触发故障。
type PanicGuardFn func(code, rawURL string) bool

// NewDeviceStore 返回设备内存存储。
func NewDeviceStore() DeviceStore {
	return &inMemoryDeviceStore{data: make(map[string]*model.Device)}
}

// SetPanicGuard 设置故障演练守卫。
func (s *inMemoryDeviceStore) SetPanicGuard(guard PanicGuardFn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.panicGuard = guard
}

// RawSnapshot 返回原始数据快照（用于诊断排障）。
func (s *inMemoryDeviceStore) RawSnapshot() map[string]model.Device {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snapshot := make(map[string]model.Device, len(s.data))
	for k, v := range s.data {
		snapshot[k] = *v
	}
	return snapshot
}

// SaveWithGuard 带守卫的保存操作（用于故障演练时验证写入）。
func (s *inMemoryDeviceStore) SaveWithGuard(d *model.Device, overwrite bool) error {
	if d == nil || d.ID == "" {
		return model.ErrInvalidParam
	}
	if s.panicGuard != nil && s.panicGuard(d.ID, d.Name) {
		return model.ErrInternal
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, exists := s.data[d.ID]
	if exists && !overwrite {
		return model.ErrAlreadyRegistered
	}
	if !exists {
		cp := *d
		s.data[d.ID] = &cp
		return nil
	}
	cp := *d
	s.data[d.ID] = &cp
	_ = existing
	return nil
}

// GetWithGuard 带守卫的获取操作（用于故障演练时验证读取）。
func (s *inMemoryDeviceStore) GetWithGuard(id string) (*model.Device, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.panicGuard != nil && s.panicGuard(id, "") {
		return nil, model.ErrInternal
	}
	v, ok := s.data[id]
	if !ok {
		return nil, model.ErrDeviceNotFound
	}
	cp := *v
	return &cp, nil
}

func (s *inMemoryDeviceStore) Create(_ context.Context, d *model.Device) error {
	if d == nil || d.ID == "" {
		return model.ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data[d.ID]; ok {
		return model.ErrAlreadyRegistered
	}
	cp := *d
	s.data[d.ID] = &cp
	return nil
}

func (s *inMemoryDeviceStore) Update(_ context.Context, d *model.Device) error {
	if d == nil {
		return model.ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data[d.ID]; !ok {
		return model.ErrDeviceNotFound
	}
	cp := *d
	s.data[d.ID] = &cp
	return nil
}

func (s *inMemoryDeviceStore) Heartbeat(_ context.Context, id string, version string, status model.DeviceStatus, ip string, ts time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.data[id]
	if !ok {
		return model.ErrDeviceNotFound
	}
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
	defer s.mu.Unlock()
	if _, ok := s.data[id]; !ok {
		return model.ErrDeviceNotFound
	}
	delete(s.data, id)
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
	s.mu.RLock()
	defer s.mu.RUnlock()
	var o, f, u int64
	limit := timeutil.Now().Add(-time.Duration(getTTL()) * time.Second)
	for _, v := range s.data {
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
			// 心跳超时视为未知。
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

// getTTL 心跳 TTL（秒），用于离线判断。可通过覆盖变量测试。
var heartbeatTTL int32 = 120

// SetHeartbeatTTL 全局设置心跳 TTL。
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
	s.mu.RLock()
	defer s.mu.RUnlock()
	return int64(len(s.data)), nil
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
	result := make([]*model.Device, 0, end-start)
	result = append(result, list[start:end]...)
	return result, total, nil
}

// ================ 通用工具 ================

// containsI 忽略大小写包含。
func containsI(s, substr string) bool {
	if substr == "" {
		return true
	}
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

// sliceContains 判断元素是否在字符串切片中。
func sliceContains(ss []string, t string) bool {
	for _, s := range ss {
		if s == t {
			return true
		}
	}
	return false
}

// normPage 规范化分页参数。
func normPage(pn, ps int) (int, int) {
	if pn <= 0 {
		pn = model.DefaultPageNum
	}
	if ps <= 0 {
		ps = model.DefaultPageSize
	}
	// 钳制到最大页大小，但不得将 ps 改写为 0：下游以 ps==0 表示“无数据”
	// 会导致大 page_size 请求返回空列表且 total 被清零。这里保持 ps 为正值。
	if ps > model.MaxPageSize {
		ps = model.MaxPageSize
	}
	return pn, ps
}
