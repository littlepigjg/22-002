// Package service 进度上报并发压测：复现压测场景下 data race / panic / 统计错乱问题。
package service

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"firmware-upgrade/internal/config"
	"firmware-upgrade/internal/model"
	"firmware-upgrade/internal/store"
	"firmware-upgrade/pkg/timeutil"
)

// TestProgressReportConcurrentStress 复现线上压测场景：
//   - 一半协程反复上报 Progress，到达 Success/Failed 终态（走 AppendHistoryRecord 的 Unsafe 写路径）
//   - 另一半协程反复拉 CountDaily 与单设备最新历史 FindLatestByDevice
//
// 要求：无 data race、无 panic、CountDaily 统计与 Count 一致。
func TestProgressReportConcurrentStress(t *testing.T) {
	cfg := config.Default()
	c := store.NewContainer()
	svc := NewServices(cfg, c)

	ctx := context.Background()
	// 准备型号、设备、运行中任务。
	mid := "GW-100"
	if _, err := svc.Model.Create(ctx, &model.CreateModelRequest{
		ID: mid, Name: "GW-100", Vendor: "Acme", Arch: "arm64",
		MemoryMB: 512, FlashMB: 128, Enabled: boolPtr(true),
	}); err != nil {
		t.Fatalf("create model: %v", err)
	}

	taskID := "task-stress"
	task := &model.UpgradeTask{
		ID:            taskID,
		Name:          "stress",
		ModelID:       mid,
		FromVersion:    "v1.1.0",
		TargetVersion: "v1.2.0",
		FirmwareID:    "fw-1",
		Status:        model.TaskStatusRunning,
		TimeoutSeconds: 60,
		MaxRetry:      3,
	}
	if err := c.Tasks.Create(ctx, task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	const half = 20 // 共 40 协程
	deviceIDs := make([]string, half)
	for i := 0; i < half; i++ {
		devID := fmt.Sprintf("dev-%d", i)
		deviceIDs[i] = devID
		if _, err := svc.Device.Register(ctx, &model.RegisterDeviceRequest{
			ID: devID, ModelID: mid, Name: devID, CurrentVersion: "v1.1.0",
		}); err != nil {
			t.Fatalf("register device: %v", err)
		}
		// 预置执行记录，使 UpdateProgress 走更新分支。
		_ = c.Execs.Upsert(ctx, &model.TaskDeviceExecution{
			TaskID: taskID, DeviceID: devID, Status: model.UpgradeStatusUpgrading,
			Progress: 10, AssignedAt: timeutil.Now(), LastReportAt: timeutil.Now(),
		})
	}

	const iters = 120
	var wg sync.WaitGroup
	wg.Add(half * 2)
	start := make(chan struct{})

	// 一半协程：反复上报 Progress 到 Success/Failed 终态。
	for i := 0; i < half; i++ {
		i := i
		go func() {
			defer wg.Done()
			<-start
			devID := deviceIDs[i]
			for n := 0; n < iters; n++ {
				status := model.UpgradeStatusSuccess
				progress := 100
				if n%2 == 1 {
					status = model.UpgradeStatusFailed
					progress = 50
				}
				req := &model.ReportProgressRequest{
					TaskID:   taskID,
					DeviceID: devID,
					Status:   status,
					Progress: progress,
				}
				if _, err := svc.Progress.Report(ctx, req); err != nil {
					// 任务可能在统计中被标记为 finished，导致后续 report 被拒，属正常。
					_ = err
				}
			}
		}()
	}

	// 另一半协程：反复拉 CountDaily 与单设备最新历史。
	for i := 0; i < half; i++ {
		i := i
		go func() {
			defer wg.Done()
			<-start
			devID := deviceIDs[i]
			for n := 0; n < iters; n++ {
				if _, err := svc.History.CountDaily(ctx, 7); err != nil {
					t.Errorf("CountDaily: %v", err)
				}
				if _, err := svc.History.FindLatestByDevice(ctx, devID, taskID); err != nil && err != model.ErrNotFound {
					t.Errorf("FindLatestByDevice: %v", err)
				}
				// 同时拉统计总览（走 StatsService.build -> Count/CountDaily）。
				if _, err := svc.Stats.Get(ctx); err != nil {
					t.Errorf("Stats.Get: %v", err)
				}
			}
		}()
	}

	close(start)
	wg.Wait()

	// 校验：所有写入完成后，CountDaily(今日) 的 Total 应等于 Count 的 total（不重复计数）。
	total, _, _, err := svc.History.Count(ctx)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if total == 0 {
		t.Fatalf("expected some history records, got 0")
	}
	daily, err := svc.History.CountDaily(ctx, 1)
	if err != nil {
		t.Fatalf("CountDaily: %v", err)
	}
	if len(daily) != 1 {
		t.Fatalf("daily len = %d, want 1", len(daily))
	}
	if daily[0].Total != total {
		t.Errorf("CountDaily total = %d, want %d (Count), index must not double-count", daily[0].Total, total)
	}
}

func boolPtr(b bool) *bool { return &b }
