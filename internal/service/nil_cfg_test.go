package service

import (
	"context"
	"testing"
	"time"

	"firmware-upgrade/internal/model"
	"firmware-upgrade/pkg/timeutil"
)

// 本组测试复现快速启动（NewServices(cfg=nil)）下的稳定性 panic：
// 设备上报「升级成功 progress=100」时服务直接 panic，堆栈指向 service 层缓存失效调用。
//
// 根因：nil cfg 装配分支（services.go）漏装 StatsService，导致 Task/Progress 持有
// 的 stats 为 nil；Report/UpdateStatus/Delete/ScanTimeout 在状态流转末尾调用
// p.stats.Invalidate()/s.stats.Invalidate() 即 nil 指针解引用。正常带 config 构建的
// 服务因装配了 Stats 不受影响。
//
// 修复后这些流程必须跑完且业务结果正确写回存储。

// TestNilCfg_ReportSuccessProgress100 是核心复现路径。
func TestNilCfg_ReportSuccessProgress100(t *testing.T) {
	svc := NewServices(nil, nil)
	if svc.Stats == nil {
		t.Fatal("nil cfg 装配后 Stats 仍为 nil，统计缓存失效调用将 panic")
	}
	ctx := context.Background()

	taskID := seedFixture(t, svc)

	// 上报「升级成功 progress=100」——修复前必 panic（nil stats.Invalidate）。
	exec, err := svc.Progress.Report(ctx, &model.ReportProgressRequest{
		TaskID: taskID, DeviceID: "D-1",
		Status: model.UpgradeStatusSuccess, Progress: 100, MD5Verified: true,
	})
	if err != nil {
		t.Fatalf("report success progress=100: %v", err)
	}
	if exec.Status != model.UpgradeStatusSuccess || exec.Progress != 100 {
		t.Fatalf("exec not persisted as success/100: %+v", exec)
	}

	// 设备版本应回写到 v2.0.0（业务结果落库）。
	gotDev, err := svc.Device.Get(ctx, "D-1")
	if err != nil {
		t.Fatalf("get device: %v", err)
	}
	if gotDev.CurrentVersion != "v2.0.0" {
		t.Fatalf("device version not updated to v2.0.0: got %q", gotDev.CurrentVersion)
	}

	// 统计缓存失效不应阻塞流程，Get 仍可正常重建。
	if _, err := svc.Stats.Get(ctx); err != nil {
		t.Fatalf("stats get after report: %v", err)
	}
}

// TestNilCfg_ReportFailedProgress 覆盖 failed 分支的 Invalidate 调用。
func TestNilCfg_ReportFailedProgress(t *testing.T) {
	svc := NewServices(nil, nil)
	ctx := context.Background()
	taskID := seedFixture(t, svc)

	if _, err := svc.Progress.Report(ctx, &model.ReportProgressRequest{
		TaskID: taskID, DeviceID: "D-1",
		Status: model.UpgradeStatusFailed, Progress: 50, ErrorMessage: "boom",
	}); err != nil {
		t.Fatalf("report failed: %v", err)
	}
}

// TestNilCfg_UpdateStatusCancel 覆盖 UpdateStatus cancel/finish 路径。
func TestNilCfg_UpdateStatusCancel(t *testing.T) {
	svc := NewServices(nil, nil)
	ctx := context.Background()
	taskID := seedFixture(t, svc)

	if _, err := svc.Task.UpdateStatus(ctx, taskID, "cancel", "manual cancel"); err != nil {
		t.Fatalf("update status cancel: %v", err)
	}
	t2, _ := svc.Task.Get(ctx, taskID)
	if t2.Status != model.TaskStatusCanceled {
		t.Fatalf("task not canceled: %s", t2.Status)
	}

	// finish 分支同样会触碰 stats.Invalidate。
	if _, err := svc.Task.UpdateStatus(ctx, taskID, "finish", ""); err != nil {
		t.Fatalf("update status finish: %v", err)
	}
}

