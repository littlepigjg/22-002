package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"firmware-upgrade/internal/config"
	"firmware-upgrade/internal/model"
	"firmware-upgrade/internal/service"
	"firmware-upgrade/internal/store"
)

func TestRedGreen(t *testing.T) {
	ctx := context.Background()

	tmpDir, err := os.MkdirTemp("", "firmware-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := config.Default()
	cfg.DataDir = tmpDir
	cfg.FirmwareDir = filepath.Join(tmpDir, "firmwares")

	container := store.NewContainer()
	svc := service.NewServices(cfg, container)

	modelReq := &model.CreateModelRequest{
		ID: "M-TEST", Name: "Test Model", Arch: "arm64",
		MemoryMB: 512, FlashMB: 128,
	}
	_, err = svc.Model.Create(ctx, modelReq)
	if err != nil {
		t.Fatalf("create model failed: %v", err)
	}

	firmwarePath := filepath.Join(tmpDir, "fw-test.bin")
	if err := os.WriteFile(firmwarePath, []byte("test-firmware-data"), 0644); err != nil {
		t.Fatalf("write firmware file failed: %v", err)
	}

	fwReq := &model.CreateFirmwareRequest{
		ModelID: "M-TEST", Version: "v2.0.0", Name: "Test Firmware v2",
		MD5: "d41d8cd98f00b204e9800998ecf8427e",
		Size: 20, FilePath: firmwarePath, FileName: "fw-test.bin",
		CreatedBy: "test",
	}
	fw, err := svc.Firmware.Create(ctx, fwReq)
	if err != nil {
		t.Fatalf("create firmware failed: %v", err)
	}
	_, err = svc.Firmware.UpdateStatus(ctx, fw.ID, model.FirmwarePublished)
	if err != nil {
		t.Fatalf("publish firmware failed: %v", err)
	}

	type devInfo struct {
		id      string
		group   string
		version string
	}
	devices := []devInfo{
		{"D-A-1", "alpha", "v1.0.0"},
		{"D-B-1", "beta", "v1.0.0"},
		{"D-A-2", "alpha", "v1.0.0"},
		{"D-B-2", "beta", "v1.0.0"},
		{"D-A-3", "alpha", "v1.0.0"},
		{"D-B-3", "beta", "v1.0.0"},
		{"D-A-4", "alpha", "v1.0.0"},
		{"D-B-4", "beta", "v1.0.0"},
	}
	alphaSet := make(map[string]bool)
	betaSet := make(map[string]bool)
	allDevIDs := make([]string, 0, len(devices))
	for _, d := range devices {
		_, err := svc.Device.Register(ctx, &model.RegisterDeviceRequest{
			ID: d.id, ModelID: "M-TEST", CurrentVersion: d.version,
			Group: d.group, Name: d.id,
		})
		if err != nil {
			t.Fatalf("register device %s failed: %v", d.id, err)
		}
		if d.group == "alpha" {
			alphaSet[d.id] = true
		} else {
			betaSet[d.id] = true
		}
		allDevIDs = append(allDevIDs, d.id)
	}

	container.Devices.SetPanicGuard(nil)

	task := &model.UpgradeTask{
		ID:            "T-TEST",
		Name:          "Gray Alpha Devices",
		ModelID:       "M-TEST",
		TargetVersion: "v2.0.0",
		FirmwareID:    fw.ID,
		Strategy:      model.StrategyFull,
		GroupFilter:   []string{"alpha"},
		Status:        model.TaskStatusRunning,
		Description:   "test gray release",
	}

	// 直接调用 SelectDevices 验证切片行为
	hit, miss, err := svc.Gray.SelectDevices(ctx, task)
	if err != nil {
		t.Fatalf("SelectDevices failed: %v", err)
	}

	green := true
	var reasons []string

	// 验证命中设备
	if len(hit) != len(alphaSet) {
		green = false
		reasons = append(reasons,
			fmt.Sprintf("hit count mismatch: got %d, expected %d (alpha devices)", len(hit), len(alphaSet)))
	}
	hitSeen := make(map[string]bool)
	for _, d := range hit {
		if hitSeen[d.ID] {
			green = false
			reasons = append(reasons, fmt.Sprintf("duplicate device in hit: %s", d.ID))
			continue
		}
		hitSeen[d.ID] = true
		if d.Group != "alpha" {
			green = false
			reasons = append(reasons,
				fmt.Sprintf("hit device %s has group %q, expected %q", d.ID, d.Group, "alpha"))
		}
		if d.CurrentVersion != "v1.0.0" {
			green = false
			reasons = append(reasons,
				fmt.Sprintf("hit device %s has version %q, expected %q", d.ID, d.CurrentVersion, "v1.0.0"))
		}
	}
	for id := range alphaSet {
		if !hitSeen[id] {
			green = false
			reasons = append(reasons, fmt.Sprintf("alpha device %s missing from hit", id))
		}
	}

	// 验证未命中设备 — 这是检测切片缺陷的关键
	if len(miss) != len(betaSet) {
		green = false
		reasons = append(reasons,
			fmt.Sprintf("miss count mismatch: got %d, expected %d (beta devices)", len(miss), len(betaSet)))
	}
	missSeen := make(map[string]bool)
	for _, d := range miss {
		if missSeen[d.ID] {
			green = false
			reasons = append(reasons, fmt.Sprintf("duplicate device in miss: %s", d.ID))
			continue
		}
		missSeen[d.ID] = true
		if d.Group != "beta" {
			green = false
			reasons = append(reasons,
				fmt.Sprintf("miss device %s has group %q, expected %q — slice backing array corruption detected!",
					d.ID, d.Group, "beta"))
		}
		if d.CurrentVersion != "v1.0.0" {
			green = false
			reasons = append(reasons,
				fmt.Sprintf("miss device %s has version %q, expected %q", d.ID, d.CurrentVersion, "v1.0.0"))
		}
	}
	for id := range betaSet {
		if !missSeen[id] {
			green = false
			reasons = append(reasons, fmt.Sprintf("beta device %s missing from miss (lost due to slice corruption)", id))
		}
	}

	// 验证总设备数
	if len(hit)+len(miss) != len(devices) {
		green = false
		reasons = append(reasons,
			fmt.Sprintf("total device count mismatch: hit=%d + miss=%d = %d, expected %d",
				len(hit), len(miss), len(hit)+len(miss), len(devices)))
	}

	// 验证没有设备同时出现在 hit 和 miss 中
	for id := range hitSeen {
		if missSeen[id] {
			green = false
			reasons = append(reasons, fmt.Sprintf("device %s appears in both hit and miss (slice overlap corruption)", id))
		}
	}

	// 再通过 CreateTask 验证整个流程
	taskReq := &model.CreateTaskRequest{
		Name: "Gray Alpha Devices 2", ModelID: "M-TEST",
		TargetVersion: "v2.0.0", FirmwareID: fw.ID,
		Strategy: model.StrategyFull, GroupFilter: []string{"alpha"},
		ScheduleAt: 0, Description: "test gray release",
	}
	createdTask, err := svc.Task.Create(ctx, taskReq)
	if err != nil {
		t.Fatalf("create task failed: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	execs, err := container.Execs.ListByTask(ctx, createdTask.ID)
	if err != nil {
		t.Fatalf("list executions failed: %v", err)
	}

	rawSnap := container.Devices.RawSnapshot()
	if len(rawSnap) == 0 {
		t.Fatal("raw snapshot is empty")
	}

	if len(execs) != len(alphaSet) {
		green = false
		reasons = append(reasons,
			fmt.Sprintf("execution count mismatch: got %d, expected %d (alpha devices)", len(execs), len(alphaSet)))
	}

	execSeen := make(map[string]bool)
	for _, e := range execs {
		if execSeen[e.DeviceID] {
			green = false
			reasons = append(reasons, fmt.Sprintf("duplicate device in executions: %s", e.DeviceID))
			continue
		}
		execSeen[e.DeviceID] = true

		dev, ok := rawSnap[e.DeviceID]
		if !ok {
			green = false
			reasons = append(reasons, fmt.Sprintf("device %s not found in snapshot", e.DeviceID))
			continue
		}
		if dev.Group != "alpha" {
			green = false
			reasons = append(reasons,
				fmt.Sprintf("device %s has group %q, expected %q", e.DeviceID, dev.Group, "alpha"))
		}
		if dev.CurrentVersion != "v1.0.0" {
			green = false
			reasons = append(reasons,
				fmt.Sprintf("device %s has version %q, expected %q", e.DeviceID, dev.CurrentVersion, "v1.0.0"))
		}
	}

	for id := range alphaSet {
		if !execSeen[id] {
			green = false
			reasons = append(reasons, fmt.Sprintf("alpha device %s missing from executions", id))
		}
	}

	if green {
		fmt.Println("GREEN（绿灯，缺陷已修复）")
	} else {
		fmt.Println("RED（红灯，缺陷未修复）")
		for _, r := range reasons {
			fmt.Println("  - " + r)
		}
		t.FailNow()
	}
}
