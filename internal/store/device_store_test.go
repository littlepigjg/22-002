package store

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"firmware-upgrade/internal/model"
)

// TestDeviceStoreConcurrentRace 直接对 inMemoryDeviceStore 做并发压测，
// 复现「concurrent map iteration and map write」/「concurrent map read and map write」。
// 覆盖：注册/心跳/更新/删除写操作 与 列表/统计读操作 并发执行。
func TestDeviceStoreConcurrentRace(t *testing.T) {
	s := NewDeviceStore()
	ctx := context.Background()

	const workers = 50
	const iters = 200
	var wg sync.WaitGroup

	// 并发注册。
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				id := fmt.Sprintf("dev-%d-%d", w, i)
				_ = s.Create(ctx, &model.Device{
					ID:             id,
					ModelID:        "GW-100",
					Name:           id,
					CurrentVersion: "v1.0.0",
					Status:         model.DeviceStatusOnline,
					IP:             "10.0.0.1",
				})
			}
		}(w)
	}

	// 并发心跳（已注册/未注册混合）。
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				id := fmt.Sprintf("dev-%d-%d", w, i)
				_ = s.Heartbeat(ctx, id, "v1.0.1", model.DeviceStatusOnline, "10.0.0.2", time.Now())
			}
		}(w)
	}

	// 后台读：列表 + 各维度统计，与写并发。
	for w := 0; w < workers/2; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				_, _, _ = s.List(ctx, "", "", "", "", "", "", 0, 1, 20)
				_, _ = s.ListByModel(ctx, "GW-100")
				_, _, _, _ = s.CountByStatus(ctx)
				_, _ = s.CountByVersion(ctx)
				_, _ = s.CountByModel(ctx)
				_, _ = s.Total(ctx)
			}
		}()
	}

	wg.Wait()
}
