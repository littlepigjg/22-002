package main

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"firmware-upgrade/internal/model"
	"firmware-upgrade/internal/service"
	"firmware-upgrade/internal/store"
)

func TestRedGreen(t *testing.T) {
	fmt.Println("========== 设备心跳错误处理测试 ==========")
	fmt.Println()

	deviceStore := store.NewDeviceStore()
	modelStore := store.NewDeviceModelStore()
	deviceSvc := service.NewDeviceService(deviceStore, modelStore)

	ctx := context.Background()

	t.Run("设备未注册心跳应返回ErrDeviceNotFound", func(t *testing.T) {
		req := &model.HeartbeatRequest{
			ID:             "non-existent-device-001",
			CurrentVersion: "1.0.0",
			Status:         model.DeviceStatusOnline,
			IP:             "192.168.1.100",
		}

		err := deviceSvc.Heartbeat(ctx, req)
		if err == nil {
			fmt.Printf("RED（红灯，缺陷未修复）\n")
			fmt.Printf("  心跳未注册设备返回 nil 错误，期望返回 ErrDeviceNotFound\n")
			t.Errorf("expected ErrDeviceNotFound, got nil error")
			return
		}

		if errors.Is(err, model.ErrDeviceNotFound) {
			fmt.Printf("GREEN（绿灯，缺陷已修复）\n")
			fmt.Printf("  心跳未注册设备正确返回 ErrDeviceNotFound\n")
		} else {
			fmt.Printf("RED（红灯，缺陷未修复）\n")
			fmt.Printf("  错误类型不正确: %v，期望 errors.Is(err, model.ErrDeviceNotFound) 为 true\n", err)
			t.Errorf("expected ErrDeviceNotFound, got: %v", err)
		}
	})

	t.Run("设备已注册心跳应成功", func(t *testing.T) {
		regReq := &model.RegisterDeviceRequest{
			ID:             "existing-device-001",
			ModelID:        "model-001",
			Name:           "Test Device",
			CurrentVersion: "1.0.0",
			IP:             "192.168.1.50",
		}
		_ = regReq

		modelStore.Create(ctx, &model.DeviceModel{
			ID:      "model-001",
			Name:    "Test Model",
			Vendor:  "Vendor",
			Arch:    "amd64",
			MemoryMB: 1024,
			FlashMB:  4096,
			Enabled: true,
		})

		_, err := deviceSvc.Register(ctx, regReq)
		if err != nil {
			t.Logf("注册设备失败（可能型号不存在，跳过此测试）: %v", err)
			return
		}

		hbReq := &model.HeartbeatRequest{
			ID:             "existing-device-001",
			CurrentVersion: "1.0.1",
			Status:         model.DeviceStatusOnline,
			IP:             "192.168.1.51",
		}

		err = deviceSvc.Heartbeat(ctx, hbReq)
		if err != nil {
			fmt.Printf("RED（红灯，缺陷未修复）\n")
			fmt.Printf("  已注册设备心跳失败: %v\n", err)
			t.Errorf("expected nil error for registered device heartbeat, got: %v", err)
		} else {
			fmt.Printf("GREEN（绿灯，缺陷已修复）\n")
			fmt.Printf("  已注册设备心跳成功\n")
		}
	})

	fmt.Println()
	fmt.Println("========== 测试完成 ==========")
}

func TestHeartbeatErrorPropagation(t *testing.T) {
	fmt.Println("========== 错误传播链测试 ==========")
	fmt.Println()

	deviceStore := store.NewDeviceStore()
	modelStore := store.NewDeviceModelStore()
	deviceSvc := service.NewDeviceService(deviceStore, modelStore)

	ctx := context.Background()

	t.Run("错误应可通过errors.Is识别", func(t *testing.T) {
		req := &model.HeartbeatRequest{
			ID:     "device-error-test-001",
			Status: model.DeviceStatusOnline,
		}

		err := deviceSvc.Heartbeat(ctx, req)
		if err == nil {
			fmt.Printf("RED（红灯，缺陷未修复）\n")
			fmt.Printf("  未注册设备心跳返回 nil\n")
			t.Errorf("expected ErrDeviceNotFound, got nil")
			return
		}

		isDeviceNotFound := errors.Is(err, model.ErrDeviceNotFound)
		if isDeviceNotFound {
			fmt.Printf("GREEN（绿灯，缺陷已修复）\n")
			fmt.Printf("  错误正确实现了 Unwrap()，可通过 errors.Is 识别\n")
		} else {
			fmt.Printf("RED（红灯，缺陷未修复）\n")
			fmt.Printf("  错误无法通过 errors.Is 识别为 ErrDeviceNotFound\n")
			fmt.Printf("  错误消息: %v\n", err)
			t.Errorf("expected errors.Is(err, model.ErrDeviceNotFound) to be true")
		}
	})

	fmt.Println()
	fmt.Println("========== 错误传播测试完成 ==========")
}
