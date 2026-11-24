package app

import (
	"context"
	"log"
	"time"

	"mindfs/server/internal/api/usecase"
)

// startSessionMaintenanceSweeper 起一拍无损维护巡检：启动立即跑一次，之后按配置间隔。
// 它只修「运行时目录确实不存在且会话空闲」的会话并清死 PID 锁；清图片与删空会话
// 会改变历史，永远只能由人从 HTTP / Router 手动触发。
func startSessionMaintenanceSweeper(
	ctx context.Context,
	svc *usecase.Service,
	listRootIDs func() []string,
	worktree WorktreeOptions,
	opts SessionMaintenanceOptions,
	includeLocks bool,
) {
	if !opts.enabled() || svc == nil {
		return
	}
	go func() {
		runtimeSweep := func() {
			for _, rootID := range listRootIDs() {
				report, err := svc.SweepRuntimeIssues(ctx, rootID)
				if err != nil {
					log.Printf("[session-maintenance] sweep error root=%s err=%v", rootID, err)
					continue
				}
				if report.Repaired > 0 || report.Failed > 0 || report.Skipped > 0 {
					log.Printf("[session-maintenance] sweep root=%s scanned=%d repaired=%d skipped=%d failed=%d",
						rootID, report.Scanned, report.Repaired, report.Skipped, report.Failed)
				}
				for _, detail := range report.Details {
					log.Printf("[session-maintenance] %s", detail)
				}
			}
		}
		lockSweep := func() {
			if includeLocks {
				locks, err := svc.SessionMaintenanceCleanLocks(ctx)
				if err != nil {
					log.Printf("[session-maintenance] clean locks error err=%v", err)
					return
				}
				if len(locks.Removed) > 0 {
					log.Printf("[session-maintenance] clean locks removed=%d", len(locks.Removed))
				}
			}
		}
		runtimeSweep()
		lockSweep()
		runtimeTicker := time.NewTicker(worktree.sweepInterval())
		defer runtimeTicker.Stop()
		lockTicker := time.NewTicker(opts.sweepInterval())
		defer lockTicker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-runtimeTicker.C:
				runtimeSweep()
			case <-lockTicker.C:
				lockSweep()
			}
		}
	}()
}
