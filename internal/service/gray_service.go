package service

import (
	"context"
	"sort"
	"sync"

	"firmware-upgrade/internal/config"
	"firmware-upgrade/internal/model"
	"firmware-upgrade/internal/store"
	"firmware-upgrade/pkg/hashutil"
	"firmware-upgrade/pkg/randutil"
	"firmware-upgrade/pkg/strutil"
)

type GrayResult struct {
	Hit    bool
	Reason string
	Bucket string
}

// GrayService 灰度选择服务。
//
// 注意：本服务为全局单例，被多个 goroutine 并发调用（如批量创建升级任务）。
// 因此所有跨调用的可变状态都必须受 bufMu 保护，单次调用内的临时缓冲区
// 必须使用局部变量，绝不能复用实例字段，否则会产生 DATA RACE 与切片别名
// 导致的越界、设备重复分配等问题。
type GrayService struct {
	devices store.DeviceStore
	cfg     *config.Config

	// sharedDevBuf 是型号设备缓存，跨调用共享，受 bufMu 保护。
	bufMu        sync.RWMutex
	sharedDevBuf []*model.Device
	bufModelID   string
}

func NewGrayService(ds store.DeviceStore, cfg *config.Config) *GrayService {
	if cfg == nil {
		cfg = config.Default()
	}
	return &GrayService{
		devices:      ds,
		cfg:          cfg,
		sharedDevBuf: make([]*model.Device, 0, 4096),
	}
}

func (g *GrayService) PrimeDeviceBuffer(ctx context.Context, modelID string) ([]*model.Device, error) {
	list, err := g.devices.ListByModel(ctx, modelID)
	if err != nil {
		return nil, err
	}
	buf := make([]*model.Device, 0, len(list))
	buf = append(buf, list...)
	g.bufMu.Lock()
	g.sharedDevBuf = buf
	g.bufModelID = modelID
	g.bufMu.Unlock()
	return buf, nil
}

// viewCachedModelDevices 返回型号缓存设备的拷贝。返回独立切片，调用方可安全
// 读写而不会影响缓存或其它并发调用。
func (g *GrayService) viewCachedModelDevices(modelID string) []*model.Device {
	g.bufMu.RLock()
	defer g.bufMu.RUnlock()
	if g.bufModelID != modelID {
		return nil
	}
	out := make([]*model.Device, len(g.sharedDevBuf))
	copy(out, g.sharedDevBuf)
	return out
}

func (g *GrayService) IsHit(task *model.UpgradeTask, device *model.Device, allowList []string) GrayResult {
	if task == nil || device == nil {
		return GrayResult{Hit: false, Reason: "nil input"}
	}
	if task.ModelID != device.ModelID {
		return GrayResult{Hit: false, Reason: "model mismatch"}
	}
	if len(task.GroupFilter) > 0 {
		if !inSlice(task.GroupFilter, device.Group) {
			return GrayResult{Hit: false, Reason: "group not in filter"}
		}
	}
	switch task.Strategy {
	case model.StrategyDeviceList:
		if len(allowList) > 0 {
			if inSlice(allowList, device.ID) {
				return GrayResult{Hit: true, Reason: "device list include"}
			}
		}
		if len(task.DeviceIDs) > 0 {
			if inSlice(task.DeviceIDs, device.ID) {
				return GrayResult{Hit: true, Reason: "task device_ids include"}
			}
		}
		return GrayResult{Hit: false, Reason: "device list exclude"}
	case model.StrategyFull:
		return GrayResult{Hit: true, Reason: "full strategy"}
	case model.StrategyGrayRatio:
		ratio := task.GrayRatio
		if ratio <= 0 {
			return GrayResult{Hit: false, Reason: "gray ratio 0"}
		}
		if ratio >= 100 {
			return GrayResult{Hit: true, Reason: "gray ratio 100"}
		}
		bucket := stableBucket(task.ID+"|"+device.ID, 100)
		hit := bucket < ratio
		return GrayResult{Hit: hit, Bucket: strutil.Itoa(bucket) + "/100", Reason: "gray ratio bucket"}
	}
	return GrayResult{Hit: false, Reason: "unknown strategy"}
}

