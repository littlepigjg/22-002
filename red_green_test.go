package firmware_upgrade_test

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

	for i := 0; i < 20; i++ {
		version := fmt.Sprintf("v%d", i%5)
		modelID := fmt.Sprintf("model-%d", i%4)
		_ = container.Devices.Create(context.Background(), &model.Device{
			ID:              fmt.Sprintf("dev-%d", i),
			CurrentVersion:  version,
			ModelID:         modelID,
			Status:          model.DeviceStatusOnline,
			LastHeartbeatAt: time.Now(),
		})
	}

	historySvc := service.NewHistoryService(container.Histories)
	cfg := config.Default()
	statsSvc := service.NewStatsService(container, historySvc, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Millisecond)
	defer cancel()

	_, err := statsSvc.Get(ctx)

	if err != nil {
		fmt.Println("GREEN (绿灯，缺陷已修复)")
	} else {
		fmt.Println("RED (红灯，缺陷未修复)")
		t.Fail()
	}
}
