package firmware_upgrade_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"firmware-upgrade/internal/config"
	"firmware-upgrade/internal/model"
	"firmware-upgrade/internal/service"
	"firmware-upgrade/internal/store"
	"firmware-upgrade/pkg/safemap"
)

func TestRedGreen(t *testing.T) {
	passed := true
	defer func() {
		if !passed || t.Failed() {
			fmt.Println("RED（红灯，缺陷未修复）")
		} else {
			fmt.Println("GREEN（绿灯，缺陷已修复）")
		}
	}()

	ctx := context.Background()
	rounds := 2
	devicesPerRound := 36
	iterations := devicesPerRound * 5
	concurrency := 24

	for r := 0; r < rounds; r++ {
		cfg := config.Default()
		stores := store.NewContainer()
		svc := service.NewServices(cfg, stores)

		modelID := fmt.Sprintf("GW-%d", r)
		if err := stores.Models.Create(ctx, &model.DeviceModel{
			ID: modelID, Name: modelID, Vendor: "v", Arch: "arm64",
			MemoryMB: 512, FlashMB: 256, Enabled: true, CreatedAt: time.Now(), UpdatedAt: time.Now(),
		}); err != nil {
			t.Fatalf("create model failed: %v", err)
		}

		fwID := fmt.Sprintf("fw-%d", r)
		if err := stores.Firmwares.Create(ctx, &model.Firmware{
			ID: fwID, ModelID: modelID, Version: "v2.0.0", Name: "firmware",
			MD5: "abc123", Size: 1024 * 1024, FilePath: "/tmp/fw.bin", FileName: "fw.bin",
			Status: model.FirmwarePublished, ReleaseDate: time.Now(), CreatedAt: time.Now(), UpdatedAt: time.Now(),
		}); err != nil {
			t.Fatalf("create firmware failed: %v", err)
		}

		for i := 0; i < devicesPerRound; i++ {
			did := fmt.Sprintf("dev-%d-%d", r, i)
			if err := stores.Devices.Create(ctx, &model.Device{
				ID: did, ModelID: modelID, Name: did,
				CurrentVersion: "v1.0.0", Status: model.DeviceStatusOnline,
				RegisterAt: time.Now(), UpdatedAt: time.Now(), LastHeartbeatAt: time.Now(),
			}); err != nil {
				t.Fatalf("create device failed: %v", err)
			}
		}

		taskID := fmt.Sprintf("task-%d", r)
		now := time.Now()
		if err := stores.Tasks.Create(ctx, &model.UpgradeTask{
			ID: taskID, Name: "full upgrade", ModelID: modelID, TargetVersion: "v2.0.0",
			FirmwareID: fwID, Strategy: model.StrategyFull, GrayRatio: 100,
			DeviceIDs: nil, Status: model.TaskStatusRunning, ScheduleAt: now, StartTime: now,
			TimeoutSeconds: 3600, MaxRetry: 3, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("create task failed: %v", err)
		}

		prePopulate := devicesPerRound / 2
		for i := 0; i < prePopulate; i++ {
			did := fmt.Sprintf("dev-%d-%d", r, i)
			_ = stores.Execs.Upsert(ctx, &model.TaskDeviceExecution{
				TaskID:       taskID,
				DeviceID:     did,
				Status:       model.UpgradeStatusDownloading,
				Progress:     (i * 13) % 100,
				AssignedAt:   now,
				LastReportAt: now,
			})
		}

		var recoveredPanics int64
		var writerPanics int64
		stopWriter := make(chan struct{})
		var writerWg sync.WaitGroup

		writerWg.Add(1)
		go func() {
			defer writerWg.Done()
			cursor := 0
			for {
				select {
				case <-stopWriter:
					return
				default:
				}
				func() {
					defer func() {
						if rec := recover(); rec != nil {
							atomic.AddInt64(&writerPanics, 1)
						}
					}()
					did := fmt.Sprintf("dev-%d-%d", r, cursor%devicesPerRound)
					cursor++
					progress := cursor % 101
					_ = stores.Execs.UpdateProgress(ctx, taskID, did, model.UpgradeStatusUpgrading, progress, time.Now(), "", false)
				}()
			}
		}()

		sem := make(chan struct{}, concurrency)
		var pollWg sync.WaitGroup
		for i := 0; i < iterations; i++ {
			did := fmt.Sprintf("dev-%d-%d", r, i%devicesPerRound)
			sem <- struct{}{}
			pollWg.Add(1)
			go func(idx int, dev string) {
				defer pollWg.Done()
				defer func() { <-sem }()
				defer func() {
					if rec := recover(); rec != nil {
						atomic.AddInt64(&recoveredPanics, 1)
					}
				}()
				pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
				defer cancel()
				req := &model.PollUpgradeRequest{
					DeviceID:       dev,
					CurrentVersion: "v1.0.0",
					ModelID:        modelID,
				}
				_, _ = svc.Poll.Poll(pctx, req)
			}(i, did)
		}
		pollWg.Wait()
		close(stopWriter)
		writerWg.Wait()

		total := atomic.LoadInt64(&recoveredPanics) + atomic.LoadInt64(&writerPanics)
		if total > 0 {
			passed = false
			t.Errorf("round %d: %d panics detected during concurrent Poll+Update (poll=%d writer=%d)",
				r, total, atomic.LoadInt64(&recoveredPanics), atomic.LoadInt64(&writerPanics))
		}

		sm := safemap.New[string, int](4)
		for i := 0; i < 150; i++ {
			sm.Set(fmt.Sprintf("k%d", i), i)
		}
		var wg2 sync.WaitGroup
		var rPanic int64
		wg2.Add(2)
		go func() {
			defer wg2.Done()
			defer func() {
				if rec := recover(); rec != nil {
					atomic.AddInt64(&rPanic, 1)
				}
			}()
			for j := 0; j < 350; j++ {
				sm.ForEach(func(k string, v int) {
					_ = k
					_ = v
				})
			}
		}()
		go func() {
			defer wg2.Done()
			defer func() {
				if rec := recover(); rec != nil {
					atomic.AddInt64(&rPanic, 1)
				}
			}()
			for j := 0; j < 350; j++ {
				sm.Set(fmt.Sprintf("k%d", j%150), j)
				sm.Delete(fmt.Sprintf("k%d", (j+75)%150))
			}
		}()
		wg2.Wait()
		if atomic.LoadInt64(&rPanic) > 0 {
			passed = false
			t.Errorf("round %d: safemap direct ForEach/Set concurrent panics=%d", r, atomic.LoadInt64(&rPanic))
		}
	}
}
