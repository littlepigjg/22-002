package main

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"firmware-upgrade/internal/config"
	"firmware-upgrade/internal/model"
	"firmware-upgrade/internal/service"
	"firmware-upgrade/internal/store"
)

// TestRedGreen_ErrorPropagation 验证型号创建冲突时的错误传播链。
// 缺陷未修复时：errors.Is(err, model.ErrConflict) 返回 false → 测试失败（RED）
// 缺陷已修复时：errors.Is(err, model.ErrConflict) 返回 true → 测试通过（GREEN）
func TestRedGreen_ErrorPropagation(t *testing.T) {
	cfg := config.Default()
	stores := store.NewContainer()
	svc := service.NewServices(cfg, stores)

	ctx := context.Background()

	// 第一次创建型号 - 应该成功
	req := &model.CreateModelRequest{
		ID:       "test-err-001",
		Name:     "Error Test Model",
		Vendor:   "TestVendor",
		Arch:     "amd64",
		MemoryMB: 256,
		FlashMB:  64,
		Enabled:  boolPtr(true),
	}

	_, err := svc.Model.Create(ctx, req)
	if err != nil {
		t.Fatalf("第一次创建型号失败: %v", err)
	}

	// 第二次创建相同 ID 的型号 - 应该返回 ErrConflict
	_, err = svc.Model.Create(ctx, req)
	if err == nil {
		t.Fatal("第二次创建相同型号应该返回错误，但返回了 nil")
	}

	// 检查错误链是否包含 ErrConflict
	if errors.Is(err, model.ErrConflict) {
		fmt.Println("GREEN（绿灯，缺陷已修复）: errors.Is(err, model.ErrConflict) 返回 true")
		// 缺陷已修复，错误链正确传递
	} else {
		fmt.Println("RED（红灯，缺陷未修复）: errors.Is(err, model.ErrConflict) 返回 false")
		fmt.Printf("  实际错误消息: %s\n", err.Error())
		t.Errorf("错误链断裂：期望 errors.Is(err, model.ErrConflict) 为 true，但为 false。错误消息: %s", err.Error())
	}
}

func boolPtr(b bool) *bool { return &b }
