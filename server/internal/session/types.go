package session

import (
	"time"

	agenttypes "mindfs/server/internal/agent/types"
)

const (
	TypeChat    = "chat"
	TypeView    = "view"
	TypeCommand = "command"
)

// Exchange.Source 取值：谁写的这一行。
// 实时路径（MindFS 自己在驱动会话）写 live，转录导入器写 import；老数据为空。
// 写入来源用于会话所有权与同步逻辑；它不改变 GET 返回的持久化行。
const (
	ExchangeSourceLive   = "live"
	ExchangeSourceImport = "import"
)

type Session struct {
	Key              string           `json:"key"`
	Type             string           `json:"type"`
	ParentSessionKey string           `json:"parent_session_key,omitempty"`
	ParentToolCallID string           `json:"parent_tool_call_id,omitempty"`
	Source           string           `json:"source,omitempty"`
	TaskID           string           `json:"task_id,omitempty"`
	AgentCtxSeq      map[string]int   `json:"agent_ctx_seq,omitempty"`
	Model            string           `json:"model,omitempty"`
	Shell            string           `json:"shell,omitempty"`
	PlanMode         bool             `json:"plan_mode,omitempty"`
	Name             string           `json:"name"`
	Exchanges        []Exchange       `json:"exchanges"`
	RelatedFiles     []RelatedFile    `json:"related_files"`
	RelatedWorktree  *RelatedWorktree `json:"related_worktree,omitempty"`
	// WorkDir 是这个会话运行时的工作目录（cwd）。空 = 项目主 checkout。
	//
	// 与 RelatedWorktree 的区别是刻意的：RelatedWorktree 表达「这个会话属于哪个工作树」
	// （任务收尾会清它、UI 按它显示工作树徽标），WorkDir 表达「它在哪个目录里干活」——
	// 用户在标题栏菜单里手选一个目录时，只该改后者。两者混用会让一次纯换目录的操作
	// 触发工作树收尾流程、并在会话上亮出一个并不存在的工作树徽标。
	WorkDir string `json:"work_dir,omitempty"`
	// Aux 是会话行的内容徽标数据源（G-AD）：ask_user/plan/todo/task/error。
	// 与任务行的 kanban.TaskAuxFlags 同名同义，但按会话落盘 —— 无 task_id 的
	// 普通聊天会话也有这份数据（任务行 aux 只写给有 task_id 的会话）。
	Aux               SessionAuxFlags          `json:"aux_flags"`
	LastContextWindow agenttypes.ContextWindow `json:"last_context_window,omitempty"`
	PinnedAt          *time.Time               `json:"pinned_at,omitempty"`
	// ArchivedAt 非空 = 已归档。归档只是从主面板隐去，正文/链接都还在（能深链接打开、
	// 搜索搜得到），只有删除才真正清内容。与 PinnedAt 同构：NULL = 未归档。
	ArchivedAt *time.Time `json:"archived_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	ClosedAt   *time.Time `json:"closed_at,omitempty"`
}
