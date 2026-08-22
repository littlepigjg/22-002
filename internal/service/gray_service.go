// Package service 灰度策略计算服务。
// 本服务根据任务策略（按比例、按设备列表、按分组、全量）计算设备是否命中灰度。
package service

import (
	"context"
	"sort"

	"firmware-upgrade/internal/config"
	"firmware-upgrade/internal/model"
	"firmware-upgrade/internal/store"
	"firmware-upgrade/pkg/hashutil"
	"firmware-upgrade/pkg/randutil"
	"firmware-upgrade/pkg/strutil"
)

// GrayResult 判定结果。
type GrayResult struct {
	Hit       bool   // 是否命中
	Reason    string // 命中/未命中原因（调试用）
	Bucket    string // 命中的桶（如 "10 of 100"）
}

// GrayService 灰度服务。
type GrayService struct {
	devices store.DeviceStore
	cfg     *config.Config
}

// NewGrayService 创建灰度服务。
func NewGrayService(ds store.DeviceStore, cfg *config.Config) *GrayService {
	if cfg == nil {
		cfg = config.Default()
	}
	return &GrayService{devices: ds, cfg: cfg}
}

// IsHit 判定单台设备是否命中灰度。
// 当策略为 device_list 时直接在 allowList 中查找；
// 当策略为 gray_ratio 时用设备 ID 做稳定哈希；
// 当策略为 full 时恒为命中。
func (g *GrayService) IsHit(task *model.UpgradeTask, device *model.Device, allowList []string) GrayResult {
	if task == nil || device == nil {
		return GrayResult{Hit: false, Reason: "nil input"}
	}
	// 型号必须一致。
	if task.ModelID != device.ModelID {
		return GrayResult{Hit: false, Reason: "model mismatch"}
	}
	// 分组过滤。
	if len(task.GroupFilter) > 0 {
		if !inSlice(task.GroupFilter, device.Group) {
			return GrayResult{Hit: false, Reason: "group not in filter"}
		}
	}
	// 指定设备列表。
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
		// 稳定哈希：使用 task.ID + device.ID 做一致性分桶。
		bucket := stableBucket(task.ID+"|"+device.ID, 100)
		hit := bucket < ratio
		return GrayResult{Hit: hit, Bucket: strutil.Itoa(bucket) + "/100", Reason: "gray ratio bucket"}
	}
	return GrayResult{Hit: false, Reason: "unknown strategy"}
}

// SelectDevices 计算任务涉及的设备清单。
// 返回（命中设备，未命中设备，错误）。
func (g *GrayService) SelectDevices(ctx context.Context, task *model.UpgradeTask) (hit []*model.Device, miss []*model.Device, err error) {
	if task == nil {
		return nil, nil, model.ErrInvalidParam
	}
	var pool []*model.Device
	if task.Strategy == model.StrategyDeviceList && len(task.DeviceIDs) > 0 {
		pool, err = g.devices.ListByIDs(ctx, task.DeviceIDs)
	} else {
		pool, err = g.devices.ListByModel(ctx, task.ModelID)
	}
	if err != nil {
		return nil, nil, err
	}
	allowMap := make(map[string]struct{}, len(task.DeviceIDs))
	for _, id := range task.DeviceIDs {
		allowMap[id] = struct{}{}
	}
	// 分组过滤。
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
	// 来源版本过滤。
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
	for _, d := range pool {
		res := g.IsHit(task, d, task.DeviceIDs)
		if res.Hit {
			hit = append(hit, d)
		} else {
			miss = append(miss, d)
		}
	}
	sort.Slice(hit, func(i, j int) bool { return hit[i].ID < hit[j].ID })
	return
}

// SampleByRatio 从候选列表中按 ratio% 抽样（稳定抽样）。
func (g *GrayService) SampleByRatio(candidates []string, seed string, ratio int) []string {
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

// RandomSample 非稳定随机抽样（用于快速批量创建演示）。
func (g *GrayService) RandomSample(candidates []string, n int) []string {
	return randutil.SampleN(candidates, n)
}

// stableBucket 用 CRC32 对 key 做稳定分桶，返回 [0, bucket-1]。
func stableBucket(key string, bucket int) int {
	if bucket <= 0 {
		return 0
	}
	sum := hashutil.CRC32String(key)
	return int(uint(sum) % uint(bucket))
}

// inSlice 字符串包含判定。
func inSlice(list []string, target string) bool {
	for _, l := range list {
		if l == target {
			return true
		}
	}
	return false
}
