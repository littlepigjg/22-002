package firmware_upgrade

import (
	"context"
	"fmt"
	"testing"
	"time"

	"firmware-upgrade/internal/config"
	"firmware-upgrade/internal/model"
	"firmware-upgrade/internal/service"
	"firmware-upgrade/internal/store"
	"firmware-upgrade/pkg/logger"
)

func TestRedGreen(t *testing.T) {
	cfg := config.Default()
	container := store.NewContainer()

	svc := service.NewServices(cfg, container)

	tracker := logger.NewTraceTracker(100)
	logger.SetGlobalTraceTracker(tracker)

	ctx := context.Background()

	modelID := "TEST-MODEL-001"
	_, err := svc.Model.Create(ctx, &model.CreateModelRequest{
		ID:       modelID,
		Name:     "Test Model",
		Vendor:   "TestVendor",
		Arch:     "arm64",
		MemoryMB: 512,
		FlashMB:  2048,
	})
	if err != nil {
		t.Fatalf("failed to create model: %v", err)
	}

	firmwareID := "fw-test-001"
	fw, err := svc.Firmware.Create(ctx, &model.CreateFirmwareRequest{
		ModelID:    modelID,
		Version:    "v1.0.0",
		Name:       "Test Firmware",
		MD5:        "d41d8cd98f00b204e9800998ecf8427e",
		Size:       1024,
		FilePath:   "/tmp/fw-test.bin",
		FileName:   "fw-test.bin",
		ReleaseDate: "2025-01-01",
	})
	if err != nil {
		t.Fatalf("failed to create firmware: %v", err)
	}
	firmwareID = fw.ID

	_, err = svc.Firmware.UpdateStatus(ctx, firmwareID, model.FirmwarePublished)
	if err != nil {
		t.Fatalf("failed to publish firmware: %v", err)
	}

	deviceID := "DEV-001"
	_, err = svc.Device.Register(ctx, &model.RegisterDeviceRequest{
		ID:             deviceID,
		ModelID:        modelID,
		Name:           "Test Device",
		CurrentVersion: "v0.9.0",
		IP:             "192.168.1.100",
		Group:          "group-a",
	})
	if err != nil {
		t.Fatalf("failed to register device: %v", err)
	}

	testTraceID := "trace-test-20250101-0001"
	taskCtx := logger.WithTraceID(context.Background(), testTraceID)

	tracker.Clear()

	_, err = svc.Task.Create(taskCtx, &model.CreateTaskRequest{
		Name:           "Test Task",
		ModelID:        modelID,
		FromVersion:    "v0.9.0",
		TargetVersion:  "v1.0.0",
		FirmwareID:     firmwareID,
		Strategy:       model.StrategyFull,
		GrayRatio:      100,
		DeviceIDs:      []string{deviceID},
		GroupFilter:    []string{"group-a"},
		TimeoutSeconds: 3600,
		MaxRetry:       3,
	})
	if err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	snapshot := tracker.Snapshot()

	foundTraceID := false
	for _, r := range snapshot {
		if r.TraceID == testTraceID {
			foundTraceID = true
			break
		}
	}

	if foundTraceID {
		fmt.Println("GREEN（绿灯，缺陷已修复）")
		fmt.Printf("trace_id '%s' 已正确透传至服务层日志，共 %d 条记录包含正确 trace_id\n",
			testTraceID, countMatchingRecords(snapshot, testTraceID))
	} else {
		fmt.Println("RED（红灯，缺陷未修复）")
		fmt.Printf("trace_id '%s' 未能透传至服务层日志，trace 记录中未找到匹配的 trace_id\n",
			testTraceID)
		if len(snapshot) > 0 {
			fmt.Printf("实际 trace 记录数: %d，其中非空 trace_id 数: %d\n",
				len(snapshot), countNonEmptyTraceID(snapshot))
		}
	}

	if !foundTraceID {
		t.Fail()
	}

	tracker.Clear()

	cancelCtx, cancel := context.WithCancel(context.Background())
	cancelCtx = logger.WithTraceID(cancelCtx, "trace-cancel-test")
	cancel()

	time.Sleep(20 * time.Millisecond)

	_, err = svc.Task.Create(cancelCtx, &model.CreateTaskRequest{
		Name:           "Cancel Test Task",
		ModelID:        modelID,
		FromVersion:    "v0.9.0",
		TargetVersion:  "v1.0.0",
		FirmwareID:     firmwareID,
		Strategy:       model.StrategyFull,
		GrayRatio:      100,
		DeviceIDs:      []string{deviceID},
		TimeoutSeconds: 3600,
		MaxRetry:       3,
	})

	contextErrDetected := false
	if err != nil {
		if err == model.ErrContextCanceled || err.Error() == "context canceled" {
			contextErrDetected = true
		}
	}

	if contextErrDetected {
		fmt.Println("GREEN（绿灯，上下文取消正确传播）")
	} else {
		fmt.Println("RED（红灯，上下文取消未能传播）")
		if err != nil {
			fmt.Printf("预期返回 context 取消错误，实际返回: %v\n", err)
		} else {
			fmt.Println("预期返回 context 取消错误，实际返回 nil（操作未被取消）")
		}
	}

	if !contextErrDetected {
		t.Fail()
	}
}

func countMatchingRecords(records []logger.TraceRecord, traceID string) int {
	count := 0
	for _, r := range records {
		if r.TraceID == traceID {
			count++
		}
	}
	return count
}

func countNonEmptyTraceID(records []logger.TraceRecord) int {
	count := 0
	for _, r := range records {
		if r.TraceID != "" {
			count++
		}
	}
	return count
}