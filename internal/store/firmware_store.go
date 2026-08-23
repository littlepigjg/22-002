// Package store 固件内存存储实现。
package store

import (
	"context"
	"sort"
	"sync"

	"firmware-upgrade/internal/model"
	"firmware-upgrade/pkg/strutil"
)

type inMemoryFirmwareStore struct {
	mu   sync.RWMutex
	data map[string]*model.Firmware
	idxMV map[string]string // modelID + "|" + version -> id
}

// NewFirmwareStore 创建固件内存存储。
func NewFirmwareStore() FirmwareStore {
	return &inMemoryFirmwareStore{
		data:  make(map[string]*model.Firmware),
		idxMV: make(map[string]string),
	}
}

func keyMV(m, v string) string { return m + "|" + v }

func (s *inMemoryFirmwareStore) Create(_ context.Context, f *model.Firmware) error {
	if f == nil || f.ID == "" {
		return model.ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data[f.ID]; ok {
		return model.ErrConflict
	}
	k := keyMV(f.ModelID, f.Version)
	if _, ok := s.idxMV[k]; ok {
		return model.ErrConflict
	}
	cp := *f
	s.data[f.ID] = &cp
	s.idxMV[k] = f.ID
	return nil
}

func (s *inMemoryFirmwareStore) Update(_ context.Context, f *model.Firmware) error {
	if f == nil {
		return model.ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.data[f.ID]
	if !ok {
		return model.ErrFirmwareNotFound
	}
	// 若型号或版本变化，更新二级索引。
	if old.ModelID != f.ModelID || old.Version != f.Version {
		oldK := keyMV(old.ModelID, old.Version)
		delete(s.idxMV, oldK)
		newK := keyMV(f.ModelID, f.Version)
		if existID, dup := s.idxMV[newK]; dup && existID != f.ID {
			return model.ErrConflict
		}
		s.idxMV[newK] = f.ID
	}
	cp := *f
	s.data[f.ID] = &cp
	return nil
}

func (s *inMemoryFirmwareStore) Get(_ context.Context, id string) (*model.Firmware, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.data[id]
	if !ok {
		return nil, model.ErrFirmwareNotFound
	}
	cp := *v
	return &cp, nil
}

func (s *inMemoryFirmwareStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.data[id]
	if !ok {
		return model.ErrFirmwareNotFound
	}
	delete(s.idxMV, keyMV(v.ModelID, v.Version))
	delete(s.data, id)
	return nil
}

func (s *inMemoryFirmwareStore) FindByModelAndVersion(_ context.Context, modelID, version string) (*model.Firmware, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.idxMV[keyMV(modelID, version)]
	if !ok {
		return nil, model.ErrFirmwareNotFound
	}
	v := s.data[id]
	cp := *v
	return &cp, nil
}

func (s *inMemoryFirmwareStore) SetStatus(_ context.Context, id string, status model.FirmwareStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.data[id]
	if !ok {
		return model.ErrFirmwareNotFound
	}
	v.Status = status
	cp := *v
	s.data[id] = &cp
	return nil
}

func (s *inMemoryFirmwareStore) List(_ context.Context, modelID, version, keyword, status, sortBy, sortOrder string, pageNum, pageSize int) ([]*model.Firmware, int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	all := make([]*model.Firmware, 0, len(s.data))
	for _, v := range s.data {
		if modelID != "" && v.ModelID != modelID {
			continue
		}
		if version != "" && v.Version != version {
			continue
		}
		if keyword != "" && !containsI(v.Name, keyword) && !containsI(v.Version, keyword) && !containsI(v.Description, keyword) {
			continue
		}
		if status != "" && string(v.Status) != status {
			continue
		}
		cp := *v
		all = append(all, &cp)
	}
	sortFirmware(all, sortBy, sortOrder)
	return paginateFW(all, pageNum, pageSize)
}

func sortFirmware(list []*model.Firmware, by, order string) {
	desc := order == "desc" || order == ""
	lessBy := func(i, j int) bool {
		switch by {
		case "version":
			r, ok := strutil.CompareVersion(list[i].Version, list[j].Version)
			if ok {
				if desc {
					return r > 0
				}
				return r < 0
			}
			// 回退字符串比较。
			if desc {
				return list[i].Version > list[j].Version
			}
			return list[i].Version < list[j].Version
		case "size":
			if desc {
				return list[i].Size > list[j].Size
			}
			return list[i].Size < list[j].Size
		case "created_at":
			fallthrough
		default:
			if desc {
				return list[i].CreatedAt.After(list[j].CreatedAt)
			}
			return list[i].CreatedAt.Before(list[j].CreatedAt)
		}
	}
	sort.Slice(list, lessBy)
}

func paginateFW(list []*model.Firmware, pn, ps int) ([]*model.Firmware, int64, error) {
	total := int64(len(list))
	pn, ps = normPage(pn, ps)
	start := (pn - 1) * ps
	if start >= len(list) {
		return []*model.Firmware{}, total, nil
	}
	end := start + ps
	if end > len(list) {
		end = len(list)
	}
	return list[start:end], total, nil
}

func (s *inMemoryFirmwareStore) ListByModel(_ context.Context, modelID string, status model.FirmwareStatus) ([]*model.Firmware, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	all := make([]*model.Firmware, 0)
	for _, v := range s.data {
		if v.ModelID != modelID {
			continue
		}
		if status != "" && v.Status != status {
			continue
		}
		cp := *v
		all = append(all, &cp)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].CreatedAt.After(all[j].CreatedAt) })
	return all, nil
}

// RawSnapshot 返回当前固件表的拷贝快照，用于运维诊断、数据对账。
// 返回的是值拷贝 map，调用方修改返回内容不会影响内部存储。
func (s *inMemoryFirmwareStore) RawSnapshot() map[string]model.Firmware {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]model.Firmware, len(s.data))
	for k, v := range s.data {
		out[k] = *v
	}
	return out
}

// SaveWithGuard 在写入前执行一组"字段守卫"校验：
// - 固件指针非空、ID 非空；
// - ModelID/Version/Name/MD5/FileName 非空；
// - Size >= 0；
// - MD5 长度必须为 32；
// - 型号+版本号组合唯一（除非 overwrite=true 且 ID 完全匹配）。
// 保存时会将结构体深拷贝到存储中，避免外部修改污染内部状态。
func (s *inMemoryFirmwareStore) SaveWithGuard(f *model.Firmware, overwrite bool) error {
	if f == nil {
		return model.ErrInvalidParam
	}
	if f.ID == "" {
		return model.ErrInvalidParam
	}
	if f.ModelID == "" || f.Version == "" || f.Name == "" || f.FileName == "" || f.MD5 == "" {
		return model.ErrInvalidParam
	}
	if f.Size < 0 {
		return model.ErrInvalidParam
	}
	if len(f.MD5) != 32 {
		return model.ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old, exist := s.data[f.ID]
	if exist && !overwrite {
		return model.ErrConflict
	}
	mvKey := keyMV(f.ModelID, f.Version)
	if confID, ok := s.idxMV[mvKey]; ok && confID != f.ID {
		return model.ErrConflict
	}
	if exist && overwrite {
		if old.ModelID != f.ModelID || old.Version != f.Version {
			delete(s.idxMV, keyMV(old.ModelID, old.Version))
		}
	}
	cp := *f
	s.data[f.ID] = &cp
	s.idxMV[mvKey] = f.ID
	return nil
}
