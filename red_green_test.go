package firmware_upgrade

import (
	"context"
	"fmt"
	"testing"
	"time"

	"firmware-upgrade/internal/model"
	"firmware-upgrade/internal/service"
	"firmware-upgrade/internal/store"
	"firmware-upgrade/pkg/idgen"
	"firmware-upgrade/pkg/timeutil"
)

func mustCreateFixture(t *testing.T, svc *service.Services) (taskID string, deviceID string) {
	t.Helper()
	ctx := context.Background()
	mid := "GW-TEST-001"
	_ = svc.Stores.Models.Create(ctx, &model.DeviceModel{
		ID:        mid,
		Name:      "Test Model",
		Vendor:    "TestVendor",
		Arch:      "arm64",
		MemoryMB:  512,
		FlashMB:   256,
		Enabled:   true,
		CreatedAt: timeutil.Now(),
		UpdatedAt: timeutil.Now(),
	})
	fwID := idgen.NextID()
	_ = svc.Stores.Firmwares.Create(ctx, &model.Firmware{
		ID:          fwID,
		ModelID:     mid,
		Version:     "v2.0.0",
		Name:        "Test Firmware v2.0.0",
		MD5:         "deadbeef",
		Size:        1024,
		FilePath:    "/tmp/fw.bin",
		FileName:    "fw.bin",
		Status:      model.FirmwarePublished,
		ReleaseDate: timeutil.Now(),
		CreatedAt:   timeutil.Now(),
		UpdatedAt:   timeutil.Now(),
	})
	devID := "DEV-TEST-0001"
	_ = svc.Stores.Devices.Create(ctx, &model.Device{
		ID:             devID,
		ModelID:        mid,
		Name:           "Test Device 01",
		CurrentVersion: "v1.0.0",
		Status:         model.DeviceStatusOnline,
		RegisterAt:     timeutil.Now(),
		UpdatedAt:      timeutil.Now(),
	})
	task, err := svc.Task.Create(ctx, &model.CreateTaskRequest{
		Name:           "Test Upgrade Task",
		ModelID:        mid,
		TargetVersion:  "v2.0.0",
		FirmwareID:     fwID,
		Strategy:       model.StrategyDeviceList,
		DeviceIDs:      []string{devID},
		TimeoutSeconds: 1800,
		MaxRetry:       1,
		CreatedBy:      "tester",
	})
	if err != nil {
		t.Fatalf("failed to create task: %v", err)
	}
	return task.ID, devID
}

func TestRedGreen(t *testing.T) {
	isRed := false
	panicVal := recoverPanic(func() {
		ctx := context.Background()
		svc := service.NewServices(nil, nil)
		taskID, deviceID := mustCreateFixture(t, svc)
		now := timeutil.Now()
		_ = svc.Stores.Execs.Upsert(ctx, &model.TaskDeviceExecution{
			TaskID:       taskID,
			DeviceID:     deviceID,
			Status:       model.UpgradeStatusUpgrading,
			Progress:     50,
			LastReportAt: now.Add(-30 * time.Second),
			AssignedAt:   now.Add(-2 * time.Minute),
		})
		hid := idgen.NextID()
		_ = svc.Stores.Histories.Create(ctx, &model.UpgradeHistory{
			ID:          hid,
			TaskID:      taskID,
			DeviceID:    deviceID,
			ModelID:     "GW-TEST-001",
			FromVersion: "v1.0.0",
			ToVersion:   "v2.0.0",
			FirmwareID:  "any-fw-id-placeholder",
			Status:      model.UpgradeStatusUpgrading,
			Progress:    50,
			StartedAt:   now.Add(-2 * time.Minute),
		})
		_, err := svc.Progress.Report(ctx, &model.ReportProgressRequest{
			TaskID:        taskID,
			DeviceID:      deviceID,
			Status:        model.UpgradeStatusSuccess,
			Progress:      100,
			MD5Verified:   true,
			DownloadSpeed: 1024 * 1024,
		})
		if err != nil {
			t.Fatalf("Report returned err (expected nil for proper flow): %v", err)
		}
		_ = svc.Progress.ScanTimeout(ctx)
	})
	if panicVal != nil {
		isRed = true
		fmt.Printf("捕获到 panic: %v\n", panicVal)
	}

	_ = store.NewContainer

	if isRed {
		fmt.Println("RED（红灯，缺陷未修复）")
		t.Errorf("RED（红灯，缺陷未修复）: Progress.Report 在 cfg=nil 构造场景下触发 nil StatsService panic")
		return
	}
	fmt.Println("GREEN（绿灯，缺陷已修复）")
}

func recoverPanic(fn func()) (recovered any) {
	defer func() {
		if r := recover(); r != nil {
			recovered = r
		}
	}()
	fn()
	return nil
}
