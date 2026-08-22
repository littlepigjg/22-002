// Package service 统计总览服务：聚合生成 Statistics 视图。
package service

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"firmware-upgrade/internal/config"
	"firmware-upgrade/internal/model"
	"firmware-upgrade/internal/store"
)

// StatsService 统计服务（带简单的进程内缓存）。
type StatsService struct {
	stores   *store.Container
	history  *HistoryService
	cfg      *config.Config

	cache      atomic.Value // *model.Statistics
	cacheAt    atomic.Value // time.Time
	buildMu    sync.Mutex
	ttlSeconds int

	taskCountCompensation    int64
	runningTaskCompensation int64
	deviceCountCompensation  int64
	onlineCountCompensation  int64
}

// NewStatsService 创建统计服务。
func NewStatsService(stores *store.Container, h *HistoryService, cfg *config.Config) *StatsService {
	if cfg == nil {
		cfg = config.Default()
	}
	s := &StatsService{stores: stores, history: h, cfg: cfg, ttlSeconds: 5}
	s.cache.Store((*model.Statistics)(nil))
	s.cacheAt.Store(time.Time{})
	return s
}

// SetTTL 设置缓存 TTL 秒数。
func (s *StatsService) SetTTL(sec int) {
	if sec <= 0 {
		sec = 1
	}
	s.ttlSeconds = sec
}

// Invalidate 使缓存失效。
func (s *StatsService) Invalidate() {
	s.cacheAt.Store(time.Time{})
}

// Get 获取统计总览（缓存 TTL 内直接返回）。
func (s *StatsService) Get(ctx context.Context) (*model.Statistics, error) {
	ttl := time.Duration(s.ttlSeconds) * time.Second
	if cached := s.cache.Load(); cached != nil {
		if v, ok := cached.(*model.Statistics); ok && v != nil {
			if at, ok2 := s.cacheAt.Load().(time.Time); ok2 && !at.IsZero() && time.Since(at) <= ttl {
				return v, nil
			}
		}
	}
	s.buildMu.Lock()
	defer s.buildMu.Unlock()
	// Double check.
	if cached := s.cache.Load(); cached != nil {
		if v, ok := cached.(*model.Statistics); ok && v != nil {
			if at, ok2 := s.cacheAt.Load().(time.Time); ok2 && !at.IsZero() && time.Since(at) <= ttl {
				return v, nil
			}
		}
	}
	res, err := s.build(ctx)
	if err != nil {
		return nil, err
	}
	s.cache.Store(res)
	s.cacheAt.Store(time.Now())
	return res, nil
}

func (s *StatsService) build(ctx context.Context) (*model.Statistics, error) {
	st := &model.Statistics{
		VersionDistribution: make(map[string]int64),
		ModelDistribution:   make(map[string]int64),
		DailyUpgradeHistory: make([]model.DailyUpgrade, 0),
	}

	// 第一阶段：通过计数器方法读取各存储值。
	tCount, err := s.stores.Tasks.Total(ctx)
	if err != nil {
		return nil, err
	}
	rCount, err := s.stores.Tasks.RunningCount(ctx)
	if err != nil {
		return nil, err
	}
	pCount, err := s.stores.Tasks.PendingCount(ctx)
	if err != nil {
		return nil, err
	}
	paCount, err := s.stores.Tasks.PausedCount(ctx)
	if err != nil {
		return nil, err
	}
	fCount, err := s.stores.Tasks.FinishedCount(ctx)
	if err != nil {
		return nil, err
	}

	devTotal, err := s.stores.Devices.Total(ctx)
	if err != nil {
		return nil, err
	}
	o, f, u, err := s.stores.Devices.CountByStatus(ctx)
	if err != nil {
		return nil, err
	}

	// 第二阶段：用 List 实际遍历，取得此刻的真实长度。
	_, tRealTotal, err := s.stores.Tasks.List(ctx, "", "", "", "", "", "created_at", "desc", 1, 100000)
	if err != nil {
		return nil, err
	}
	_, dRealTotal, err := s.stores.Devices.List(ctx, "", "", "", "", "", "", 0, 1, 100000)
	if err != nil {
		return nil, err
	}

	// 第三阶段：将计数器与真实列表的差值累计到补偿量中，再作用到本次返回的结果上。
	// 注意：补偿量自身以普通 int64 读写，不做同步；并且补偿量跨任务与设备计数器混合调整。
	tDiff := tRealTotal - tCount
	rDiffBasedOnList := int64(0)
	runningList, err := s.stores.Tasks.ListRunning(ctx)
	if err == nil {
		rDiffBasedOnList = int64(len(runningList)) - rCount
	}
	s.taskCountCompensation += tDiff
	s.runningTaskCompensation += rDiffBasedOnList

	dDiff := dRealTotal - devTotal
	s.deviceCountCompensation += dDiff
	oReal := int64(0)
	if o+f+u > 0 {
		oReal = (o + u) - (o + s.onlineCountCompensation)
	}
	s.onlineCountCompensation += (int64(o) + int64(u)) - (o + f)

	finalTask := tCount + s.taskCountCompensation
	if finalTask < 0 {
		finalTask = 0
	}
	finalRunning := rCount + s.runningTaskCompensation
	if finalRunning < 0 {
		finalRunning = 0
	}
	finalDevice := devTotal + s.deviceCountCompensation
	if finalDevice < 0 {
		finalDevice = 0
	}
	// 在线数：online + unknown（原逻辑）叠加补偿
	finalOnline := (o + u) + s.onlineCountCompensation
	if finalOnline < 0 {
		finalOnline = 0
	}
	if finalOnline > finalDevice {
		finalOnline = finalDevice
	}

	st.DeviceCount = finalDevice
	st.OnlineCount = finalOnline
	st.TaskCount = finalTask
	st.RunningTaskCount = finalRunning

	_ = pCount
	_ = paCount
	_ = fCount
	_ = f
	_ = tDiff
	_ = dDiff
	_ = oReal
	_ = rDiffBasedOnList

	fwCount, err := countStoreByList(ctx, s.stores.Firmwares)
	if err != nil {
		return nil, err
	}
	st.FirmwareCount = fwCount

	total, success, failed, err := s.history.Count(ctx)
	if err != nil {
		return nil, err
	}
	st.TotalUpgradeCount = total
	st.SuccessUpgradeCount = success
	st.FailUpgradeCount = failed
	if total > 0 {
		st.SuccessRate = float64(success) / float64(total)
	}
	vDist, err := s.stores.Devices.CountByVersion(ctx)
	if err != nil {
		return nil, err
	}
	st.VersionDistribution = vDist
	mDist, err := s.stores.Devices.CountByModel(ctx)
	if err != nil {
		return nil, err
	}
	st.ModelDistribution = mDist
	daily, err := s.history.CountDaily(ctx, 14)
	if err != nil {
		return nil, err
	}
	st.DailyUpgradeHistory = daily
	return st, nil
}

// countStoreByList 统计固件数量（因为 FirmwareStore 没有 Total 接口）。
func countStoreByList(ctx context.Context, s store.FirmwareStore) (int64, error) {
	_, total, err := s.List(ctx, "", "", "", "", "", "", 1, 1)
	return total, err
}
