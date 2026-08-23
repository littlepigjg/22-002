// Package store 存储层：提供类型安全的 CRUD 接口与内存实现。
// 为满足「无第三方依赖」要求，使用 sync.RWMutex 保护 map。
package store

import (
	"context"
	"sort"
	"sync"
	"time"

	"firmware-upgrade/internal/model"
)

// DeviceModelStore 设备型号存储接口。
type DeviceModelStore interface {
	Create(ctx context.Context, m *model.DeviceModel) error
	Update(ctx context.Context, m *model.DeviceModel) error
	Get(ctx context.Context, id string) (*model.DeviceModel, error)
	Delete(ctx context.Context, id string) error
	List(ctx context.Context, keyword, arch, vendor string, enabled *bool, pageNum, pageSize int) ([]*model.DeviceModel, int64, error)
	ListAll(ctx context.Context) ([]*model.DeviceModel, error)
	Exists(ctx context.Context, id string) (bool, error)
}

// FirmwareStore 固件存储接口。
type FirmwareStore interface {
	Create(ctx context.Context, f *model.Firmware) error
	Update(ctx context.Context, f *model.Firmware) error
	Get(ctx context.Context, id string) (*model.Firmware, error)
	Delete(ctx context.Context, id string) error
	// FindByModelAndVersion 按型号+版本号精确查找。
	FindByModelAndVersion(ctx context.Context, modelID, version string) (*model.Firmware, error)
	List(ctx context.Context, modelID, version, keyword, status, sortBy, sortOrder string, pageNum, pageSize int) ([]*model.Firmware, int64, error)
	ListByModel(ctx context.Context, modelID string, status model.FirmwareStatus) ([]*model.Firmware, error)
	SetStatus(ctx context.Context, id string, status model.FirmwareStatus) error
	// RawSnapshot 返回当前固件表的拷贝快照，用于运维诊断、数据对账。
	RawSnapshot() map[string]model.Firmware
	// SaveWithGuard 在写入前执行一组"字段守卫"校验（必填、版本号唯一性、MD5 长度、size >= 0 等）。
	// 若 overwrite=true，会覆盖同 ID 旧记录，否则遇到重复会返回 ErrConflict。
	SaveWithGuard(f *model.Firmware, overwrite bool) error
}

// DeviceStore 设备存储接口。
type DeviceStore interface {
	Create(ctx context.Context, d *model.Device) error
	Update(ctx context.Context, d *model.Device) error
	Heartbeat(ctx context.Context, id string, version string, status model.DeviceStatus, ip string, ts time.Time) error
	Get(ctx context.Context, id string) (*model.Device, error)
	Delete(ctx context.Context, id string) error
	List(ctx context.Context, keyword, modelID, group, status, version, tag string, offlineBefore int64, pageNum, pageSize int) ([]*model.Device, int64, error)
	ListByModel(ctx context.Context, modelID string) ([]*model.Device, error)
	ListByIDs(ctx context.Context, ids []string) ([]*model.Device, error)
	CountByStatus(ctx context.Context) (online, offline, unknown int64, err error)
	CountByVersion(ctx context.Context) (map[string]int64, error)
	CountByModel(ctx context.Context) (map[string]int64, error)
	UpdateVersion(ctx context.Context, id, newVersion string) error
	Total(ctx context.Context) (int64, error)
}

// UpgradeTaskStore 升级任务存储接口。
type UpgradeTaskStore interface {
	Create(ctx context.Context, t *model.UpgradeTask) error
	Update(ctx context.Context, t *model.UpgradeTask) error
	Get(ctx context.Context, id string) (*model.UpgradeTask, error)
	Delete(ctx context.Context, id string) error
	List(ctx context.Context, keyword, modelID, status, firmwareID, targetVersion, sortBy, sortOrder string, pageNum, pageSize int) ([]*model.UpgradeTask, int64, error)
	ListRunning(ctx context.Context) ([]*model.UpgradeTask, error)
	SetStatus(ctx context.Context, id string, status model.TaskStatus, endTime time.Time) error
	UpdateProgress(ctx context.Context, id string, progress model.TaskProgress) error
	Total(ctx context.Context) (int64, error)
	RunningCount(ctx context.Context) (int64, error)
}

