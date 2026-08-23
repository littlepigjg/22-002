package firmware_upgrade

import (
	"firmware-upgrade/internal/config"
	"firmware-upgrade/internal/service"
	"firmware-upgrade/internal/store"
)

type Assembly struct {
	Cfg      *config.Config
	Stores   *store.Container
	Services *service.Services
}

func NewAssembly(cfg *config.Config) *Assembly {
	if cfg == nil {
		cfg = config.Default()
	}
	s := store.NewContainer()
	svcs := service.NewServices(cfg, s)
	return &Assembly{Cfg: cfg, Stores: s, Services: svcs}
}
