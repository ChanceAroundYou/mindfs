package app

import (
	"time"

	"mindfs/server/internal/sessionmaintenance"
)

const (
	defaultSessionMaintenanceIntervalMinutes = 10
	defaultWorktreeSweepIntervalMinutes      = 10
)

func (o WorktreeOptions) autoFinishEnabled() bool {
	return o.AutoFinish == nil || *o.AutoFinish
}

func (o SessionMaintenanceOptions) enabled() bool {
	return o.Enabled == nil || *o.Enabled
}

// sweepInterval 是卡死会话巡检周期；和锁清理周期分开，避免一个键改两件事。
func (o WorktreeOptions) sweepInterval() time.Duration {
	minutes := o.SweepIntervalMinutes
	if minutes <= 0 {
		minutes = defaultWorktreeSweepIntervalMinutes
	}
	return time.Duration(minutes) * time.Minute
}

func (o SessionMaintenanceOptions) sweepInterval() time.Duration {
	minutes := o.SweepIntervalMinutes
	if minutes <= 0 {
		minutes = defaultSessionMaintenanceIntervalMinutes
	}
	return time.Duration(minutes) * time.Minute
}

// maintenanceOptions 把节点配置交给无损维护内核。KeepBackups 为 nil 或 true
// 都保留 .bak，只有显式 false 才不留。
func (o SessionMaintenanceOptions) maintenanceOptions() sessionmaintenance.Options {
	return sessionmaintenance.Options{KeepBackups: o.KeepBackups}
}
