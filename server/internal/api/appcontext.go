package api

import (
	"context"
	"sync"

	"mindfs/server/internal/agent"
	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/api/usecase"
	"mindfs/server/internal/auth"
	"mindfs/server/internal/channels/feishu"
	"mindfs/server/internal/fs"
	"mindfs/server/internal/githubimport"
	"mindfs/server/internal/kanban"
	"mindfs/server/internal/nodes"
	"mindfs/server/internal/notifyscript"
	"mindfs/server/internal/pins"
	"mindfs/server/internal/preferences"
	"mindfs/server/internal/scheduled"
	"mindfs/server/internal/update"
	"mindfs/server/internal/webpush"
)

type AppContext struct {
	Dirs   *fs.Registry
	Agents *agent.Pool
	Prober *agent.Prober
	Update *update.Service
	GitHub *githubimport.Service
	Auth   *auth.Store
	// AccountDir 是本账户私有的配置目录（registry/preferences/nodes/prompts/工作流草稿）。
	// 主账户为空串时，各 store 会落回进程默认目录。
	AccountDir string
	WebPush    *webpush.Service
	Notify     *notifyscript.Service
	Prefs      *preferences.Store
	Nodes      *nodes.Store
	// Pins 是本账户的置顶表（项目置顶 + 会话置顶），**每账户一份**，与 Prefs 相反。
	//
	// 置顶必须按账户分开：你的置顶不该出现在别人的会话栏里。项目置顶原本住在
	// 共享偏好里，那正是它跨账户泄漏的原因。
	// 权威只在主节点 —— 运行节点上这份是空的，控制面端点一律 403。
	Pins      *pins.Store
	Scheduled *scheduled.Service
	Kanban    *kanban.Service
	// Feishu 是外部通道。nil = 本机没配（缺省），所有外发一律跳过；
	// 卡片属于哪个账户由实例装配时固定，不在这里再分账户。
	Feishu *feishu.Service
	// TaskAgent / TaskModel 是新任务派发默认落到的 Agent 与模型（节点本地
	// 配置 task.agent / task.model，由 server/app 归一化出 claude + sonnet）。
	// 空值 = 没配置：选 Agent 退回「探针顺序里第一个可用」的老启发式。
	TaskAgent string
	TaskModel string
	// Hygiene 是会话卫生状态机（超长会话提示 + N 轮后自动压缩）。nil = 没装配，
	// 读口一律返回 disabled，发送路径不做任何观测与触发。
	Hygiene *usecase.HygieneTracker

	mu                       sync.RWMutex
	sessionWorktreeMu        sync.Mutex
	roots                    map[string]*RootContext // root id -> root context
	fileChangeListeners      []func(fs.FileChangeEvent)
	fileChangeBatchListeners []func(fs.FileChangeBatchEvent)
	relatedFileListeners     []func(fs.RelatedFileEvent)
	streamHub                *StreamHub
	candidateRegistry        *usecase.CandidateRegistry
	externalImporters        map[string]agenttypes.ExternalSessionImporter
	// workflowTurnOverride 是 Workflow NodeRun 执行的单测接缝：不启动真实 CLI，
	// 只验证 adapter 选中的 agent/model/session 与结果解析。生产装配恒为 nil。
	workflowTurnOverride func(ctx context.Context, req workflowTurnRequest) (string, error)
	// workflowOutputLLM 是 Router 的结果转换器：执行节点只写自然语言，由它把
	// 原文整理成引擎需要的 outcome/summary。与 Router 共用 agent/model。
	workflowOutputLLM kanban.LLMClient
	// workflowAuthoringOverride 是 Agent 提案生成的单测接缝。它只影响生成文本，
	// 校验、diff、dry-run 仍走生产路径；生产装配恒为 nil。
	workflowAuthoringOverride func(ctx context.Context, req workflowAuthoringRequest) (string, error)
	// routerTools 按任务缓存注册给 CLI 的 Router 工具句柄。Router 会话长期
	// 存活且只注册一次工具，句柄必须按名字路由到当前轮实现（见 routerToolBindings）。
	routerTools map[string]*routerToolBindings
}

// SessionHygiene 把会话卫生状态机交给 usecase（它只持有 Registry，靠可选接口
// 取能力）。没装配时返回 nil，调用方按「关闭」处理。
func (s *AppContext) SessionHygiene() *usecase.HygieneTracker {
	if s == nil {
		return nil
	}
	return s.Hygiene
}

func (s *AppContext) TaskUpdated(rootID string, detail kanban.TaskDetail) {
	s.GetSessionStreamHub().BroadcastAll(WSResponse{
		Type: "task.updated",
		Payload: map[string]any{
			"root_id": rootID,
			"task":    detail.Task,
			"detail":  detail,
		},
	})
}

func (s *AppContext) ReleaseFileWatcher(rootID, sessionKey string) {
	rootCtx, err := s.GetRootContext(rootID)
	if err != nil {
		return
	}

	s.mu.Lock()
	watcher := rootCtx.Watcher
	s.mu.Unlock()
	if watcher == nil {
		return
	}

	watcher.UnregisterSession(sessionKey)
	if watcher.SessionCount() > 0 {
		return
	}

	s.mu.Lock()
	if rootCtx.Watcher == watcher {
		rootCtx.Watcher = nil
	}
	s.mu.Unlock()
	watcher.Close()
}

// StopTaskExecution requests cancellation without releasing the scheduler slot.
// The kanban runner releases it only after SendMessage returns.
func (s *AppContext) TaskDeleted(root, id string) {
	s.GetSessionStreamHub().BroadcastAll(WSResponse{Type: "task.deleted", Payload: map[string]any{"root_id": root, "task_id": id}})
}
