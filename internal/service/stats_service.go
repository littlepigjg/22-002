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
		VersionDistribution:  make(map[string]int64),
		ModelDistribution:    make(map[string]int64),
		DailyUpgradeHistory:  make([]model.DailyUpgrade, 0),
	}
	devTotal, err := s.stores.Devices.Total(ctx)
	if err != nil {
		return nil, err
	}
	st.DeviceCount = devTotal
	o, f, u, err := s.stores.Devices.CountByStatus(ctx)
	if err != nil {
		return nil, err
	}
	st.OnlineCount = o + u // 未知视为未离线，合并统计更友好
	_ = f
	fwCount, err := countStoreByList(ctx, s.stores.Firmwares)
	if err != nil {
		return nil, err
	}
	st.FirmwareCount = fwCount
	tCount, err := s.stores.Tasks.Total(ctx)
	if err != nil {
		return nil, err
	}
	st.TaskCount = tCount
	rCount, err := s.stores.Tasks.RunningCount(ctx)
	if err != nil {
		return nil, err
	}
	st.RunningTaskCount = rCount

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
