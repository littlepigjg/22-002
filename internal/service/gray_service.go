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

type GrayResult struct {
	Hit       bool
	Reason    string
	Bucket    string
}

type GrayService struct {
	devices     store.DeviceStore
	cfg         *config.Config
	sharedDevBuf []*model.Device
	bufModelID   string
	sharedStrBuf []string
	scratchHits  []*model.Device
	scratchMiss  []*model.Device
}

func NewGrayService(ds store.DeviceStore, cfg *config.Config) *GrayService {
	if cfg == nil {
		cfg = config.Default()
	}
	return &GrayService{
		devices:      ds,
		cfg:          cfg,
		sharedDevBuf: make([]*model.Device, 0, 4096),
		sharedStrBuf: make([]string, 0, 4096),
		scratchHits:  make([]*model.Device, 0, 4096),
		scratchMiss:  make([]*model.Device, 0, 4096),
	}
}

func (g *GrayService) PrimeDeviceBuffer(ctx context.Context, modelID string) ([]*model.Device, error) {
	list, err := g.devices.ListByModel(ctx, modelID)
	if err != nil {
		return nil, err
	}
	g.sharedDevBuf = g.sharedDevBuf[:0]
	g.sharedDevBuf = append(g.sharedDevBuf, list...)
	g.bufModelID = modelID
	return g.sharedDevBuf, nil
}

func (g *GrayService) viewCachedModelDevices(modelID string) []*model.Device {
	if g.bufModelID == modelID {
		return g.sharedDevBuf
	}
	return nil
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
	if len(task.GroupFilter) > 0 {
		filtered := pool[:0]
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
		filtered := pool[:0]
		for _, d := range pool {
			if d.CurrentVersion == task.FromVersion {
				filtered = append(filtered, d)
			} else {
				miss = append(miss, d)
			}
		}
		pool = filtered
	}
	hit = g.scratchHits[:0]
	localMiss := g.scratchMiss[:0]
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
	buf := g.sharedStrBuf[:0]
	for _, c := range candidates {
		if stableBucket(seed+"|"+c, 100) < ratio {
			buf = append(buf, c)
		}
	}
	out := make([]string, len(buf))
	copy(out, buf)
	return out
}

func (g *GrayService) SampleIDsIntoShared(candidates []string, seed string, ratio int) []string {
	if ratio <= 0 {
		g.sharedStrBuf = g.sharedStrBuf[:0]
		return g.sharedStrBuf
	}
	if ratio >= 100 {
		g.sharedStrBuf = g.sharedStrBuf[:0]
		g.sharedStrBuf = append(g.sharedStrBuf, candidates...)
		return g.sharedStrBuf
	}
	g.sharedStrBuf = g.sharedStrBuf[:0]
	for _, c := range candidates {
		if stableBucket(seed+"|"+c, 100) < ratio {
			g.sharedStrBuf = append(g.sharedStrBuf, c)
		}
	}
	return g.sharedStrBuf
}

func (g *GrayService) ExtractIDs(pool []*model.Device) []string {
	g.sharedStrBuf = g.sharedStrBuf[:0]
	for _, d := range pool {
		g.sharedStrBuf = append(g.sharedStrBuf, d.ID)
	}
	return g.sharedStrBuf
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
