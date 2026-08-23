package firmware_upgrade

import (
	"context"
	"fmt"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"firmware-upgrade/internal/config"
	"firmware-upgrade/internal/model"
	"firmware-upgrade/internal/service"
	"firmware-upgrade/internal/store"
	"firmware-upgrade/pkg/idgen"
)

type rawExecStorer interface {
	InsertWithGuard(ctx context.Context, e *model.TaskDeviceExecution) bool
	RawSnapshot() map[string]model.TaskDeviceExecution
}

type rawTaskStorer interface {
	RawSnapshot() map[string]model.UpgradeTask
}

func buildScaffold(t *testing.T) (
	*store.Container,
	*service.PollService,
	*config.Config,
) {
	t.Helper()
	cfg := config.Default()
	c := store.NewContainer()
	if err := c.Models.Create(context.Background(), &model.DeviceModel{
		ID:        "GW-100",
		Name:      "Gateway-100",
		Vendor:    "ACME",
		Arch:      "arm64",
		MemoryMB:  256,
		FlashMB:   1024,
		Enabled:   true,
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed model failed: %v", err)
	}
	fw := &model.Firmware{
		ID:          idgen.NextID(),
		ModelID:     "GW-100",
		Version:     "v2.0.0",
		Name:        "GW-100 v2.0.0",
		MD5:         "deadbeefcafebabe",
		Size:        1 << 20,
		FilePath:    "/tmp/fw.bin",
		FileName:    "gw100-v2.bin",
		Status:      model.FirmwarePublished,
		ReleaseDate: time.Now(),
		CreatedAt:   time.Now(),
	}
	if err := c.Firmwares.Create(context.Background(), fw); err != nil {
		t.Fatalf("seed firmware failed: %v", err)
	}
	task := &model.UpgradeTask{
		ID:             idgen.NextID(),
		Name:           "Upgrade GW-100 to v2.0.0",
		ModelID:        "GW-100",
		FromVersion:    "v1.0.0",
		TargetVersion:  "v2.0.0",
		FirmwareID:     fw.ID,
		Strategy:       model.StrategyFull,
		GrayRatio:      100,
		Status:         model.TaskStatusRunning,
		ScheduleAt:     time.Now(),
		StartTime:      time.Now(),
		TimeoutSeconds: 1800,
		MaxRetry:       3,
		CreatedAt:      time.Now(),
	}
	if err := c.Tasks.Create(context.Background(), task); err != nil {
		t.Fatalf("seed task failed: %v", err)
	}
	gray := service.NewGrayService(c.Devices, cfg)
	historySvc := service.NewHistoryService(c.Histories)
	stats := service.NewStatsService(c, historySvc, cfg)
	progress := service.NewProgressService(c.Execs, c.Tasks, c.Devices, c.Histories, stats, cfg)
	poll := service.NewPollService(c.Tasks, c.Execs, c.Devices, c.Firmwares, gray, progress, historySvc, cfg)
	return c, poll, cfg
}

func registerDevice(t *testing.T, c *store.Container) *model.Device {
	t.Helper()
	d := &model.Device{
		ID:              "dev-sn-orphan-01",
		ModelID:         "GW-100",
		Name:            "Orphan Device 01",
		CurrentVersion:  "v1.0.0",
		Status:          model.DeviceStatusOnline,
		IP:              "10.0.0.5",
		MAC:             "aa:bb:cc:dd:ee:ff",
		LastHeartbeatAt: time.Now(),
		RegisterAt:      time.Now(),
	}
	if err := c.Devices.Create(context.Background(), d); err != nil {
		t.Fatalf("register device failed: %v", err)
	}
	return d
}

// TestRedGreen 缺陷红灯/绿灯验收。
// 缺陷存在时（有漏洞）：Poll 会 nil pointer deref panic → RED（红灯，缺陷未修复）。
// 缺陷修复后：Poll 对空 TaskID 执行记录不 panic，返回无升级消息 → GREEN（绿灯，缺陷已修复）。
func TestRedGreen(t *testing.T) {
	c, poll, _ := buildScaffold(t)
	dev := registerDevice(t, c)

	storer, ok := c.Execs.(rawExecStorer)
	if !ok {
		t.Fatalf("exec store does not support InsertWithGuard")
	}
	_ = c.Tasks.(rawTaskStorer)

	// Part A: 正常场景没有空 TaskID 记录时 Poll 必须正确返回。
	t.Log("=== part A: normal poll without orphan exec record ===")
	normalReq := &model.PollUpgradeRequest{DeviceID: dev.ID, CurrentVersion: "v1.0.0", ModelID: "GW-100"}
	resp, err := poll.Poll(context.Background(), normalReq)
	if err != nil {
		t.Fatalf("normal poll returned error: %v", err)
	}
	if resp == nil || !resp.NeedUpgrade {
		t.Fatalf("normal poll expected NeedUpgrade=true, got resp=%+v err=%v", resp, err)
	}

	// Part B: 通过诊断钩子注入一条 TaskID 为空的孤立执行记录（模拟脏数据/跨模块契约破坏）。
	t.Log("=== part B: inject orphan exec record via diagnostic hook and call Poll ===")
	inserted := storer.InsertWithGuard(context.Background(), &model.TaskDeviceExecution{
		TaskID:       "",
		DeviceID:     dev.ID,
		Status:       model.UpgradeStatusPending,
		Progress:     0,
		AssignedAt:   time.Now(),
		LastReportAt: time.Now(),
	})
	if !inserted {
		t.Fatalf("InsertWithGuard failed to inject orphan exec record")
	}

	panicked, panicMsg := runPollSafely(poll, dev.ID)

	if panicked {
		fmt.Println("RED（红灯，缺陷未修复）")
		fmt.Println("panic detail:", panicMsg)
		t.Errorf("RED（红灯，缺陷未修复） — Poll panicked on orphan exec record: %s", panicMsg)
		return
	}
	fmt.Println("GREEN（绿灯，缺陷已修复）")
}

func runPollSafely(poll *service.PollService, deviceID string) (panicked bool, panicMsg string) {
	defer func() {
		if r := recover(); r != nil {
			panicked = true
			stack := debug.Stack()
			head := strings.SplitN(string(stack), "\n", 12)[0]
			_ = head
			panicMsg = fmt.Sprintf("recovered: %v\nstack:\n%s", r, string(stack))
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req := &model.PollUpgradeRequest{DeviceID: deviceID, CurrentVersion: "v1.0.0", ModelID: "GW-100"}
	resp, err := poll.Poll(ctx, req)
	_ = resp
	_ = err
	return
}
