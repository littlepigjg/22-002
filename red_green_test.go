package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"firmware-upgrade/internal/config"
	"firmware-upgrade/internal/handler"
	"firmware-upgrade/internal/model"
	"firmware-upgrade/internal/service"
	"firmware-upgrade/internal/store"
)

// createTestTaskService 创建测试用的 TaskService 和 TaskHandler
func createTestTaskService() (*service.TaskService, *handler.TaskHandler, *service.ModelService, *service.FirmwareService) {
	cfg := config.Default()
	stores := store.NewContainer()
	modelService := service.NewModelService(stores.Models)
	fileOp := service.NewFileOpService(cfg)
	firmwareService := service.NewFirmwareService(stores.Firmwares, stores.Models, fileOp, cfg)
	historyService := service.NewHistoryService(stores.Histories)
	statsService := service.NewStatsService(stores, historyService, cfg)
	grayService := service.NewGrayService(stores.Devices, cfg)
	taskService := service.NewTaskService(stores.Tasks, stores.Execs, stores.Devices, stores.Firmwares, stores.Models, grayService, historyService, statsService, cfg)
	taskHandler := handler.NewTaskHandler(taskService)
	return taskService, taskHandler, modelService, firmwareService
}

// setupTestData 设置测试数据：创建型号、固件和任务
func setupTestData(ctx context.Context, taskService *service.TaskService, modelService *service.ModelService, firmwareService *service.FirmwareService, modelID string, version string) (*model.UpgradeTask, error) {
	// 创建型号
	_, err := modelService.Create(ctx, &model.CreateModelRequest{
		ID:          modelID,
		Name:        "Test Model " + modelID,
		Description: "test model for defect verification",
		Vendor:      "TestVendor",
		Arch:        "arm64",
		MemoryMB:    512,
		FlashMB:     2048,
		Enabled:     ptrBool(true),
	})
	if err != nil {
		return nil, fmt.Errorf("创建型号失败: %v", err)
	}

	// 创建固件
	_, err = firmwareService.Create(ctx, &model.CreateFirmwareRequest{
		ModelID:        modelID,
		Version:        version,
		Name:           "Test Firmware " + version,
		Description:    "test firmware for defect verification",
		MD5:            "d41d8cd98f00b204e9800998ecf8427e",
		Size:           1024,
		FilePath:       "/tmp/test_firmware.bin",
		FileName:       "firmware.bin",
		CreatedBy:      "tester",
	})
	if err != nil {
		return nil, fmt.Errorf("创建固件失败: %v", err)
	}

	// 发布固件
	_, err = firmwareService.UpdateStatus(ctx, firmwareIDFromModel(ctx, firmwareService, modelID, version), model.FirmwarePublished)
	if err != nil {
		return nil, fmt.Errorf("发布固件失败: %v", err)
	}

	// 创建任务
	now := time.Now()
	req := &model.CreateTaskRequest{
		Name:           "test-task-" + modelID,
		ModelID:        modelID,
		TargetVersion:  version,
		Strategy:       model.StrategyFull,
		GrayRatio:      100,
		TimeoutSeconds: 300,
		MaxRetry:       3,
		Description:    "test task for duplicate cancel",
		CreatedBy:      "tester",
		ScheduleAt:     now.Add(-1 * time.Hour).Unix(),
	}
	task, err := taskService.Create(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("创建任务失败: %v", err)
	}
	return task, nil
}

// ptrBool 辅助函数：创建 bool 指针
func ptrBool(b bool) *bool {
	return &b
}

// firmwareIDFromModel 通过型号和版本查找固件ID
func firmwareIDFromModel(ctx context.Context, firmwareService *service.FirmwareService, modelID, version string) string {
	fw, err := firmwareService.FindByModelAndVersion(ctx, modelID, version)
	if err != nil {
		return ""
	}
	return fw.ID
}

