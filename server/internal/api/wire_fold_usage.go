package api

import (
	"context"

	"mindfs/server/internal/agent"
	"mindfs/server/internal/api/usecase"
	"mindfs/server/internal/kanban"
)

// WireFoldUsageReader 把「这一轮 Run 花了多少」的账本读口挂到看板服务上
// （Plan 2026-11-20 Task 13）。
//
// 为什么在 api 层适配而不是让 kanban 直接读账本：账本按**池级**会话键记
// （agent 前缀 + 会话键），而任务里存的只是会话键；补前缀要会话元数据，
// kanban 不依赖 session/agent 包，硬塞进去会把两层搅在一起。
//
// 没挂上、节点跑的是 mode=off、会话根本没折过 —— 都表现为「读不到」，
// 完成记录的 usage 是 null。把这种情况写成 0 会让「没有数」被读成「没花钱」。
func (s *AppContext) WireFoldUsageReader(svc *kanban.Service) {
	if svc == nil {
		return
	}
	// AppContext 满足 usecase.Registry；解析池级会话键的逻辑只写一份，
	// 与胶囊读数（GetSessionContextSaving）共用同一条路径。
	svc.SetFoldUsageReader(foldUsageAdapter{source: &usecase.Service{Registry: s}})
}

// foldUsageSource 是适配层唯一需要的能力（*usecase.Service 满足它）。收窄成
// 接口是为了让字段映射能被单测直接钉住。
type foldUsageSource interface {
	GetSessionFoldUsage(ctx context.Context, rootID, key string) ([]agent.FoldUsageRow, error)
}

type foldUsageAdapter struct {
	source foldUsageSource
}

var _ kanban.FoldUsageReader = foldUsageAdapter{}

func (a foldUsageAdapter) SessionFoldUsage(ctx context.Context, rootID, sessionKey string) ([]kanban.FoldUsageSample, error) {
	rows, err := a.source.GetSessionFoldUsage(ctx, rootID, sessionKey)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	samples := make([]kanban.FoldUsageSample, 0, len(rows))
	for _, row := range rows {
		samples = append(samples, kanban.FoldUsageSample{
			At:      row.At,
			Input:   row.Input,
			Output:  row.Output,
			Cached:  row.Cached,
			Missing: row.Missing,
		})
	}
	return samples, nil
}
