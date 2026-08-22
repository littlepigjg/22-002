// Package service 历史服务并发安全测试。
package service

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"firmware-upgrade/internal/model"
	"firmware-upgrade/internal/store"
	"firmware-upgrade/pkg/timeutil"
)

// TestHistoryServiceConcurrentStress 复现压测场景：一半协程反复上报 Progress 到达
// Success/Failed 终态（走 AppendHistoryRecord 的 Unsafe 写路径），另一半反复拉
// CountDaily 与 FindLatestByDevice，验证无 data race、无 panic、统计数对得上。
func TestHistoryServiceConcurrentStress(t *testing.T) {
	histStore := store.NewUpgradeHistoryStore()
	hs := NewHistoryService(histStore)

	const writers = 20
	const readers = 20
	const iters = 150

	var wg sync.WaitGroup
	wg.Add(writers + readers)
	start := make(chan struct{})

	// writers: 反复到达 Success/Failed 终态，走生产 AppendHistoryRecord 路径。
	for i := 0; i < writers; i++ {
		i := i
		go func() {
			defer wg.Done()
			<-start
			devID := fmt.Sprintf("dev-%d", i)
			taskID := fmt.Sprintf("task-%d", i%4)
			for n := 0; n < iters; n++ {
				now := timeutil.Now()
				status := model.UpgradeStatusSuccess
				if n%2 == 1 {
					status = model.UpgradeStatusFailed
				}
				h := &model.UpgradeHistory{
					ID:        fmt.Sprintf("%s:%s:%d-%d", taskID, devID, n, now.UnixNano()),
					TaskID:    taskID,
					DeviceID:  devID,
					ModelID:   "GW-100",
					Status:    status,
					Progress:  100,
					StartedAt: now,
				}
				if err := hs.AppendHistoryRecord(context.Background(), h); err != nil {
					t.Errorf("AppendHistoryRecord: %v", err)
				}
			}
		}()
	}

	// readers: 反复拉 CountDaily 与单设备最新历史。
	for i := 0; i < readers; i++ {
		i := i
		go func() {
			defer wg.Done()
			<-start
			devID := fmt.Sprintf("dev-%d", i%writers)
			taskID := fmt.Sprintf("task-%d", i%4)
			for n := 0; n < iters; n++ {
				if _, err := hs.CountDaily(context.Background(), 7); err != nil {
					t.Errorf("CountDaily: %v", err)
				}
				if _, err := hs.FindLatestByDevice(context.Background(), devID, taskID); err != nil && err != model.ErrNotFound {
					t.Errorf("FindLatestByDevice: %v", err)
				}
				if _, _, _, err := hs.Count(context.Background()); err != nil {
					t.Errorf("Count: %v", err)
				}
			}
		}()
	}

	close(start)
	wg.Wait()

	// 写入完成后校验统计数：每条记录写入一次，byDay 与 data 应一致。
	total, success, failed, err := hs.Count(context.Background())
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	wantTotal := int64(writers * iters)
	if total != wantTotal {
		t.Errorf("Count total = %d, want %d", total, wantTotal)
	}
	if success+failed != total {
		t.Errorf("success+failed=%d != total=%d", success+failed, total)
	}

	daily, err := hs.CountDaily(context.Background(), 1)
	if err != nil {
		t.Fatalf("CountDaily: %v", err)
	}
	if len(daily) != 1 {
		t.Fatalf("daily len = %d, want 1", len(daily))
	}
	if daily[0].Total != wantTotal {
		t.Errorf("CountDaily total = %d, want %d (no double counting)", daily[0].Total, wantTotal)
	}
	if daily[0].Success+daily[0].Failed != daily[0].Total {
		t.Errorf("daily success+failed=%d != total=%d", daily[0].Success+daily[0].Failed, daily[0].Total)
	}
}

// TestHistoryServiceConcurrentCreateUpdate FindLatest 同时验证 Create/Update 并发正确性。
func TestHistoryServiceConcurrentCreateUpdate(t *testing.T) {
	histStore := store.NewUpgradeHistoryStore()
	hs := NewHistoryService(histStore)

	const n = 40
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			devID := fmt.Sprintf("dev-%d", i)
			taskID := "task-A"
			now := timeutil.Now()
			h := &model.UpgradeHistory{
				ID:        fmt.Sprintf("h-%d-%d", i, now.UnixNano()),
				TaskID:    taskID,
				DeviceID:  devID,
				Status:    model.UpgradeStatusUpgrading,
				Progress:  50,
				StartedAt: now,
			}
			if err := hs.Create(context.Background(), h); err != nil {
				t.Errorf("Create: %v", err)
				return
			}
			// 读最新，再 Update。
			latest, err := hs.FindLatestByDevice(context.Background(), devID, taskID)
			if err != nil {
				t.Errorf("FindLatestByDevice: %v", err)
				return
			}
			latest.Status = model.UpgradeStatusSuccess
			latest.Progress = 100
			if err := hs.Update(context.Background(), latest); err != nil {
				t.Errorf("Update: %v", err)
			}
		}()
	}
	wg.Wait()

	total, success, _, err := hs.Count(context.Background())
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if total != n {
		t.Errorf("total = %d, want %d", total, n)
	}
	if success != n {
		t.Errorf("success = %d, want %d", success, n)
	}
}

var _ = time.Now
