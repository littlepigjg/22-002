package firmware_upgrade

import (
	"context"
	"fmt"
	"testing"
	"time"

	"firmware-upgrade/internal/config"
	"firmware-upgrade/internal/model"
	"firmware-upgrade/internal/service"
	"firmware-upgrade/internal/store"
)

func TestRedGreen(t *testing.T) {
	container := store.NewContainer()
	cfg := config.Default()
	historySvc := service.NewHistoryService(container.Histories)
	statsSvc := service.NewStatsService(container, historySvc, cfg)
	statsSvc.SetTTL(1)

	ctx := context.Background()
	now := time.Now()

	devices := []*model.Device{
		{ID: "dev-1", ModelID: "model-a", CurrentVersion: "v1.0", Status: model.DeviceStatusOnline, LastHeartbeatAt: now},
		{ID: "dev-2", ModelID: "model-a", CurrentVersion: "v1.0", Status: model.DeviceStatusOnline, LastHeartbeatAt: now},
		{ID: "dev-3", ModelID: "model-b", CurrentVersion: "v2.0", Status: model.DeviceStatusOnline, LastHeartbeatAt: now},
	}
	for _, d := range devices {
		if err := container.Devices.Create(ctx, d); err != nil {
			t.Fatalf("failed to create device %s: %v", d.ID, err)
		}
	}

	firstStats, err := statsSvc.Get(ctx)
	if err != nil {
		t.Fatalf("first Get failed: %v", err)
	}

	origKeys := make([]string, 0)
	origVals := make([]int64, 0)
	for k, v := range firstStats.VersionDistribution {
		origKeys = append(origKeys, k)
		origVals = append(origVals, v)
	}

	statsSvc.Invalidate()

	newDevices := []*model.Device{
		{ID: "dev-4", ModelID: "model-c", CurrentVersion: "v3.0", Status: model.DeviceStatusOnline, LastHeartbeatAt: now},
		{ID: "dev-5", ModelID: "model-c", CurrentVersion: "v3.0", Status: model.DeviceStatusOnline, LastHeartbeatAt: now},
		{ID: "dev-6", ModelID: "model-d", CurrentVersion: "v3.0", Status: model.DeviceStatusOnline, LastHeartbeatAt: now},
	}
	for _, d := range newDevices {
		if err := container.Devices.Create(ctx, d); err != nil {
			t.Fatalf("failed to create new device %s: %v", d.ID, err)
		}
	}

	_, err = statsSvc.Get(ctx)
	if err != nil {
		t.Fatalf("second Get failed: %v", err)
	}

	corrupted := false
	dist := firstStats.VersionDistribution

	if len(dist) != len(origKeys) {
		corrupted = true
	} else {
		for i, k := range origKeys {
			if dist[k] != origVals[i] {
				corrupted = true
				break
			}
		}
		if !corrupted {
			dist2 := firstStats.VersionDistribution
			for k := range dist2 {
				found := false
				for _, ok := range origKeys {
					if ok == k {
						found = true
						break
					}
				}
				if !found {
					corrupted = true
					break
				}
			}
		}
	}

	if corrupted {
		fmt.Println("RED（红灯，缺陷未修复）：version_distribution 在两次请求之间发生了数据污染")
		t.Fail()
	} else {
		fmt.Println("GREEN（绿灯，缺陷已修复）：version_distribution 在两次请求间保持独立")
	}
}
