package api

import (
	"context"
	"fmt"

	"mindfs/server/internal/kanban"
)

// WireRouterTranscriptReader 把会话转录的读端口挂到看板服务上，供 Router 的
// router_read_session 使用（2026-11-19 用户裁定：Router 要有读会话的权力）。
//
// 权限边界两处：
//   - 哪些会话可读由 kanban 侧的 TaskRelatedSessionKeys 决定，这里不做二次判断；
//   - 只透传类型与正文，工具调用/推理/计划块进不了 Router 的上下文。
//
// 与 WireSessionRunningProbe 分开：那个读 StreamHub 的活跃位，这个读磁盘上的块。
func (s *AppContext) WireRouterTranscriptReader(svc *kanban.Service) {
	if svc == nil {
		return
	}
	svc.SetRouterTranscriptReader(routerTranscriptReader{app: s})
}

type routerTranscriptReader struct {
	app *AppContext
}

func (r routerTranscriptReader) ReadSessionBlocks(ctx context.Context, rootID, sessionKey string) ([]kanban.RouterTranscriptBlock, error) {
	if r.app == nil {
		return nil, fmt.Errorf("app context required")
	}
	manager, err := r.app.GetSessionManager(rootID)
	if err != nil {
		return nil, err
	}
	// 走解析后的读口：A+ 的流式片段（Step<0）与旧 revision 不是用户可见正文，
	// 让 Router 看到它们会把 offset=1 翻成半句片段、把同一段回复数两遍。
	blocks, err := manager.ReadResolvedBlocks(ctx, sessionKey)
	if err != nil {
		return nil, err
	}
	out := make([]kanban.RouterTranscriptBlock, 0, len(blocks))
	for _, block := range blocks {
		out = append(out, kanban.RouterTranscriptBlock{Type: string(block.Type), Text: block.Text})
	}
	return out, nil
}

var _ kanban.RouterTranscriptReader = routerTranscriptReader{}
