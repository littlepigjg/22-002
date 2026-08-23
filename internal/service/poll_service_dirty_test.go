// Package service Poll 针对脏执行记录（TaskID 为空）崩溃的回归测试。
package service

import (
	"context"
	"testing"
	"time"

	"firmware-upgrade/internal/config"
	"firmware-upgrade/internal/model"
	"firmware-upgrade/internal/store"
	"firmware-upgrade/pkg/timeutil"
)

// newTestPollService 构建一套真实的存储 + 服务装配，供回归测试使用。
func newTestPollService(t *testing.T) (*PollService, *store.Container) {
	t.Helper()
	cfg := config.Default()
	s := store.NewContainer()
	svc := NewServices(cfg, s)
	return svc.Poll, s
}

func mustCreateDevice(t *testing.T, ds store.DeviceStore, id, modelID, version string) {
	t.Helper()
	d := &model.Device{
		ID:              id,
		ModelID:         modelID,
		CurrentVersion:  version,
		Status:          model.DeviceStatusOnline,
		Group:           "default",
		LastHeartbeatAt: timeutil.Now(),
	}
	if err := ds.Create(context.Background(), d); err != nil {
		t.Fatalf("create device: %v", err)
	}
}

func mustCreateRunningTask(t *testing.T, ts store.UpgradeTaskStore, id, modelID, firmwareID, target string) {
	t.Helper()
	task := &model.UpgradeTask{
		ID:             id,
		Name:           "task-" + id,
		ModelID:        modelID,
		TargetVersion:  target,
		FirmwareID:     firmwareID,
		Strategy:       model.StrategyFull,
		Status:         model.TaskStatusRunning,
		TimeoutSeconds: 60,
		CreatedAt:      timeutil.Now(),
		UpdatedAt:      timeutil.Now(),
	}
	if err := ts.Create(context.Background(), task); err != nil {
		t.Fatalf("create task: %v", err)
	}
}

func mustCreateFirmware(t *testing.T, fs store.FirmwareStore, id, modelID, version string) {
	t.Helper()
	fw := &model.Firmware{
		ID:      id,
		ModelID: modelID,
		Version: version,
		Name:    "fw-" + id,
		MD5:     "d41d8cd98f00b204e9800998ecf8427e",
		Size:    1024,
		Status:  model.FirmwarePublished,
	}
	if err := fs.Create(context.Background(), fw); err != nil {
		t.Fatalf("create firmware: %v", err)
	}
}

// TestPollDirtyEmptyTaskIDRecordDoesNotPanic 复现运维诊断钩子注入脏执行记录
// （TaskID 为空、状态 pending）后同设备 Poll 必现 panic 的场景，验证修复后优雅跳过。
func TestPollDirtyEmptyTaskIDRecordDoesNotPanic(t *testing.T) {
	t.Parallel()
	poll, s := newTestPollService(t)
	ctx := context.Background()

	const devID, modelID, version = "DEV-001", "GW-100", "v1.0.0"
	mustCreateDevice(t, s.Devices, devID, modelID, version)

	// 模拟运维诊断钩子注入脏执行记录：TaskID 为空、状态 pending。
	// 这条记录会被 FindAssignedRunning 当作"已分配运行中"返回，旧逻辑会把空 TaskID
	// 带进 buildResponse 并对 nil 任务做指针解引用 -> panic。
	// Upsert 对空 TaskID 走 orphan 分支，落盘结构与 InsertWithGuard 诊断钩子一致，
	// 都挂在 __orphan__|<deviceID> 键下、状态为 pending/""。
	dirty := &model.TaskDeviceExecution{
		TaskID:     "",
		DeviceID:   devID,
		Status:     model.UpgradeStatusPending,
		Progress:   0,
		AssignedAt: timeutil.Now(),
	}
	if err := s.Execs.Upsert(ctx, dirty); err != nil {
		t.Fatalf("upsert dirty record: %v", err)
	}

	// 此时没有任何 running 任务，Poll 应优雅返回 NeedUpgrade=false 而非 panic。
	res, err := poll.Poll(ctx, &model.PollUpgradeRequest{DeviceID: devID, CurrentVersion: version, ModelID: modelID})
	if err != nil {
		t.Fatalf("Poll returned error on dirty record: %v", err)
	}
	if res == nil {
		t.Fatal("Poll returned nil response on dirty record")
	}
	if res.NeedUpgrade {
		t.Fatalf("expected NeedUpgrade=false on dirty-only record, got true: %+v", res)
	}

	// 再调一次，验证后续轮询仍然稳定（旧逻辑这里必现 crash）。
	res2, err := poll.Poll(ctx, &model.PollUpgradeRequest{DeviceID: devID, CurrentVersion: version, ModelID: modelID})
	if err != nil {
		t.Fatalf("second Poll returned error: %v", err)
	}
	if res2 == nil || res2.NeedUpgrade {
		t.Fatalf("expected stable NeedUpgrade=false on second poll, got: %+v", res2)
	}
}

