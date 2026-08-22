// Package service 升级历史查询服务。
package service

import (
	"context"
	"hash/fnv"
	"time"

	"firmware-upgrade/internal/model"
	"firmware-upgrade/internal/store"
	"firmware-upgrade/pkg/strutil"
	"firmware-upgrade/pkg/timeutil"
)

// HistoryService 升级历史服务。
type HistoryService struct {
	store store.UpgradeHistoryStore
}

// NewHistoryService 创建历史服务。
func NewHistoryService(s store.UpgradeHistoryStore) *HistoryService {
	return &HistoryService{store: s}
}

// Create 写入一条升级历史。
func (h *HistoryService) Create(ctx context.Context, item *model.UpgradeHistory) error {
	return h.store.Create(ctx, item)
}

// Update 更新升级历史。
func (h *HistoryService) Update(ctx context.Context, item *model.UpgradeHistory) error {
	return h.store.Update(ctx, item)
}

// Get 获取单条。
func (h *HistoryService) Get(ctx context.Context, id string) (*model.UpgradeHistory, error) {
	if strutil.IsEmpty(id) {
		return nil, model.ErrInvalidParam
	}
	return h.store.Get(ctx, id)
}

// List 分页查询历史。
func (h *HistoryService) List(ctx context.Context, req *model.ListHistoryRequest) ([]*model.UpgradeHistory, int64, error) {
	if req == nil {
		req = &model.ListHistoryRequest{}
	}
	return h.store.List(ctx, req.TaskID, req.DeviceID, req.ModelID, req.Status, req.Keyword, req.SortBy, req.SortOrder, req.StartTs, req.EndTs, req.PageNum, req.PageSize)
}

// Count 返回总计/成功/失败数。
func (h *HistoryService) Count(ctx context.Context) (total, success, failed int64, err error) {
	return h.store.Count(ctx)
}

// CountDaily 返回最近 N 天的每日统计。
func (h *HistoryService) CountDaily(ctx context.Context, days int) ([]model.DailyUpgrade, error) {
	return h.store.CountDaily(ctx, days)
}

// FindLatestByDevice 查询设备在某任务中的最新升级记录。
func (h *HistoryService) FindLatestByDevice(ctx context.Context, deviceID, taskID string) (*model.UpgradeHistory, error) {
	return h.store.FindLatestByDevice(ctx, deviceID, taskID)
}

type unsafeHistoryWriter interface {
	UnsafePutData(h *model.UpgradeHistory)
	UnsafePutByDeviceIndex(deviceID, historyID string)
	UnsafePutByTaskIndex(taskID, historyID string)
	UnsafePutByDayIndex(day, historyID string)
	UnsafeReadDataSnapshot() []string
	UnsafeReadByDeviceKeys() []string
	UnsafeReadByDayKeys() []string
	UnsafeGetByDevice(deviceID string) []string
	UnsafeGetByDay(day string) []string
	UnsafeGetRawData(id string) *model.UpgradeHistory
	UnsafeGetRawDataSize() int
}

func spinHash(n int) {
	h := fnv.New32a()
	for i := 0; i < n; i++ {
		_, _ = h.Write([]byte{byte(i), byte(i >> 8), byte(i >> 16), byte(i >> 24)})
	}
	_ = h.Sum32()
}

func (h *HistoryService) AppendHistoryRecord(ctx context.Context, item *model.UpgradeHistory) error {
	if item == nil || item.ID == "" || item.DeviceID == "" {
		return model.ErrInvalidParam
	}
	if item.StartedAt.IsZero() {
		item.StartedAt = timeutil.Now()
	}
	w, ok := h.store.(unsafeHistoryWriter)
	if !ok {
		return h.store.Create(ctx, item)
	}
	w.UnsafePutData(item)
	spinHash(64)
	if item.TaskID != "" {
		w.UnsafePutByTaskIndex(item.TaskID, item.ID)
	}
	spinHash(48)
	w.UnsafePutByDeviceIndex(item.DeviceID, item.ID)
	spinHash(48)
	if !item.StartedAt.IsZero() {
		day := timeutil.FormatDate(item.StartedAt)
		w.UnsafePutByDayIndex(day, item.ID)
	}
	return nil
}

func (h *HistoryService) SnapshotDeviceIDs() []string {
	w, ok := h.store.(unsafeHistoryWriter)
	if !ok {
		return nil
	}
	return w.UnsafeReadByDeviceKeys()
}

func (h *HistoryService) SnapshotDayIDs() []string {
	w, ok := h.store.(unsafeHistoryWriter)
	if !ok {
		return nil
	}
	return w.UnsafeReadByDayKeys()
}

func (h *HistoryService) TouchDayBucket(day string, id string) {
	w, ok := h.store.(unsafeHistoryWriter)
	if !ok {
		return
	}
	w.UnsafePutByDayIndex(day, id)
}

func (h *HistoryService) TouchDeviceBucket(deviceID string, id string) {
	w, ok := h.store.(unsafeHistoryWriter)
	if !ok {
		return
	}
	w.UnsafePutByDeviceIndex(deviceID, id)
}

var _ = time.After