// TestNilCfg_Delete 覆盖 Delete 路径。
func TestNilCfg_Delete(t *testing.T) {
	svc := NewServices(nil, nil)
	ctx := context.Background()
	taskID := seedFixture(t, svc)

	if err := svc.Task.Delete(ctx, taskID); err != nil {
		t.Fatalf("delete task: %v", err)
	}
	if _, err := svc.Task.Get(ctx, taskID); err == nil {
		t.Fatalf("task still exists after delete")
	}
}

// TestNilCfg_ScanTimeout 覆盖 ScanTimeout 扫到超时执行记录的路径。
func TestNilCfg_ScanTimeout(t *testing.T) {
	svc := NewServices(nil, nil)
	ctx := context.Background()
	taskID := seedFixture(t, svc)

	// 把执行记录的最近上报时间拨到很久以前，使其超过超时阈值。
	execs := svc.Stores.Execs
	e, err := execs.Get(ctx, taskID, "D-1")
	if err != nil {
		t.Fatalf("get exec: %v", err)
	}
	e.LastReportAt = timeutil.Now().Add(-2 * time.Hour)
	_ = execs.Upsert(ctx, e)

	// 修复前 handled>0 分支会 panic。
	handled := svc.Progress.ScanTimeout(ctx)
	if handled <= 0 {
		t.Fatalf("expected timeout handling, got handled=%d", handled)
	}
	// 超时应标记为 failed。
	e2, _ := execs.Get(ctx, taskID, "D-1")
	if e2.Status != model.UpgradeStatusFailed {
		t.Fatalf("exec not marked failed on timeout: %s", e2.Status)
	}
}

// seedFixture 构造一个 running 态的设备列表任务并返回任务 ID。
// 路径：型号 -> 注册设备 -> 固件（草稿）-> 发布 -> 创建任务（自动分配执行记录/历史）。
// Task.Create 内部会调用 assignInitialExecutions -> invalidateStats，
// 因此本函数本身也覆盖了 nil cfg 下 Create 不崩溃的断言。
func seedFixture(t *testing.T, svc *Services) string {
	t.Helper()
	ctx := context.Background()

	mdl, err := svc.Model.Create(ctx, &model.CreateModelRequest{
		ID: "M-x", Name: "GW-100", Arch: "arm64", Enabled: boolPtr(true),
	})
	if err != nil {
		t.Fatalf("create model: %v", err)
	}

	dev, err := svc.Device.Register(ctx, &model.RegisterDeviceRequest{
		ID: "D-1", ModelID: mdl.ID, Name: "dev1",
		CurrentVersion: "v1.0.0", IP: "10.0.0.1", MAC: "00:11:22:33:44:55",
	})
	if err != nil {
		t.Fatalf("register device: %v", err)
	}
	// 注册时未带目标版本；任务创建校验依赖设备存在即可，目标版本由任务决定。
	_ = dev

	fw, err := svc.Firmware.Create(ctx, &model.CreateFirmwareRequest{
		ModelID:  mdl.ID,
		Version:  "v2.0.0",
		Name:     "fw-v2.0.0",
		MD5:      "0123456789abcdef0123456789abcdef",
		Size:     1024,
		FilePath: "/tmp/fw-v2.0.0.bin",
		FileName: "fw-v2.0.0.bin",
	})
	if err != nil {
		t.Fatalf("create firmware: %v", err)
	}
	if _, err := svc.Firmware.UpdateStatus(ctx, fw.ID, model.FirmwarePublished); err != nil {
		t.Fatalf("publish firmware: %v", err)
	}

	task, err := svc.Task.Create(ctx, &model.CreateTaskRequest{
		Name: "t1", ModelID: mdl.ID, TargetVersion: "v2.0.0", FirmwareID: fw.ID,
		Strategy: model.StrategyDeviceList, DeviceIDs: []string{"D-1"},
		TimeoutSeconds: 60, MaxRetry: 1,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if task.Status != model.TaskStatusRunning {
		t.Fatalf("task not running after create: %s", task.Status)
	}
	return task.ID
}

func boolPtr(v bool) *bool { return &v }
