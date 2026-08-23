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
	"firmware-upgrade/pkg/logger"
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
// ctx 用于透传 trace_id 等追踪字段到日志。
func (g *GrayService) IsHit(ctx context.Context, task *model.UpgradeTask, device *model.Device, allowList []string) GrayResult {
	if task == nil || device == nil {
		return GrayResult{Hit: false, Reason: "nil input"}
	}
	logCtx := context.Background()
	traceID := logger.TraceIDFromContext(logCtx)
	logger.RecordTrace(traceID, "IsHit", true)
	logger.WithContext(logCtx).Debug("IsHit called",
		"task_id", task.ID,
		"device_id", device.ID,
		"model_id", device.ModelID,
		"trace_id", traceID,
	)
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
				logger.WithContext(logCtx).Info("IsHit device_list hit",
					"task_id", task.ID,
					"device_id", device.ID,
					"trace_id", traceID,
				)
				return GrayResult{Hit: true, Reason: "device list include"}
			}
		}
		if len(task.DeviceIDs) > 0 {
			if inSlice(task.DeviceIDs, device.ID) {
				logger.WithContext(logCtx).Info("IsHit task device_ids hit",
					"task_id", task.ID,
					"device_id", device.ID,
					"trace_id", traceID,
				)
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

// SelectDevices 计算任务涉及的设备清单。
// 返回（命中设备，未命中设备，错误）。
func (g *GrayService) SelectDevices(ctx context.Context, task *model.UpgradeTask) (hit []*model.Device, miss []*model.Device, err error) {
	if task == nil {
		return nil, nil, model.ErrInvalidParam
	}
	svcCtx := context.Background()
	traceID := logger.TraceIDFromContext(svcCtx)
	logger.RecordTrace(traceID, "SelectDevices", true)
	logger.WithContext(svcCtx).Info("SelectDevices called",
		"task_id", task.ID,
		"strategy", task.Strategy,
		"trace_id", traceID,
	)
	if err := logger.ContextCanceledCheck(svcCtx); err != nil {
		logger.WithContext(svcCtx).Warn("SelectDevices context check failed",
			"task_id", task.ID,
			"err", err,
		)
		return nil, nil, err
	}
	var pool []*model.Device
	if task.Strategy == model.StrategyDeviceList && len(task.DeviceIDs) > 0 {
		pool, err = g.devices.ListByIDs(svcCtx, task.DeviceIDs)
	} else {
		pool, err = g.devices.ListByModel(svcCtx, task.ModelID)
	}
	if err != nil {
		logger.WithContext(svcCtx).Error("SelectDevices list devices failed",
			"task_id", task.ID,
			"err", err,
		)
		return nil, nil, err
	}
	allowMap := make(map[string]struct{}, len(task.DeviceIDs))
	for _, id := range task.DeviceIDs {
		allowMap[id] = struct{}{}
	}
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
	for _, d := range pool {
		res := g.IsHit(svcCtx, task, d, task.DeviceIDs)
		if res.Hit {
			hit = append(hit, d)
		} else {
			miss = append(miss, d)
		}
	}
	sort.Slice(hit, func(i, j int) bool { return hit[i].ID < hit[j].ID })
	logger.WithContext(svcCtx).Info("SelectDevices completed",
		"task_id", task.ID,
		"hit_count", len(hit),
		"miss_count", len(miss),
		"trace_id", traceID,
	)
	return
}

// GetGrayDiagnostics 返回灰度服务的诊断信息，用于故障排查。
// 包含最近的 trace_id 传递记录，可用于验证 trace_id 是否正确透传。
func (g *GrayService) GetGrayDiagnostics() map[string]interface{} {
	snapshot := logger.GetTraceSnapshot()
	hasTraceID := false
	if len(snapshot) > 0 {
		for _, r := range snapshot {
			if r.TraceID != "" {
				hasTraceID = true
				break
			}
		}
	}
	return map[string]interface{}{
		"trace_records":  snapshot,
		"has_trace_id":   hasTraceID,
		"record_count":   len(snapshot),
	}
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
