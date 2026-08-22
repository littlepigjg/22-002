package service

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"firmware-upgrade/internal/model"
	"firmware-upgrade/internal/store"
)

// TestStressRegisterHeartbeatRace 模拟压测：并发注册 + 心跳 + 后台读（列表/统计），
// 用于复现 "concurrent map iteration and map write" / "concurrent map read and map write" 数据竞争。
func TestStressRegisterHeartbeatRace(t *testing.T) {
	c := store.NewContainer()
	svc := NewDeviceService(c.Devices, c.Models)

	// 预置一个型号，注册设备时需要校验型号存在。
	if err := c.Models.Create(context.Background(), &model.DeviceModel{
		ID:      "GW-100",
		Name:    "GW-100",
		Vendor:  "acme",
		Arch:    "arm64",
		Enabled: true,
	}); err != nil {
		t.Fatalf("seed model: %v", err)
	}

	const workers = 50
	const iters = 200

	var wg sync.WaitGroup
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 并发注册设备。
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				id := fmt.Sprintf("dev-%d-%d", w, i)
				req := &model.RegisterDeviceRequest{
					ID:             id,
					ModelID:        "GW-100",
					Name:           id,
					CurrentVersion: "v1.0.0",
					IP:             "10.0.0.1",
				}
				_, _ = svc.Register(ctx, req)
			}
		}(w)
	}

	// 并发心跳（针对已注册/未注册设备混合）。
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				id := fmt.Sprintf("dev-%d-%d", w, i)
				hb := &model.HeartbeatRequest{
					ID:             id,
					CurrentVersion: "v1.0.1",
					Status:         model.DeviceStatusOnline,
					IP:             "10.0.0.2",
				}
				_ = svc.Heartbeat(ctx, hb)
			}
		}(w)
	}

	// 后台读：设备列表、按型号统计、按状态统计、按版本统计。
	for w := 0; w < workers/2; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				_, _, _ = svc.List(ctx, &model.ListDeviceRequest{PageNum: 1, PageSize: 20})
				_, _ = svc.CountByModel(ctx)
				_, _, _, _ = svc.CountStatus(ctx)
				_, _ = svc.CountByVersion(ctx)
				_, _ = svc.Total(ctx)
			}
		}()
	}

	wg.Wait()
}
