package firmware_upgrade_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"firmware-upgrade/internal/config"
	"firmware-upgrade/internal/model"
	"firmware-upgrade/internal/service"
	"firmware-upgrade/internal/store"
	"firmware-upgrade/pkg/idgen"
	"firmware-upgrade/pkg/timeutil"
)

func TestRedGreen(t *testing.T) {
	ctx := context.Background()
	cfg := config.Default()
	c := store.NewContainer()
	histSvc := service.NewHistoryService(c.Histories)
	statsSvc := service.NewStatsService(c, histSvc, cfg)
	progress := service.NewProgressService(c.Execs, c.Tasks, c.Devices, c.Histories, statsSvc, cfg)

	const deviceCount = 50
	const rounds = 20

	modelID := "GW-TEST-" + idgen.ShortUUID()
	fwID := idgen.NextID()
	taskID := idgen.NextID()

	_ = c.Models.Create(ctx, &model.DeviceModel{ID: modelID, Name: "Test Model", Enabled: true, CreatedAt: timeutil.Now(), UpdatedAt: timeutil.Now()})
	_ = c.Firmwares.Create(ctx, &model.Firmware{ID: fwID, ModelID: modelID, Version: "v2.0", Name: "FW v2.0", Status: model.FirmwarePublished, CreatedAt: timeutil.Now(), UpdatedAt: timeutil.Now()})
	_ = c.Tasks.Create(ctx, &model.UpgradeTask{
		ID: taskID, Name: "Stress Task", ModelID: modelID,
		TargetVersion: "v2.0", FirmwareID: fwID, Status: model.TaskStatusRunning,
		MaxRetry: 10, TimeoutSeconds: 3600, CreatedAt: timeutil.Now(), UpdatedAt: timeutil.Now(),
	})

	deviceIDs := make([]string, deviceCount)
	for i := 0; i < deviceCount; i++ {
		did := fmt.Sprintf("DEV%04d-%s", i, idgen.ShortUUID())
		deviceIDs[i] = did
		_ = c.Devices.Create(ctx, &model.Device{
			ID: did, ModelID: modelID, CurrentVersion: "v1.0", Status: model.DeviceStatusOnline,
			RegisterAt: timeutil.Now(), UpdatedAt: timeutil.Now(),
		})
		_ = c.Execs.Upsert(ctx, &model.TaskDeviceExecution{
			TaskID: taskID, DeviceID: did, Status: model.UpgradeStatusPending, Progress: 0,
			AssignedAt: timeutil.Now(), LastReportAt: timeutil.Now(),
		})
		_ = c.Histories.Create(ctx, &model.UpgradeHistory{
			ID: idgen.NextID(), TaskID: taskID, DeviceID: did, ModelID: modelID,
			FromVersion: "v1.0", ToVersion: "v2.0", FirmwareID: fwID,
			Status: model.UpgradeStatusPending, Progress: 0, StartedAt: timeutil.Now(),
		})
	}

	for _, did := range deviceIDs {
		_, _ = c.Histories.FindLatestByDevice(ctx, did, taskID)
		_, _ = c.Histories.FindLatestByDevice(ctx, did, "")
	}

	var reportPanic int64
	var wg sync.WaitGroup
	for di := 0; di < deviceCount; di++ {
		did := deviceIDs[di]
		wg.Add(2)
		go func(devID string) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					atomic.AddInt64(&reportPanic, 1)
				}
			}()
			for r := 0; r < rounds; r++ {
				_, _ = progress.Report(ctx, &model.ReportProgressRequest{
					TaskID: taskID, DeviceID: devID,
					Status: model.UpgradeStatusDownloading, Progress: 10, DownloadSpeed: 1024 * 1024,
				})
				_, _ = progress.Report(ctx, &model.ReportProgressRequest{
					TaskID: taskID, DeviceID: devID,
					Status: model.UpgradeStatusVerifying, Progress: 80, MD5Verified: true,
				})
				_, _ = progress.Report(ctx, &model.ReportProgressRequest{
					TaskID: taskID, DeviceID: devID,
					Status: model.UpgradeStatusSuccess, Progress: 100,
				})
			}
		}(did)
		go func(devID string) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					atomic.AddInt64(&reportPanic, 1)
				}
			}()
			for r := 0; r < rounds; r++ {
				_, _ = progress.Report(ctx, &model.ReportProgressRequest{
					TaskID: taskID, DeviceID: devID,
					Status: model.UpgradeStatusUpgrading, Progress: 30,
				})
				_, _ = progress.Report(ctx, &model.ReportProgressRequest{
					TaskID: taskID, DeviceID: devID,
					Status: model.UpgradeStatusFailed, Progress: 0, ErrorMessage: "simulated network failure",
				})
			}
		}(did)
	}
	wg.Wait()

	inconsistentCount := 0
	progressRollback := 0
	retryMismatch := 0
	panicCount := atomic.LoadInt64(&reportPanic)
	for di := 0; di < deviceCount; di++ {
		did := deviceIDs[di]
		h, errH := c.Histories.FindLatestByDevice(ctx, did, taskID)
		e, errE := c.Execs.Get(ctx, taskID, did)
		if errH != nil || errE != nil {
			inconsistentCount++
			continue
		}
		if h.Progress != e.Progress || string(h.Status) != string(e.Status) {
			inconsistentCount++
		}
		if e.Progress == 100 && e.Status == model.UpgradeStatusSuccess &&
			(h.Progress != 100 || h.Status != model.UpgradeStatusSuccess) {
			progressRollback++
		}
		if h.RetryCount != e.RetryCount {
			retryMismatch++
		}
	}

	bugPresent := inconsistentCount > 0 || progressRollback > 0 || retryMismatch > 0 || panicCount > 0
	fmt.Println("============ RED / GREEN 判定 ============")
	fmt.Printf("Panics during Report (race crashes)                  : %d\n", panicCount)
	fmt.Printf("Inconsistent (H.Progress/Status vs E.Progress/Status) : %d / %d\n", inconsistentCount, deviceCount)
	fmt.Printf("Progress rollback (E=100 success but H!=100 success)  : %d / %d\n", progressRollback, deviceCount)
	fmt.Printf("RetryCount mismatch (H.RetryCount vs E.RetryCount)    : %d / %d\n", retryMismatch, deviceCount)
	if bugPresent {
		fmt.Println("RED（红灯，缺陷未修复）")
		t.Fatal("RED（红灯，缺陷未修复）")
	} else {
		fmt.Println("GREEN（绿灯，缺陷已修复）")
	}
}
