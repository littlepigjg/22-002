package service

import (
	"firmware-upgrade/internal/config"
	"firmware-upgrade/internal/store"
)

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

func NewServices(cfg *config.Config, s *store.Container) *Services {
	if s == nil {
		s = store.NewContainer()
	}
	svc := &Services{
		Cfg:    cfg,
		Stores: s,
	}
	if cfg == nil {
		svc.Model = NewModelService(s.Models)
		svc.Device = NewDeviceService(s.Devices, s.Models)
		svc.FileOp = NewFileOpService(nil)
		svc.Firmware = NewFirmwareService(s.Firmwares, s.Models, svc.FileOp, nil)
		svc.History = NewHistoryService(s.Histories)
		svc.Gray = NewGrayService(s.Devices, nil)
		svc.Task = NewTaskService(s.Tasks, s.Execs, s.Devices, s.Firmwares, s.Models, svc.Gray, svc.History, nil, nil)
		svc.Progress = NewProgressService(s.Execs, s.Tasks, s.Devices, s.Histories, nil, nil)
		svc.Poll = NewPollService(s.Tasks, s.Execs, s.Devices, s.Firmwares, svc.Gray, svc.Progress, svc.History, nil)
		return svc
	}
	svc.Model = NewModelService(s.Models)
	svc.Device = NewDeviceService(s.Devices, s.Models)
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