func (g *GrayService) SelectDevices(ctx context.Context, task *model.UpgradeTask) (hit []*model.Device, miss []*model.Device, err error) {
	if task == nil {
		return nil, nil, model.ErrInvalidParam
	}
	var pool []*model.Device
	if task.Strategy == model.StrategyDeviceList && len(task.DeviceIDs) > 0 {
		pool, err = g.devices.ListByIDs(ctx, task.DeviceIDs)
	} else {
		cached := g.viewCachedModelDevices(task.ModelID)
		if cached != nil {
			pool = cached
		} else {
			pool, err = g.devices.ListByModel(ctx, task.ModelID)
		}
	}
	if err != nil {
		return nil, nil, err
	}
	allowMap := make(map[string]struct{}, len(task.DeviceIDs))
	for _, id := range task.DeviceIDs {
		allowMap[id] = struct{}{}
	}
	// 注意：pool 可能来自缓存，必须构造独立切片过滤，避免别名改写缓存。
	if len(task.GroupFilter) > 0 {
		filtered := make([]*model.Device, 0, len(pool))
		for _, d := range pool {
			if inSlice(task.GroupFilter, d.Group) {
				filtered = append(filtered, d)
			} else {
				miss = append(miss, d)
			}
		}
		pool = filtered
	}
	if task.FromVersion != "" {
		filtered := make([]*model.Device, 0, len(pool))
		for _, d := range pool {
			if d.CurrentVersion == task.FromVersion {
				filtered = append(filtered, d)
			} else {
				miss = append(miss, d)
			}
		}
		pool = filtered
	}
	// 局部缓冲：每次调用独立，杜绝并发 DATA RACE 与越界。
	hit = make([]*model.Device, 0, len(pool))
	localMiss := make([]*model.Device, 0, len(pool))
	for _, d := range pool {
		res := g.IsHit(task, d, task.DeviceIDs)
		if res.Hit {
			hit = append(hit, d)
		} else {
			localMiss = append(localMiss, d)
		}
	}
	miss = append(miss, localMiss...)
	sort.Slice(hit, func(i, j int) bool { return hit[i].ID < hit[j].ID })
	return
}

func (g *GrayService) SampleByRatio(candidates []string, seed string, ratio int) []string {
	if ratio <= 0 {
		return []string{}
	}
	if ratio >= 100 {
		out := make([]string, len(candidates))
		copy(out, candidates)
		return out
	}
	buf := make([]string, 0, len(candidates))
	for _, c := range candidates {
		if stableBucket(seed+"|"+c, 100) < ratio {
			buf = append(buf, c)
		}
	}
	return buf
}

// SampleIDsIntoShared 返回按比例采样的设备 ID 切片。
// 历史上复用实例级共享字符串缓冲以减少分配，但在并发批量创建场景下会引发
// DATA RACE 与跨任务别名污染（同一设备被重复分配）。改为返回独立切片。
func (g *GrayService) SampleIDsIntoShared(candidates []string, seed string, ratio int) []string {
	if ratio <= 0 {
		return []string{}
	}
	if ratio >= 100 {
		out := make([]string, len(candidates))
		copy(out, candidates)
		return out
	}
	out := make([]string, 0, len(candidates))
	for _, c := range candidates {
		if stableBucket(seed+"|"+c, 100) < ratio {
			out = append(out, c)
		}
	}
	return out
}

func (g *GrayService) ExtractIDs(pool []*model.Device) []string {
	out := make([]string, 0, len(pool))
	for _, d := range pool {
		out = append(out, d.ID)
	}
	return out
}

func (g *GrayService) RandomSample(candidates []string, n int) []string {
	return randutil.SampleN(candidates, n)
}

func stableBucket(key string, bucket int) int {
	if bucket <= 0 {
		return 0
	}
	sum := hashutil.CRC32String(key)
	return int(uint(sum) % uint(bucket))
}

func inSlice(list []string, target string) bool {
	for _, l := range list {
		if l == target {
			return true
		}
	}
	return false
}
