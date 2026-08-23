package firmware_upgrade

import (
	"context"
	"fmt"
	"os"
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
	_ = os.MkdirAll(cfg.DataDir, 0755)
	_ = os.MkdirAll(cfg.FirmwareDir, 0755)

	container := store.NewContainer()
	svc := service.NewServices(cfg, container)

	modelID := "MODEL-01"
	deviceID := "DEVICE-01"

	devModel := &model.DeviceModel{
		ID:          modelID,
		Name:        "Test Model",
		Description: "Test device model",
		Vendor:      "TestVendor",
		Arch:        "arm64",
		MemoryMB:    256,
		FlashMB:     1024,
		Enabled:     true,
		CreatedAt:   timeutil.Now(),
		UpdatedAt:   timeutil.Now(),
	}
	if err := container.Models.Create(ctx, devModel); err != nil {
		t.Fatalf("create model failed: %v", err)
	}

	fwID := idgen.NextID()
	fw := &model.Firmware{
		ID:            fwID,
		ModelID:       modelID,
		Version:       "v2.0.0",
		Name:          "Test Firmware v2",
		Description:   "Firmware for testing",
		MD5:           "abcdef1234567890abcdef1234567890",
		Size:          1024000,
		FilePath:      cfg.FirmwareDir + "/firmware.bin",
		FileName:      "firmware.bin",
		Status:        model.FirmwarePublished,
		ReleaseDate:   timeutil.Now(),
		MinFromVersion: "",
		CreatedBy:     "test",
		CreatedAt:     timeutil.Now(),
		UpdatedAt:     timeutil.Now(),
	}
	if err := container.Firmwares.Create(ctx, fw); err != nil {
		t.Fatalf("create firmware failed: %v", err)
	}

	dev := &model.Device{
		ID:              deviceID,
		ModelID:         modelID,
		Name:            "Test Device",
		CurrentVersion:  "v1.0.0",
		TargetVersion:   "",
		Status:          model.DeviceStatusOnline,
		IP:              "192.168.1.100",
		MAC:             "AA:BB:CC:DD:EE:FF",
		Group:           "production",
		Tags:            []string{"tag1", "tag2"},
		LastHeartbeatAt: timeutil.Now(),
		RegisterAt:      timeutil.Now(),
		UpdatedAt:       timeutil.Now(),
	}
	if err := container.Devices.Create(ctx, dev); err != nil {
		t.Fatalf("create device failed: %v", err)
	}

	task, err := svc.Task.Create(ctx, &model.CreateTaskRequest{
		Name:           "Test Upgrade Task",
		ModelID:        modelID,
		FromVersion:    "v1.0.0",
		TargetVersion:  "v2.0.0",
		FirmwareID:     fwID,
		Strategy:       model.StrategyDeviceList,
		GrayRatio:      100,
		DeviceIDs:      []string{deviceID},
		GroupFilter:    nil,
		ScheduleAt:     0,
		TimeoutSeconds: 3600,
		MaxRetry:       3,
		Description:    "Test task for red/green",
		CreatedBy:      "test",
	})
	if err != nil {
		t.Fatalf("create task failed: %v", err)
	}
	if task == nil {
		t.Fatal("create task returned nil")
	}

	devAfterTask, err := container.Devices.Get(ctx, deviceID)
	if err != nil {
		t.Fatalf("get device after task failed: %v", err)
	}

	pollReq := &model.PollUpgradeRequest{
		DeviceID:       deviceID,
		CurrentVersion: "",
		ModelID:        modelID,
		FreeSpaceMB:    1024,
	}
	pollResp, err := svc.Poll.Poll(ctx, pollReq)
	if err != nil {
		t.Fatalf("poll failed: %v", err)
	}

	deviceVersionOK := devAfterTask.CurrentVersion == "v1.0.0"
	deviceGroupOK := devAfterTask.Group == "production"
	pollResultOK := pollResp.NeedUpgrade

	if deviceVersionOK && deviceGroupOK && pollResultOK {
		fmt.Println("GREEN（绿灯，缺陷已修复）")
		fmt.Printf("  设备版本: %s (期望 v1.0.0)\n", devAfterTask.CurrentVersion)
		fmt.Printf("  设备分组: %s (期望 production)\n", devAfterTask.Group)
		fmt.Printf("  Poll 结果: NeedUpgrade=%v (期望 true)\n", pollResp.NeedUpgrade)
	} else {
		fmt.Println("RED（红灯，缺陷未修复）")
		if !deviceVersionOK {
			fmt.Printf("  设备版本被污染: %s (期望 v1.0.0)\n", devAfterTask.CurrentVersion)
		}
		if !deviceGroupOK {
			fmt.Printf("  设备分组被污染: %s (期望 production)\n", devAfterTask.Group)
		}
		if !pollResultOK {
			fmt.Printf("  Poll 返回 NeedUpgrade=false (期望 true)，消息: %s\n", pollResp.Message)
		}
		t.FailNow()
	}
}

