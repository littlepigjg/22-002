package firmware_upgrade

import (
	"context"
	"fmt"
	"testing"

	"firmware-upgrade/internal/config"
	"firmware-upgrade/internal/model"
	"firmware-upgrade/internal/service"
	"firmware-upgrade/internal/store"
	"firmware-upgrade/pkg/timeutil"
)

func TestRedGreen(t *testing.T) {
	ctx := context.Background()

	container := store.NewContainer()

	task := &model.UpgradeTask{
		ID:             "task-001",
		Name:           "Test Task",
		ModelID:        "model-001",
		TargetVersion:  "v2.0",
		Status:         model.TaskStatusRunning,
		TimeoutSeconds: 1,
		MaxRetry:       3,
		CreatedAt:      timeutil.Now(),
		UpdatedAt:      timeutil.Now(),
	}

	if err := container.Tasks.Create(ctx, task); err != nil {
		t.Log("RED（红灯，缺陷未修复）")
		t.Fatalf("FAIL: failed to create task: %v", err)
	}

	exec := &model.TaskDeviceExecution{
		TaskID:     "task-001",
		DeviceID:   "device-001",
		Status:     model.UpgradeStatusDownloading,
		Progress:   50,
		AssignedAt: timeutil.Now().Add(-2 * 1e9),
	}

	if err := container.Execs.Upsert(ctx, exec); err != nil {
		t.Log("RED（红灯，缺陷未修复）")
		t.Fatalf("FAIL: failed to upsert exec: %v", err)
	}

	cfg := config.Default()
	historySvc := service.NewHistoryService(container.Histories)
	statsSvc := service.NewStatsService(container, historySvc, cfg)
	progressSvc := service.NewProgressService(container.Execs, container.Tasks, container.Devices, container.Histories, statsSvc, cfg)

	defer func() {
		if r := recover(); r != nil {
			t.Log("RED（红灯，缺陷未修复）")
			t.Fatalf("FAIL: panic detected - nil pointer dereference when FindLatestByDevice returns nil history without error: %v", r)
		}
	}()

	result := progressSvc.ScanTimeout(ctx)

	t.Log("GREEN（绿灯，缺陷已修复）")
	fmt.Printf("PASS: ScanTimeout completed successfully without panic, handled %d timeout records\n", result)
}