// TestPollDirtyRecordThenRealTaskAssigned 验证脏记录存在时，若存在可升级任务，
// Poll 跳过脏记录并正常分配真实任务，返回 NeedUpgrade=true。
func TestPollDirtyRecordThenRealTaskAssigned(t *testing.T) {
	t.Parallel()
	poll, s := newTestPollService(t)
	ctx := context.Background()

	const devID, modelID, version = "DEV-002", "GW-200", "v1.0.0"
	mustCreateDevice(t, s.Devices, devID, modelID, version)
	mustCreateFirmware(t, s.Firmwares, "FW-200", modelID, "v2.0.0")
	mustCreateRunningTask(t, s.Tasks, "TASK-200", modelID, "FW-200", "v2.0.0")

	// 先注入脏记录（同 orphan 分支，复现诊断钩子脏数据）。
	dirty := &model.TaskDeviceExecution{
		TaskID:     "",
		DeviceID:   devID,
		Status:     model.UpgradeStatusPending,
		AssignedAt: timeutil.Now(),
	}
	if err := s.Execs.Upsert(ctx, dirty); err != nil {
		t.Fatalf("upsert dirty record: %v", err)
	}

	res, err := poll.Poll(ctx, &model.PollUpgradeRequest{DeviceID: devID, CurrentVersion: version, ModelID: modelID})
	if err != nil {
		t.Fatalf("Poll returned error: %v", err)
	}
	if res == nil {
		t.Fatal("Poll returned nil response")
	}
	if !res.NeedUpgrade {
		t.Fatalf("expected NeedUpgrade=true (skipped dirty record, assigned real task), got: %+v", res)
	}
	if res.TaskID != "TASK-200" {
		t.Fatalf("expected TaskID=TASK-200, got %q", res.TaskID)
	}
	if res.FirmwareID != "FW-200" {
		t.Fatalf("expected FirmwareID=FW-200, got %q", res.FirmwareID)
	}
	if res.DownloadURL == "" {
		t.Fatal("expected non-empty DownloadURL")
	}
}

// TestPollCleanDeviceNoUpgrade 干净设备、无 running 任务 -> NeedUpgrade=false。
func TestPollCleanDeviceNoUpgrade(t *testing.T) {
	t.Parallel()
	poll, s := newTestPollService(t)
	ctx := context.Background()

	const devID, modelID, version = "DEV-003", "GW-300", "v1.0.0"
	mustCreateDevice(t, s.Devices, devID, modelID, version)

	res, err := poll.Poll(ctx, &model.PollUpgradeRequest{DeviceID: devID, CurrentVersion: version, ModelID: modelID})
	if err != nil {
		t.Fatalf("Poll returned error: %v", err)
	}
	if res == nil || res.NeedUpgrade {
		t.Fatalf("expected NeedUpgrade=false on clean device with no task, got: %+v", res)
	}
}

// TestPollCleanDeviceWithRunningTask 干净设备 + running 任务 -> NeedUpgrade=true。
func TestPollCleanDeviceWithRunningTask(t *testing.T) {
	t.Parallel()
	poll, s := newTestPollService(t)
	ctx := context.Background()

	const devID, modelID, version = "DEV-004", "GW-400", "v1.0.0"
	mustCreateDevice(t, s.Devices, devID, modelID, version)
	mustCreateFirmware(t, s.Firmwares, "FW-400", modelID, "v2.0.0")
	mustCreateRunningTask(t, s.Tasks, "TASK-400", modelID, "FW-400", "v2.0.0")

	res, err := poll.Poll(ctx, &model.PollUpgradeRequest{DeviceID: devID, CurrentVersion: version, ModelID: modelID})
	if err != nil {
		t.Fatalf("Poll returned error: %v", err)
	}
	if res == nil || !res.NeedUpgrade {
		t.Fatalf("expected NeedUpgrade=true on clean device with running task, got: %+v", res)
	}
}

// TestBuildResponseEmptyTaskIDDoesNotPanic 直接覆盖 buildResponse 对空/缺失 taskID 的 nil 兜底。
func TestBuildResponseEmptyTaskIDDoesNotPanic(t *testing.T) {
	t.Parallel()
	poll, s := newTestPollService(t)
	ctx := context.Background()

	const devID, modelID = "DEV-005", "GW-500"
	mustCreateDevice(t, s.Devices, devID, modelID, "v1.0.0")
	dev, _ := s.Devices.Get(ctx, devID)

	// 空 taskID：task store 对空 id 返回 (nil,nil)，旧逻辑对 t.FirmwareID 解引用 panic。
	res, err := poll.buildResponse(ctx, "", dev)
	if err != nil {
		t.Fatalf("buildResponse(empty id) returned error: %v", err)
	}
	if res == nil {
		t.Fatal("buildResponse(empty id) returned nil")
	}
	if res.NeedUpgrade {
		t.Fatalf("expected NeedUpgrade=false for empty task id, got true: %+v", res)
	}
}

// TestBuildResponseMissingFirmwareGraceful 任务存在但固件被删 -> 优雅降级 NeedUpgrade=false。
func TestBuildResponseMissingFirmwareGraceful(t *testing.T) {
	t.Parallel()
	poll, s := newTestPollService(t)
	ctx := context.Background()

	const devID, modelID = "DEV-006", "GW-600"
	mustCreateDevice(t, s.Devices, devID, modelID, "v1.0.0")
	// 任务指向一个不存在的固件 ID。
	mustCreateRunningTask(t, s.Tasks, "TASK-600", modelID, "FW-MISSING", "v2.0.0")
	dev, _ := s.Devices.Get(ctx, devID)

	res, err := poll.buildResponse(ctx, "TASK-600", dev)
	if err != nil {
		t.Fatalf("buildResponse(missing firmware) returned error: %v", err)
	}
	if res == nil {
		t.Fatal("buildResponse(missing firmware) returned nil")
	}
	if res.NeedUpgrade {
		t.Fatalf("expected NeedUpgrade=false for missing firmware, got true: %+v", res)
	}
}

// 防止 time 包在某些构建中被误判未使用。
var _ = time.Second
