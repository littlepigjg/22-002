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
)

func TestRedGreen(t *testing.T) {
	ctx := context.Background()
	cfg := config.Default()
	_ = os.MkdirAll(cfg.DataDir, 0o755)
	_ = os.MkdirAll(cfg.FirmwareDir, 0o755)

	container := store.NewContainer()
	svc := service.NewServices(cfg, container)

	modelID := "MODEL-001"
	deviceID := "DEVICE-001"
	fwVersion := "v1.0.0"

	_, err := svc.Model.Create(ctx, &model.CreateModelRequest{
		ID:     modelID,
		Name:   "Test Model",
		Arch:   "arm64",
	})
	if err != nil {
		t.Fatalf("create model failed: %v", err)
	}

	fw, err := svc.Firmware.Create(ctx, &model.CreateFirmwareRequest{
		ModelID:    modelID,
		Version:    fwVersion,
		Name:       "Test Firmware",
		MD5:        "d41d8cd98f00b204e9800998ecf8427e",
		Size:       1024,
		FilePath:   cfg.FirmwareDir + "/test.bin",
		FileName:   "test.bin",
		ReleaseDate: "2024-01-01",
	})
	if err != nil {
		t.Fatalf("create firmware failed: %v", err)
	}

	_, err = svc.Firmware.UpdateStatus(ctx, fw.ID, model.FirmwarePublished)
	if err != nil {
		t.Fatalf("publish firmware failed: %v", err)
	}

	_, err = svc.Device.Register(ctx, &model.RegisterDeviceRequest{
		ID:             deviceID,
		ModelID:        modelID,
		Name:           "Test Device",
		CurrentVersion: "v0.9.0",
		IP:             "192.168.1.1",
	})
	if err != nil {
		t.Fatalf("register device failed: %v", err)
	}

	task, err := svc.Task.Create(ctx, &model.CreateTaskRequest{
		Name:          "Test Task",
		ModelID:       modelID,
		TargetVersion: fwVersion,
		FirmwareID:    fw.ID,
		Strategy:      model.StrategyFull,
		TimeoutSeconds: 3600,
		MaxRetry:      3,
	})
	if err != nil {
		t.Fatalf("create task failed: %v", err)
	}

	if task.Status != model.TaskStatusRunning {
		t.Fatalf("task should be running, got %s", task.Status)
	}

	pollReq := &model.PollUpgradeRequest{
		DeviceID:       deviceID,
		CurrentVersion: "v0.9.0",
		ModelID:        modelID,
	}
	pollResp, err := svc.Poll.Poll(ctx, pollReq)
	if err != nil {
		t.Fatalf("poll failed: %v", err)
	}
	if !pollResp.NeedUpgrade {
		t.Fatalf("expected need upgrade, got false: %s", pollResp.Message)
	}
	if pollResp.TaskID != task.ID {
		t.Fatalf("expected task ID %s, got %s", task.ID, pollResp.TaskID)
	}

	reportReq := &model.ReportProgressRequest{
		TaskID:    task.ID,
		DeviceID:  deviceID,
		Status:    model.UpgradeStatusDownloading,
		Progress:  50,
	}
	_, err = svc.Progress.Report(ctx, reportReq)
	if err != nil {
		t.Fatalf("report progress failed: %v", err)
	}

	_, err = svc.Task.UpdateStatus(ctx, task.ID, "pause", "manual pause")
	if err != nil {
		t.Fatalf("pause task failed: %v", err)
	}

	taskAfter, err := svc.Task.Get(ctx, task.ID)
	if err != nil {
		t.Fatalf("get task failed: %v", err)
	}
	if taskAfter.Status != model.TaskStatusPaused {
		t.Fatalf("task should be paused, got %s", taskAfter.Status)
	}

	secondReportReq := &model.ReportProgressRequest{
		TaskID:    task.ID,
		DeviceID:  deviceID,
		Status:    model.UpgradeStatusDownloading,
		Progress:  80,
	}
	_, reportErr := svc.Progress.Report(ctx, secondReportReq)

	secondPollReq := &model.PollUpgradeRequest{
		DeviceID:       deviceID,
		CurrentVersion: "v0.9.0",
		ModelID:        modelID,
	}
	secondPollResp, pollErr := svc.Poll.Poll(ctx, secondPollReq)

	if reportErr != nil {
		fmt.Println("GREEN（绿灯，缺陷已修复）")
		fmt.Printf("  进度上报正确拒绝: %v\n", reportErr)
	} else {
		fmt.Println("RED（红灯，缺陷未修复）")
		fmt.Println("  暂停任务后仍可上报进度，预期应被拒绝")
		t.Error("暂停任务后进度上报未被拒绝——缺陷未修复")
	}

	if pollErr == nil && secondPollResp != nil && secondPollResp.NeedUpgrade {
		if reportErr == nil {
			fmt.Println("RED（红灯，缺陷未修复）")
		}
		fmt.Println("  暂停任务后轮询仍返回升级信息，预期应返回无需升级")
		t.Error("暂停任务后轮询仍返回升级信息——缺陷未修复")
	} else {
		if reportErr != nil {
			fmt.Println("GREEN（绿灯，缺陷已修复）")
		}
		fmt.Println("  轮询正确拒绝: 暂停任务不返回升级信息")
	}

	if reportErr != nil {
		if pollErr == nil && secondPollResp != nil && !secondPollResp.NeedUpgrade {
			fmt.Println("")
			fmt.Println("=== 综合判定: GREEN（绿灯，缺陷已修复）===")
		}
	}
}
