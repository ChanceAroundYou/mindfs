package api

import "mindfs/server/internal/sessionmaintenance"

// SetMaintenanceOptions 由装配期写入节点本地的会话维护策略。
// 未调用时零值仍然保留 .bak，符合「只做无损维护」的默认。
func (s *AppContext) SetMaintenanceOptions(opts sessionmaintenance.Options) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.maintenance = opts
	s.mu.Unlock()
}

// MaintenanceOptions 供 usecase 通过可选接口读取。
func (s *AppContext) MaintenanceOptions() sessionmaintenance.Options {
	if s == nil {
		return sessionmaintenance.Options{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.maintenance
}
