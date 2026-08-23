package main

import (
	"context"
	"fmt"
	"os"
	"testing"

	"firmware-upgrade/internal/config"
	"firmware-upgrade/internal/model"
	"firmware-upgrade/internal/service"
	"firmware-upgrade/internal/store"
	"firmware-upgrade/pkg/timeutil"
)

func TestRedGreen(t *testing.T) {
	ctx := context.Background()
	cfg := config.Default()

	container := store.NewContainer()

	// 创建任务
	task := &model.UpgradeTask{
		ID:            "task-001",
		Name:          "test-task",
		ModelID:       "model-001",
		TargetVersion: "v2.0.0",
		FirmwareID:    "fw-001",
		Strategy:      model.StrategyFull,
		Status:        model.TaskStatusRunning,
		MaxRetry:      3,
		Progress:      model.TaskProgress{},
		CreatedAt:     timeutil.Now(),
		UpdatedAt:     timeutil.Now(),
	}
	if err := container.Tasks.Create(ctx, task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	// 创建设备
	device := &model.Device{
		ID:             "device-001",
		ModelID:        "model-001",
		Name:           "test-device",
		CurrentVersion: "v1.0.0",
		Status:         model.DeviceStatusOnline,
		Group:          "group-1",
		Tags:           []string{"tag1"},
		LastHeartbeatAt: timeutil.Now(),
		RegisterAt:     timeutil.Now(),
		UpdatedAt:      timeutil.Now(),
	}
	if err := container.Devices.Create(ctx, device); err != nil {
		t.Fatalf("failed to create device: %v", err)
	}

	// 创建执行记录
	exec := &model.TaskDeviceExecution{
		TaskID:     "task-001",
		DeviceID:   "device-001",
		Status:     model.UpgradeStatusPending,
		Progress:   0,
		AssignedAt: timeutil.Now(),
	}
	if err := container.Execs.Upsert(ctx, exec); err != nil {
		t.Fatalf("failed to create exec: %v", err)
	}

	// 创建必要的服务
	historySvc := service.NewHistoryService(container.Histories)
	statsSvc := service.NewStatsService(container, historySvc, cfg)
	progressSvc := service.NewProgressService(container.Execs, container.Tasks, container.Devices, container.Histories, statsSvc, cfg)

	// 设置重试回调来收集错误
	progressSvc.SetRetryOnAttempt(func(attempt int, err error) {
		// 记录回调被调用的次数
	})

	// 清空错误历史
	progressSvc.ClearRetryErrorHistory()

	// 关键测试：不创建历史记录，使 FindLatestByDevice 返回错误
	// 这样 retry.Do 会重试 3 次，每次都失败
	// 由于缺陷，只有最后一次的错误会被收集

	// 调用 Report 方法，此时 updateHistory 会失败（因为没有历史记录）
	req := &model.ReportProgressRequest{
		TaskID:       "task-001",
		DeviceID:     "device-001",
		Status:       model.UpgradeStatusFailed,
		Progress:     50,
		ErrorMessage: "test error",
	}

	_, err := progressSvc.Report(ctx, req)

	// 验证：由于 updateHistory 最终失败，Report 应该返回错误
	// 但这不是关键，关键是验证收集到的错误数量
	_ = err

	// 获取收集到的错误历史
	errHistory := progressSvc.GetRetryErrorHistory()

	// 预期：3 次重试应该收集 3 个错误
	// 但由于缺陷（OnAttempt 只在最后一次重试时触发），实际只收集到 1 个错误
	expectedErrors := 3
	actualErrors := len(errHistory)

	if actualErrors == expectedErrors {
		// 缺陷已修复
		fmt.Println("GREEN（绿灯，缺陷已修复）")
		os.Exit(0)
	} else {
		// 缺陷存在
		fmt.Printf("RED（红灯，缺陷未修复）\n")
		fmt.Printf("预期收集 %d 个错误，实际收集 %d 个错误\n", expectedErrors, actualErrors)
		t.Errorf("expected %d errors in history, got %d", expectedErrors, actualErrors)
		os.Exit(1)
	}
}
