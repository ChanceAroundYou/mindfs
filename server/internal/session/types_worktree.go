package session

import (
	"os"
	"strings"
	"time"
)

type RelatedFile struct {
	RootID           string `json:"root_id,omitempty"`
	RepoPath         string `json:"repo_path,omitempty"`
	RepoName         string `json:"repo_name,omitempty"`
	RepoKind         string `json:"repo_kind,omitempty"`
	Path             string `json:"path"`
	Head             string `json:"head,omitempty"`
	Relation         string `json:"relation"`
	CreatedBySession bool   `json:"created_by_session"`
}

type RelatedWorktree struct {
	RootID    string    `json:"root_id"`
	Path      string    `json:"path"`
	Branch    string    `json:"branch,omitempty"`
	Head      string    `json:"head,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SessionAuxFlags 是会话行的内容徽标数据源（G-AD）：ask_user/plan/todo/task/error。
// 与任务行的 kanban.TaskAuxFlags 同名同义，但按会话落盘 —— 无 task_id 的普通聊天
// 会话也有这份数据（任务行 aux 只写给有 task_id 的会话，所以不能复用那张表）。
type SessionAuxFlags struct {
	AskUserWaiting bool   `json:"ask_user_waiting"`
	HasPlan        bool   `json:"has_plan"`
	HasTodos       bool   `json:"has_todos"`
	HasTask        bool   `json:"has_task"`
	SessionError   string `json:"session_error,omitempty"`
}

// SessionAuxFlagsPatch 是 UpdateSessionAuxFlags 的部分更新：nil = 不动。
// 与 kanban.TaskAuxFlagsPatch 同构（跨域同义不同包，usecase 禁直调别域存储，
// 所以镜像一份而不是 import kanban —— 见 layering.md 分层铁律）。
type SessionAuxFlagsPatch struct {
	AskUserWaiting *bool
	HasPlan        *bool
	HasTodos       *bool
	HasTask        *bool
	SessionError   *string
}

// AuxFlagsPayload 是 session.Aux 的列表/广播 payload 形状（G-AD）。
// 三个构造点（sessionListResponse + 两处 session.meta.updated）共用它，
// 字段增减只改一处 —— 以前 related_worktree 三处各写一遍，漏一处就是静默不一致。
func (f SessionAuxFlags) AuxFlagsPayload() map[string]any {
	return map[string]any{
		"ask_user_waiting": f.AskUserWaiting,
		"has_plan":         f.HasPlan,
		"has_todos":        f.HasTodos,
		"has_task":         f.HasTask,
		"session_error":    f.SessionError,
	}
}

// WorktreeMissing 报告会话的 worktree 归属是否已失效（目录被删了）。
//
// 归属路径会事后消失——DELETE /api/git/worktrees、wt-finish.sh cleanup、用户手工 rm，
// 都不是 mindfs 干的，所以没有任何代码清这个字段。失效的后果是会话在 mindfs 里
// 读得到、却发不了消息（agent cwd 指向不存在的目录）。
//
// 刻意不复用 gitview.IsWorktree：它答的是「这是不是 git linked worktree」，
// 主 checkout / 普通目录 / 不存在的路径都返回 false，语义不对。
//
// 这是**派生状态**，不落库：目录重建后下一次调用自动转 false，不需要迁移。
func (wt *RelatedWorktree) WorktreeMissing() bool {
	if wt == nil {
		return false
	}
	p := strings.TrimSpace(wt.Path)
	if p == "" {
		return false
	}
	// 非目录（路径存在但指向文件）同样算失效：agent 的 cwd 必须是目录。
	info, err := os.Stat(p)
	return err != nil || !info.IsDir()
}

type SearchOptions struct {
	Query string
	Limit int
}

type SearchHit struct {
	Key              string     `json:"key"`
	Type             string     `json:"type"`
	ParentSessionKey string     `json:"parent_session_key,omitempty"`
	ParentToolCallID string     `json:"parent_tool_call_id,omitempty"`
	Agent            string     `json:"agent,omitempty"`
	Model            string     `json:"model,omitempty"`
	Shell            string     `json:"shell,omitempty"`
	Name             string     `json:"name"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	ClosedAt         *time.Time `json:"closed_at,omitempty"`
	MatchType        string     `json:"match_type"`
	MatchScore       int        `json:"match_score"`
	Seq              int        `json:"seq"`
	Snippet          string     `json:"snippet,omitempty"`
}
