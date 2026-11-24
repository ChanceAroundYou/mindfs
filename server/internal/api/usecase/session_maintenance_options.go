package usecase

import "mindfs/server/internal/sessionmaintenance"

// maintenanceOptionsProvider 由 api.AppContext 实现。usecase 不依赖 api 包，
// 所以用可选接口取节点本地策略；没有装配时保留安全默认（留备份）。
type maintenanceOptionsProvider interface {
	MaintenanceOptions() sessionmaintenance.Options
}

func maintenanceOptionsFor(registry Registry) sessionmaintenance.Options {
	if provider, ok := registry.(maintenanceOptionsProvider); ok && provider != nil {
		return provider.MaintenanceOptions()
	}
	return sessionmaintenance.Options{}
}