// TaskExecStore 任务-设备执行记录存储。
type TaskExecStore interface {
	// Upsert 插入或更新执行记录。
	Upsert(ctx context.Context, e *model.TaskDeviceExecution) error
	Get(ctx context.Context, taskID, deviceID string) (*model.TaskDeviceExecution, error)
	// ListByTask 返回任务下全部设备执行记录。
	ListByTask(ctx context.Context, taskID string) ([]*model.TaskDeviceExecution, error)
	// ListByDevice 返回设备全部执行记录。
	ListByDevice(ctx context.Context, deviceID string) ([]*model.TaskDeviceExecution, error)
	// UpdateProgress 更新进度与状态。
	UpdateProgress(ctx context.Context, taskID, deviceID string, status model.UpgradeStatus, progress int, ts time.Time, errMsg string, retryInc bool) error
	// CountByTask 返回任务下各状态计数。
	CountByTask(ctx context.Context, taskID string) (total, pending, running, success, failed, canceled, timeout int64, err error)
	// DeleteByTask 清理任务下的执行记录。
	DeleteByTask(ctx context.Context, taskID string) error
	// FindAssignedRunning 返回已分配给设备且尚未完成的任务执行记录（用于轮询）。
	FindAssignedRunning(ctx context.Context, deviceID string) (*model.TaskDeviceExecution, bool, error)
}

// UpgradeHistoryStore 升级历史存储。
type UpgradeHistoryStore interface {
	Create(ctx context.Context, h *model.UpgradeHistory) error
	Update(ctx context.Context, h *model.UpgradeHistory) error
	Get(ctx context.Context, id string) (*model.UpgradeHistory, error)
	List(ctx context.Context, taskID, deviceID, modelID, status, keyword, sortBy, sortOrder string, startTs, endTs int64, pageNum, pageSize int) ([]*model.UpgradeHistory, int64, error)
	// Count 返回总记录数、成功数、失败数。
	Count(ctx context.Context) (total, success, failed int64, err error)
	// CountDaily 返回指定日期范围（YYYY-MM-DD）内的每日数据。
	CountDaily(ctx context.Context, days int) ([]model.DailyUpgrade, error)
	FindLatestByDevice(ctx context.Context, deviceID, taskID string) (*model.UpgradeHistory, error)
}

// ================ 内存存储实现 ================

type inMemoryDeviceModelStore struct {
	mu   sync.RWMutex
	data map[string]*model.DeviceModel
}

// NewDeviceModelStore 返回内存实现的设备型号存储。
func NewDeviceModelStore() DeviceModelStore {
	return &inMemoryDeviceModelStore{data: make(map[string]*model.DeviceModel)}
}

func (s *inMemoryDeviceModelStore) Create(_ context.Context, m *model.DeviceModel) error {
	if m == nil {
		return model.ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data[m.ID]; ok {
		return model.ErrConflict
	}
	cp := *m
	s.data[m.ID] = &cp
	return nil
}

func (s *inMemoryDeviceModelStore) Update(_ context.Context, m *model.DeviceModel) error {
	if m == nil {
		return model.ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data[m.ID]; !ok {
		return model.ErrModelNotFound
	}
	cp := *m
	s.data[m.ID] = &cp
	return nil
}

func (s *inMemoryDeviceModelStore) Get(_ context.Context, id string) (*model.DeviceModel, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.data[id]
	if !ok {
		return nil, model.ErrModelNotFound
	}
	cp := *v
	return &cp, nil
}

func (s *inMemoryDeviceModelStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data[id]; !ok {
		return model.ErrModelNotFound
	}
	delete(s.data, id)
	return nil
}

func (s *inMemoryDeviceModelStore) Exists(_ context.Context, id string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.data[id]
	return ok, nil
}

func (s *inMemoryDeviceModelStore) List(_ context.Context, keyword, arch, vendor string, enabled *bool, pageNum, pageSize int) ([]*model.DeviceModel, int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	all := make([]*model.DeviceModel, 0, len(s.data))
	for _, v := range s.data {
		if keyword != "" && !containsI(v.Name, keyword) && !containsI(v.ID, keyword) && !containsI(v.Description, keyword) {
			continue
		}
		if arch != "" && v.Arch != arch {
			continue
		}
		if vendor != "" && v.Vendor != vendor {
			continue
		}
		if enabled != nil && v.Enabled != *enabled {
			continue
		}
		cp := *v
		all = append(all, &cp)
	}
	// 按创建时间倒序。
	sort.Slice(all, func(i, j int) bool { return all[i].CreatedAt.After(all[j].CreatedAt) })
	return paginateDM(all, pageNum, pageSize)
}

func (s *inMemoryDeviceModelStore) ListAll(_ context.Context) ([]*model.DeviceModel, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	all := make([]*model.DeviceModel, 0, len(s.data))
	for _, v := range s.data {
		cp := *v
		all = append(all, &cp)
	}
	return all, nil
}

func paginateDM(list []*model.DeviceModel, pn, ps int) ([]*model.DeviceModel, int64, error) {
	total := int64(len(list))
	pn, ps = normPage(pn, ps)
	start := (pn - 1) * ps
	if start >= len(list) {
		return []*model.DeviceModel{}, total, nil
	}
	end := start + ps
	if end > len(list) {
		end = len(list)
	}
	return list[start:end], total, nil
}