// TestRedGreen 红灯/绿灯测试：验证重复取消任务的错误处理
func TestRedGreen(t *testing.T) {
	fmt.Println("==========================================")
	fmt.Println("缺陷验证测试开始")
	fmt.Println("==========================================")

	ctx := context.Background()
	taskService, taskHandler, modelService, firmwareService := createTestTaskService()

	// 1. 创建任务
	task, err := setupTestData(ctx, taskService, modelService, firmwareService, "test-model-001", "v2.0.0")
	if err != nil {
		t.Fatalf("创建任务失败: %v", err)
	}
	fmt.Printf("任务创建成功: ID=%s, Status=%s\n", task.ID, task.Status)

	// 2. 第一次取消任务
	result1, err1 := taskService.UpdateStatus(ctx, task.ID, "cancel", "first cancel")
	if err1 != nil {
		t.Fatalf("第一次取消任务失败: %v", err1)
	}
	fmt.Printf("第一次取消成功: Status=%s\n", result1.Status)

	// 3. 第二次取消任务 - 应返回 ErrTaskState 错误
	_, err2 := taskService.UpdateStatus(ctx, task.ID, "cancel", "second cancel")
	if err2 == nil {
		t.Error("第二次取消应该返回错误，但返回了 nil")
	}
	fmt.Printf("第二次取消返回错误: %v\n", err2)

	// 4. 验证错误是否为 ErrTaskState 类型（关键验证点）
	isErrTaskState := errors.Is(err2, model.ErrTaskState)
	fmt.Printf("errors.Is(err, model.ErrTaskState) = %v\n", isErrTaskState)

	// 5. 验证错误消息内容
	errMsg := err2.Error()
	containsStateMsg := strings.Contains(errMsg, "task state transition")
	fmt.Printf("错误消息包含 'task state transition': %v\n", containsStateMsg)

	// 6. 通过 HTTP Handler 验证响应码
	// 注意：需要为每次请求创建新的 Request 对象
	// 并使用 SetPathID 在 context 中设置路径 ID
	req := httptest.NewRequest("POST", "/tasks/"+task.ID+"/action", strings.NewReader(`{"action":"cancel","reason":"third cancel"}`))
	w := httptest.NewRecorder()
	ctxWithID := handler.SetPathID(ctx, task.ID)
	req = req.WithContext(ctxWithID)
	taskHandler.Action(w, req)

	httpStatus := w.Code
	fmt.Printf("第三次取消 HTTP 状态码: %d\n", httpStatus)
	fmt.Printf("第三次取消 HTTP 响应: %s\n", w.Body.String())

	// 7. 判定结果
	// 缺陷修复后：errors.Is(err2, model.ErrTaskState) 应该为 true
	// 缺陷存在时：errors.Is(err2, model.ErrTaskState) 为 false，HTTP 状态码为 500
	if isErrTaskState && httpStatus == http.StatusBadRequest {
		fmt.Println("\n==========================================")
		fmt.Println("GREEN（绿灯，缺陷已修复）")
		fmt.Println("==========================================")
		fmt.Println("修复后行为：")
		fmt.Println("  1. errors.Is(err, model.ErrTaskState) = true")
		fmt.Println("  2. HTTP 响应码 = 400 BadRequest")
		fmt.Println("==========================================")
	} else {
		fmt.Println("\n==========================================")
		fmt.Println("RED（红灯，缺陷未修复）")
		fmt.Println("==========================================")
		fmt.Println("缺陷表现：")
		fmt.Printf("  1. errors.Is(err, model.ErrTaskState) = %v（期望 true）\n", isErrTaskState)
		fmt.Printf("  2. HTTP 响应码 = %d（期望 400 BadRequest）\n", httpStatus)
		fmt.Println("  3. TaskService.UpdateStatus 对 ErrTaskState 使用 errors.New 重新包装")
		fmt.Println("  4. 导致 handler.WriteError 无法通过 errors.Is 识别错误类型")
		fmt.Println("  5. 请求走到 default 分支，返回 500 Internal Server Error")
		fmt.Println("==========================================")
		t.Errorf("缺陷未修复：errors.Is=%v, HTTP状态码=%d", isErrTaskState, httpStatus)
	}
}

