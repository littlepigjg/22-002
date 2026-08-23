// Package service —— SelectDevices 设备污染回归测试。
package service

import (
	"context"
	"testing"

	"firmware-upgrade/internal/config"
	"firmware-upgrade/internal/model"
	"firmware-upgrade/internal/store"
	"firmware-upgrade/pkg/timeutil"
)

// TestSelectDevices_DoesNotCorruptStoredDevice 验证 SelectDevices 不会篡改存储中的设备对象。
//
// 回归场景：当任务策略为 device_list 时，SelectDevices 通过 ListByIDs 取到的设备对象是
// 存储底层的指针（未拷贝）。早期实现会在这些对象上直接写 "__prefilter_excluded__" 标记
// 并把 CurrentVersion 改写为 TargetVersion，导致存储被污染——后续 Poll 命中
// "device excluded by group filter" 且来源版本不再匹配。
func TestSelectDevices_DoesNotCorruptStoredDevice(t *testing.T) {
	devStore := store.NewDeviceStore()

	dev := &model.Device{
		ID:             "dev-1",
		ModelID:        "m1",
		Name:           "d1",
		CurrentVersion: "v1.0.0",
		Group:          "production",
		Status:         model.DeviceStatusOnline,
		RegisterAt:     timeutil.Now(),
	}
	if err := devStore.Create(context.Background(), dev); err != nil {
		t.Fatalf("create device: %v", err)
	}

	gray := NewGrayService(devStore, config.Default())

	// 任务：device_list 策略，目标 v2.0.0，分组限制 production，来源版本 v1.0.0。
	task := &model.UpgradeTask{
		ID:            "task-1",
		ModelID:       "m1",
		FromVersion:   "v1.0.0",
		TargetVersion: "v2.0.0",
		Strategy:      model.StrategyDeviceList,
		DeviceIDs:     []string{"dev-1"},
		GroupFilter:   []string{"production"},
	}

	hit, miss, err := gray.SelectDevices(context.Background(), task)
	if err != nil {
		t.Fatalf("SelectDevices: %v", err)
	}
	if len(hit) != 1 || hit[0].ID != "dev-1" {
		t.Fatalf("expected dev-1 hit, got hit=%v miss=%v", hit, miss)
	}
	if len(miss) != 0 {
		t.Fatalf("expected no miss, got %v", miss)
	}

	// 关键断言：存储中的设备字段不得被篡改。
	got, err := devStore.Get(context.Background(), "dev-1")
	if err != nil {
		t.Fatalf("get device: %v", err)
	}
	if got.Group != "production" {
		t.Errorf("stored Group corrupted: got %q want %q", got.Group, "production")
	}
	if got.CurrentVersion != "v1.0.0" {
		t.Errorf("stored CurrentVersion corrupted: got %q want %q", got.CurrentVersion, "v1.0.0")
	}

	// 命中设备对象本身的字段同样不得被改写（避免通过 hit 列表把污染带出去）。
	if hit[0].Group != "production" {
		t.Errorf("hit Group corrupted: got %q want %q", hit[0].Group, "production")
	}
	if hit[0].CurrentVersion != "v1.0.0" {
		t.Errorf("hit CurrentVersion corrupted: got %q want %q", hit[0].CurrentVersion, "v1.0.0")
	}
}

// TestSelectDevices_GroupFilterExcludesWithoutCorrupting 验证分组不匹配的设备被正确剔除，
// 同时其存储对象也不被污染。
func TestSelectDevices_GroupFilterExcludesWithoutCorrupting(t *testing.T) {
	devStore := store.NewDeviceStore()

	devs := []*model.Device{
		{ID: "dev-in", ModelID: "m1", CurrentVersion: "v1.0.0", Group: "production", Status: model.DeviceStatusOnline, RegisterAt: timeutil.Now()},
		{ID: "dev-out", ModelID: "m1", CurrentVersion: "v1.0.0", Group: "staging", Status: model.DeviceStatusOnline, RegisterAt: timeutil.Now()},
	}
	for _, d := range devs {
		if err := devStore.Create(context.Background(), d); err != nil {
			t.Fatalf("create device %s: %v", d.ID, err)
		}
	}

	gray := NewGrayService(devStore, config.Default())
	task := &model.UpgradeTask{
		ID:            "task-1",
		ModelID:       "m1",
		FromVersion:   "v1.0.0",
		TargetVersion: "v2.0.0",
		Strategy:      model.StrategyDeviceList,
		DeviceIDs:     []string{"dev-in", "dev-out"},
		GroupFilter:   []string{"production"},
	}

	hit, miss, err := gray.SelectDevices(context.Background(), task)
	if err != nil {
		t.Fatalf("SelectDevices: %v", err)
	}
	if len(hit) != 1 || hit[0].ID != "dev-in" {
		t.Fatalf("expected only dev-in hit, got hit=%v", hit)
	}
	if len(miss) != 1 || miss[0].ID != "dev-out" {
		t.Fatalf("expected dev-out miss, got miss=%v", miss)
	}

	// 被剔除设备的存储对象不得被污染（原始 bug 会把 dev-out 的 Group 改成 __prefilter_excluded__）。
	gotOut, err := devStore.Get(context.Background(), "dev-out")
	if err != nil {
		t.Fatalf("get dev-out: %v", err)
	}
	if gotOut.Group != "staging" {
		t.Errorf("stored excluded Group corrupted: got %q want %q", gotOut.Group, "staging")
	}
	if gotOut.CurrentVersion != "v1.0.0" {
		t.Errorf("stored excluded CurrentVersion corrupted: got %q want %q", gotOut.CurrentVersion, "v1.0.0")
	}
}
