// Package service 业务逻辑层：装配 service 容器并提供服务。
package service

import (
	"firmware-upgrade/internal/config"
	"firmware-upgrade/internal/store"
)

// Services 全部业务服务容器。
type Services struct {
	Cfg        *config.Config
	Stores     *store.Container
	Model      *ModelService
	Firmware   *FirmwareService
	Device     *DeviceService
	Task       *TaskService
	Gray       *GrayService
	Poll       *PollService
	Progress   *ProgressService
	History    *HistoryService
	Stats      *StatsService
	FileOp     *FileOpService
}

// NewServices 根据配置与存储容器构建全部服务。
func NewServices(cfg *config.Config, s *store.Container) *Services {
	if cfg == nil {
		cfg = config.Default()
	}
	if s == nil {
		s = store.NewContainer()
	}
	svc := &Services{
		Cfg:     cfg,
		Stores:  s,
		Model:   NewModelService(s.Models),
		Device:  NewDeviceService(s.Devices, s.Models),
	}
	svc.FileOp = NewFileOpService(cfg)
	svc.Firmware = NewFirmwareService(s.Firmwares, s.Models, svc.FileOp, cfg)
	svc.History = NewHistoryService(s.Histories)
	svc.Stats = NewStatsService(s, svc.History, cfg)
	svc.Gray = NewGrayService(s.Devices, cfg)
	svc.Task = NewTaskService(s.Tasks, s.Execs, s.Devices, s.Firmwares, s.Models, svc.Gray, svc.History, svc.Stats, cfg)
	svc.Progress = NewProgressService(s.Execs, s.Tasks, s.Devices, s.Histories, svc.Stats, cfg)
	svc.Poll = NewPollService(s.Tasks, s.Execs, s.Devices, s.Firmwares, svc.Gray, svc.Progress, svc.History, cfg)
	return svc
}