// TestMultipleStateTransitions 测试多种状态转换场景
func TestMultipleStateTransitions(t *testing.T) {
	fmt.Println("\n==========================================")
	fmt.Println("多状态转换测试")
	fmt.Println("==========================================")

	ctx := context.Background()
	taskService, _, modelService, firmwareService := createTestTaskService()

	testCases := []struct {
		name          string
		modelID       string
		action1       string
		action2       string
		expectIsState bool
	}{
		{"重复取消", "dup-cancel", "cancel", "cancel", true},
		{"已取消后完成", "cancel-finish", "cancel", "finish", true},
		{"已取消后暂停", "cancel-pause", "cancel", "pause", true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// 创建新任务
			task, err := setupTestData(ctx, taskService, modelService, firmwareService, tc.modelID, "v3.0.0")
			if err != nil {
				t.Fatalf("创建任务失败: %v", err)
			}

			// 执行第一次状态变更
			if tc.action1 != "" {
				_, err1 := taskService.UpdateStatus(ctx, task.ID, tc.action1, "")
				if err1 != nil {
					t.Fatalf("%s 失败: %v", tc.action1, err1)
				}
			}

			// 执行第二次状态变更（应失败）
			_, err2 := taskService.UpdateStatus(ctx, task.ID, tc.action2, "")
			if err2 == nil {
				t.Errorf("%s 应该返回错误，但返回了 nil", tc.action2)
			}

			isStateErr := errors.Is(err2, model.ErrTaskState)
			fmt.Printf("  %s: errors.Is(err, ErrTaskState) = %v\n", tc.name, isStateErr)

			if tc.expectIsState && !isStateErr {
				t.Errorf("%s: 期望 errors.Is(err, ErrTaskState) = true，但实际为 false", tc.name)
			}
		})
	}

	fmt.Println("==========================================")
}

// TestErrorPropagation 测试错误传播链
func TestErrorPropagation(t *testing.T) {
	fmt.Println("\n==========================================")
	fmt.Println("错误传播链测试")
	fmt.Println("==========================================")

	ctx := context.Background()
	taskService, _, modelService, firmwareService := createTestTaskService()

	// 创建任务
	task, err := setupTestData(ctx, taskService, modelService, firmwareService, "error-test", "v1.0.0")
	if err != nil {
		t.Fatalf("创建任务失败: %v", err)
	}

	// 第一次取消
	_, err1 := taskService.UpdateStatus(ctx, task.ID, "cancel", "first cancel")
	if err1 != nil {
		t.Fatalf("第一次取消失败: %v", err1)
	}

	// 第二次取消 - 错误传播链测试
	_, err2 := taskService.UpdateStatus(ctx, task.ID, "cancel", "second cancel")
	if err2 == nil {
		t.Fatal("第二次取消应返回错误")
	}

	fmt.Printf("错误链分析:\n")
	fmt.Printf("  err.Error() = %s\n", err2.Error())
	fmt.Printf("  errors.Is(err, ErrTaskState) = %v\n", errors.Is(err2, model.ErrTaskState))

	// 验证错误是简单的 errors.New 创建的（而不是带有 Unwrap 的包装错误）
	var hasUnwrap bool
	type unwrapper interface {
		Unwrap() error
	}
	if _, ok := err2.(unwrapper); ok {
		hasUnwrap = true
	}
	fmt.Printf("  错误具有 Unwrap 方法: %v\n", hasUnwrap)

	if !hasUnwrap && !errors.Is(err2, model.ErrTaskState) {
		fmt.Println("  结论: 错误使用 errors.New 创建，无法通过 errors.Is 识别原始错误类型")
	}

	fmt.Println("==========================================")

	// 标记测试结果
	if errors.Is(err2, model.ErrTaskState) {
		fmt.Println("GREEN：错误传播链正确，errors.Is 可以识别 ErrTaskState")
	} else {
		fmt.Println("RED：错误传播链断裂，errors.Is 无法识别 ErrTaskState（缺陷存在）")
		t.Error("错误传播链断裂：errors.Is 无法识别 ErrTaskState")
	}
}
