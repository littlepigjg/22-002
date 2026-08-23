// Package store 统一的存储容器，用于一次性装配所有子存储。
package store

// Container 聚合所有存储实现，便于 Service 层装配。
type Container struct {
	Models    DeviceModelStore
	Firmwares FirmwareStore
	Devices   DeviceStore
	Tasks     UpgradeTaskStore
	Execs     TaskExecStore
	Histories UpgradeHistoryStore
}

// NewContainer 创建存储容器（内存实现）。
func NewContainer() *Container {
	return &Container{
		Models:    NewDeviceModelStore(),
		Firmwares: NewFirmwareStore(),
		Devices:   NewDeviceStore(),
		Tasks:     NewUpgradeTaskStore(),
		Execs:     NewTaskExecStore(),
		Histories: NewUpgradeHistoryStore(),
	}
}