func TestRedGreenGroupFilter(t *testing.T) {
	ctx := context.Background()
	cfg := config.Default()
	_ = os.MkdirAll(cfg.DataDir, 0755)
	_ = os.MkdirAll(cfg.FirmwareDir, 0755)

	container := store.NewContainer()
	svc := service.NewServices(cfg, container)

	modelID := "MODEL-02"
	deviceID := "DEVICE-02"

	devModel := &model.DeviceModel{
		ID:          modelID,
		Name:        "Test Model 2",
		Description: "Test device model 2",
		Vendor:      "TestVendor",
		Arch:        "arm64",
		MemoryMB:    256,
		FlashMB:     1024,
		Enabled:     true,
		CreatedAt:   timeutil.Now(),
		UpdatedAt:   timeutil.Now(),
	}
	if err := container.Models.Create(ctx, devModel); err != nil {
		t.Fatalf("create model failed: %v", err)
	}

	fwID := idgen.NextID()
	fw := &model.Firmware{
		ID:            fwID,
		ModelID:       modelID,
		Version:       "v2.0.0",
		Name:          "Test Firmware v2",
		Description:   "Firmware for testing",
		MD5:           "abcdef1234567890abcdef1234567890",
		Size:          1024000,
		FilePath:      cfg.FirmwareDir + "/firmware2.bin",
		FileName:      "firmware2.bin",
		Status:        model.FirmwarePublished,
		ReleaseDate:   timeutil.Now(),
		MinFromVersion: "",
		CreatedBy:     "test",
		CreatedAt:     timeutil.Now(),
		UpdatedAt:     timeutil.Now(),
	}
	if err := container.Firmwares.Create(ctx, fw); err != nil {
		t.Fatalf("create firmware failed: %v", err)
	}

	dev := &model.Device{
		ID:              deviceID,
		ModelID:         modelID,
		Name:            "Test Device 2",
		CurrentVersion:  "v1.0.0",
		TargetVersion:   "",
		Status:          model.DeviceStatusOnline,
		IP:              "192.168.1.101",
		MAC:             "AA:BB:CC:DD:EE:F0",
		Group:           "production",
		Tags:            []string{},
		LastHeartbeatAt: timeutil.Now(),
		RegisterAt:      timeutil.Now(),
		UpdatedAt:       timeutil.Now(),
	}
	if err := container.Devices.Create(ctx, dev); err != nil {
		t.Fatalf("create device failed: %v", err)
	}

	_, err := svc.Task.Create(ctx, &model.CreateTaskRequest{
		Name:           "Test Group Filter Task",
		ModelID:        modelID,
		FromVersion:    "v1.0.0",
		TargetVersion:  "v2.0.0",
		FirmwareID:     fwID,
		Strategy:       model.StrategyDeviceList,
		GrayRatio:      100,
		DeviceIDs:      []string{deviceID},
		GroupFilter:    []string{"staging"},
		ScheduleAt:     0,
		TimeoutSeconds: 3600,
		MaxRetry:       3,
		Description:    "Test task with group filter",
		CreatedBy:      "test",
	})
	if err != nil {
		t.Fatalf("create task failed: %v", err)
	}

	devAfterTask, err := container.Devices.Get(ctx, deviceID)
	if err != nil {
		t.Fatalf("get device after task failed: %v", err)
	}

	pollReq := &model.PollUpgradeRequest{
		DeviceID:       deviceID,
		CurrentVersion: "",
		ModelID:        modelID,
		FreeSpaceMB:    1024,
	}
	pollResp, err := svc.Poll.Poll(ctx, pollReq)
	if err != nil {
		t.Fatalf("poll failed: %v", err)
	}

	deviceVersionOK := devAfterTask.CurrentVersion == "v1.0.0"
	deviceGroupOK := devAfterTask.Group == "production"
	pollResultOK := pollResp.NeedUpgrade

	if deviceVersionOK && deviceGroupOK && pollResultOK {
		fmt.Println("GREEN（绿灯，缺陷已修复 - 版本与分组污染）")
		fmt.Printf("  设备版本: %s (期望 v1.0.0)\n", devAfterTask.CurrentVersion)
		fmt.Printf("  设备分组: %s (期望 production)\n", devAfterTask.Group)
		fmt.Printf("  Poll 结果: NeedUpgrade=%v (期望 true)\n", pollResp.NeedUpgrade)
	} else {
		fmt.Println("RED（红灯，缺陷未修复 - 版本与分组污染）")
		if !deviceVersionOK {
			fmt.Printf("  设备版本被污染: %s (期望 v1.0.0)\n", devAfterTask.CurrentVersion)
		}
		if !deviceGroupOK {
			fmt.Printf("  设备分组被污染: %s (期望 production)\n", devAfterTask.Group)
		}
		if !pollResultOK {
			fmt.Printf("  Poll 返回 NeedUpgrade=false (期望 true)，消息: %s\n", pollResp.Message)
		}
		t.FailNow()
	}
}