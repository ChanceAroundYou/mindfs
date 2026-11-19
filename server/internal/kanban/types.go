package kanban

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"
)

const (
	RoleUser  = "user"
	RoleAgent = "agent"

	// 任务状态对齐全局状态列：待开始 / 进行中 / 等待你 / 已完成 / 失败·取消。
	// 旧数据中的 queued/paused 在迁移时分别归入 pending/running。
	StatusPending     = "pending"
	StatusRunning     = "running"
	StatusWaitingUser = "waiting_user"
	StatusPaused      = "paused"
	StatusSuccess     = "success"
	StatusFail        = "fail"
	StatusCancelled   = "cancelled"

	// 会话复用策略（workflow Agent 节点）。任务本身不再有阶段，
	// 这些取值只由节点定义消费。
	SessionReuseTaskMain  = "task_main"
	SessionReuseSameStage = "same_stage"
	SessionReuseAlwaysNew = "always_new"
	// SessionReusePerNode 是 workflow Agent 节点的默认（D2）：每个节点实例
	// 自己的 session，只有显式 task_main 才复用任务主会话。
	SessionReusePerNode = "per_node"
)

// Task 是看板上的一张卡。任务没有阶段：一次用户留言 = 一轮对话，
// 由 Router（ActivityService）决定这一轮跑工作流还是跑普通会话。
type Task struct {
	ID         string `json:"id"`
	TaskNumber int    `json:"task_number"`
	RootID     string `json:"root_id"`
	Name       string `json:"name,omitempty"`
	// TaskTemplateID / TaskTemplateName 是**冻结列**：模板功能已下线（Phase 3），
	// 不再写入，也不再回查任何模板库；只为历史任务保留来源标签的只读展示。
	TaskTemplateID     string `json:"task_template_id,omitempty"`
	TaskTemplateName   string `json:"task_template_name,omitempty"`
	CreateWorktree     bool   `json:"create_worktree"`
	WorktreeBranchMode string `json:"worktree_branch_mode,omitempty"`
	WorktreeBranch     string `json:"worktree_branch,omitempty"`
	Status             string `json:"status"`
	MainSessionKey     string `json:"main_session_key,omitempty"`
	// ResultSummary 是最近一轮结算后的可读结果（成功摘要是节点汇报，失败/等待
	// 是原因）。看板卡片用它回答「这个任务最后做到了什么」；没有它，卡片只剩
	// 第一条用户留言，任务列表看起来和对话开头毫无区别。
	ResultSummary  string `json:"result_summary,omitempty"`
	WorktreeRootID string `json:"worktree_root_id,omitempty"`
	WorktreePath   string `json:"worktree_path,omitempty"`
	// WorktreeBuilt 报告这个任务**曾经建出过一个 worktree**。
	//
	// 为什么要有它：WorktreePath 为空有两种相反的含义 ——
	// 「还没建」（首段还是 user 段）和「建过、记录被清掉了」。而客户端只看到
	// 「路径为空」，没有别的列能把这两种分开，于是会把「目录还在被人用」的情况
	// 显示成「已收尾」。2026-10-01 实测就是这个：repoint 顺手清了任务的归属，
	// 一个仍在使用的 worktree 被标成活已经并回主干。
	//
	// 清归属时**不**清这一列（见 TaskStore.ClearWorktreeRefs）：拆掉目录不等于
	// 没建过。存量数据默认 false —— 判据宁保守，也不要把没证据的任务说成收过尾。
	WorktreeBuilt bool         `json:"worktree_built"`
	Labels        []string     `json:"labels"`
	CreatedAt     time.Time    `json:"created_at"`
	UpdatedAt     time.Time    `json:"updated_at"`
	CompletedAt   string       `json:"completed_at,omitempty"`
	// ArchivedAt 非空 = 已归档。归档只是任务自己的标记：卡片从工作台隐藏、留在
	// 看板「已结束 → 归档」子组；级联到相关会话时同样只打标记，绝不删后代会话。
	// 取消归档只清这一列，不会反过来解开会话的归档标记。
	ArchivedAt string       `json:"archived_at,omitempty"`
	AuxFlags   TaskAuxFlags `json:"aux_flags"`
	// WorktreeMissing 是**派生**字段，不落库：序列化时按 WorktreePath 此刻是否还是
	// 目录算出来。worktree 目录会事后被删（DELETE /api/git/worktrees、
	// wt-finish.sh cleanup、手工 rm），而 WorktreePath 全仓只有 ensureTaskWorktree
	// 一处写入、没人负责清 —— 派生比存字段可靠，重建后自动转回 false，不需要迁移。
	//
	// 刻意不落库还有个理由：finishTask 保留 worktree_path 是刻意的（任务收尾不该
	// 依赖 worktree 还在），终态任务带着失效路径是正常状态，客户端需要知道自己
	// 「这个路径已经不能用了」。
	WorktreeMissing bool `json:"worktree_missing"`
}

// WorktreeMissingNow 报告任务记录的 worktree 路径此刻是否还能当 agent cwd 用。
//
// 派生值，不落库：worktree 目录会事后被删（DELETE /api/git/worktrees、
// wt-finish.sh cleanup、手工 rm），而 WorktreePath 全仓只有 ensureTaskWorktree
// 一处写入、没人负责清。重建后自动转回 false，不需要数据迁移。
//
// 「路径为空」不算失效：worktree 是执行到 agent 段时才建的，任务刚建出来、
// 或者首段还是 user 段时路径本来就该是空的，那不是失效，是还没建。
func (t Task) WorktreeMissingNow() bool {
	if !t.CreateWorktree {
		return false
	}
	if strings.TrimSpace(t.WorktreePath) == "" {
		return false
	}
	return !worktreeDirUsable(t.WorktreePath)
}

// worktreeDirUsable 报告一个路径此刻是否还能当 agent 的 cwd 用。
//
// 为什么要 stat 而不是看字段非空：WorktreePath 全仓只有 ensureTaskWorktree 一处写入，
// 而目录可能事后被 DELETE /api/git/worktrees、wt-finish.sh cleanup 或用户手工删掉，
// 没有任何代码清这个字段。拿「非空」当「存在」会让失效路径一路流到 agent 启动，
// 用户看到的是 chdir 报错而不是「worktree 被删了」。
//
// 非目录（文件 / 符号链接指到文件）同样判为不可用：cwd 必须是目录。
func worktreeDirUsable(path string) bool {
	p := strings.TrimSpace(path)
	if p == "" {
		return false
	}
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// MarshalJSON 在标准字段之外补上派生的 worktree_missing。
//
// 走这条路而不是在每个 handler 里手工加字段：Task 从不被整体序列化进 SQLite
// （task_store.go 是逐字段写列的），所以自定义 marshal 不会影响持久化；
// 而任务响应的出口很多（详情 / 列表 / overview / WS 广播 / 各动词回包），
// 逐个加必然漏。
func (t Task) MarshalJSON() ([]byte, error) {
	type alias Task // 去掉方法，避免无限递归
	out := alias(t)
	out.WorktreeMissing = t.WorktreeMissingNow()
	return json.Marshal(out)
}

type TaskAuxFlags struct {
	AskUserWaiting bool   `json:"ask_user_waiting"`
	HasPlan        bool   `json:"has_plan"`
	HasTodos       bool   `json:"has_todos"`
	HasTask        bool   `json:"has_task"`
	SessionError   string `json:"session_error,omitempty"`
}

type TaskAuxFlagsPatch struct {
	AskUserWaiting *bool
	HasPlan        *bool
	HasTodos       *bool
	HasTask        *bool
	SessionError   *string
}

type TaskEvent struct {
	ID        string    `json:"id"`
	TaskID    string    `json:"task_id"`
	Type      string    `json:"type"`
	Payload   string    `json:"payload_json,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type TaskDetail struct {
	Task   Task        `json:"task"`
	Events []TaskEvent `json:"events"`
}

type WorktreeInfo struct {
	RootID string
	Path   string
}

// TaskActivity / ContinueRequest —— WP-01 留言板后端（spec 2026-11-09）。
//
// TaskActivity 是任务对话时间线的一条 typed 记录；ContinueRequest 是每条留言
// 触发的幂等执行请求。
const (
	ActivityTypeUserComment = "user_comment"
	ActivityTypeAgentReply  = "agent_reply"
	ActivityTypeSystemEvent = "system_event"
	ActivityTypeAttention   = "attention"
	// ActivityTypeUserMessage 是对话式模型（Phase 2）的用户留言类型。
	// 存量 user_comment 行仍原样保留，读取端必须同时接受两者。
	ActivityTypeUserMessage = "user_message"
	// ActivityTypeQuestion 是任务对话里的 Agent 提问卡（ask_user）。
	// PayloadJSON 存 questions/agent；ToolUseID 关联会话里的同一条 tool call，
	// 回答时两处必须同步。
	ActivityTypeQuestion = "question"
	// ActivityTypeHumanGate 是 HumanGate 的审批卡。PayloadJSON 存
	// prompt/node_id/node_run_id；卡片本身就是审批入口，普通留言不能批准。
	ActivityTypeHumanGate = "human_gate"
)

// 对话式 Activity 的执行状态（spec 2026-11-12 §3.1.1）。
// 用户留言与系统事件没有状态，字段留空。
const (
	ActivityStatusPending   = "pending"
	ActivityStatusRunning   = "running"
	ActivityStatusCompleted = "completed"
	ActivityStatusFailed    = "failed"
	ActivityStatusCancelled = "cancelled"
	// ActivityStatusWaitingUser 表示这一轮回复停在需要用户处理的地方
	// （HumanGate、条件/输出无法判定）。它是「这一轮还没结束」，与 failed 不同：
	// 用户批准/补充后同一个 Run 会继续，最终仍会落到 completed/failed。
	ActivityStatusWaitingUser = "waiting_user"
	// ActivityStatusQueued 只属于**用户留言**：新建任务时勾掉「立即执行」，
	// 首条留言先排队，等用户点「立即执行」（RunNow）再交给 Router。
	// Agent 回复不会使用这个状态。
	ActivityStatusQueued = "queued"
)

const (
	CommentWorkflowChoiceSpecified = "specified"
	CommentWorkflowChoiceDisabled  = "disabled"
	CommentWorkflowChoiceEmpty     = "empty"
)

const (
	ContinueRequestQueued   = "queued"
	ContinueRequestBlocked  = "blocked"
	ContinueRequestConsumed = "consumed"
	// ContinueRequestPendingPhase：留言到达时该任务已绑定 WorkflowRun，但当前
	// 没有任何活动的 Agent NodeRun。D3 规定这种留言开启新的执行阶段；新阶段的
	// 触发不在 comment binding 这一包里实现，所以先显式停在 pending_phase，
	// 绝不自动改投到之后启动的节点。
	ContinueRequestPendingPhase = "pending_phase"
	// ContinueRequestDeliveryFailed：续聊发送明确失败。保留原留言，不自动重放。
	ContinueRequestDeliveryFailed = "delivery_failed"
)

type TaskActivity struct {
	ID                string `json:"id"`
	TaskID            string `json:"task_id"`
	RootID            string `json:"root_id,omitempty"`
	Type              string `json:"type"`
	Author            string `json:"author"`
	Role              string `json:"role"`
	Body              string `json:"body"`
	WorkflowChoice    string `json:"workflow_choice"`
	WorkflowVersionID string `json:"workflow_version_id,omitempty"`
	// RunID/NodeRunID 关联 workflow 运行证据；legacy 留言与 WP-03 行保持空串。
	// 对话式模型里 RunID 就是计划所称的 workflow_run_id：同一关系不设第二列。
	RunID     string `json:"run_id,omitempty"`
	NodeRunID string `json:"node_run_id,omitempty"`
	// 对话式模型（Phase 2）字段。用户留言/系统事件不写 Status/Summary。
	Status     string `json:"status,omitempty"`
	Summary    string `json:"summary,omitempty"`
	SessionKey string `json:"session_key,omitempty"`
	// PayloadJSON 是 question/human_gate 卡的结构化内容；ToolUseID 关联会话
	// 里的 ask_user tool call。普通消息保持空串。
	PayloadJSON string `json:"payload_json,omitempty"`
	ToolUseID   string `json:"tool_use_id,omitempty"`
	// Router（Plan 2026-11-14）字段。ReplyToID 把回复绑定到触发它的留言；
	// RouterDecisionID 绑定产生该活动的决策；Purpose 区分 chat/progress/reply/result；
	// Ordinal 保证同一回合多条回复的稳定顺序；Hidden 供内部/审计行使用，
	// 不进入用户可见活动流。
	ReplyToID        string    `json:"reply_to_id,omitempty"`
	RouterDecisionID string    `json:"router_decision_id,omitempty"`
	Purpose          string    `json:"purpose,omitempty"`
	Ordinal          int       `json:"ordinal,omitempty"`
	Hidden           bool      `json:"hidden,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at,omitempty"`
}

type ContinueRequest struct {
	RequestID string `json:"request_id"`
	TaskID    string `json:"task_id"`
	CommentID string `json:"comment_id"`
	Status    string `json:"status"`
	// NodeRunID/SessionKey 是留言提交时绑定的活动 Agent NodeRun（D3）。
	// 多分支活动期间为空，收束为唯一活动节点后再补写；绑定后不可变。
	NodeRunID  string    `json:"node_run_id,omitempty"`
	SessionKey string    `json:"session_key,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type PostCommentInput struct {
	TaskID            string
	CommentID         string
	Author            string
	Role              string
	Body              string
	Type              string
	WorkflowChoice    string
	WorkflowVersionID string
	// ExpectedNodeRunID 是用户显式选择的目标活动 Agent NodeRun（D3 的
	// 「多个活动时提示用户选择」分支）。留空则按当前活动节点数自动判定；
	// 非空时必须恰好指向当前 run 的活动 Agent NodeRun，否则拒绝而不降级。
	ExpectedNodeRunID string
}

type Runner interface {
	CreateTaskWorktree(ctx context.Context, rootID, name, branchMode, branch string) (WorktreeInfo, error)
	TaskUpdated(rootID string, detail TaskDetail)
}

// ── WP-04 多 worktree 注册表 ─────────────────────────────────────────────
//
// Task→多 worktree：每个 entry 归属一个 NodeRun（node_run_id 为空表示任务主
// worktree），生命周期由 state 表达。tasks 表上的
// 单 worktree 列（WorktreePath/WorktreeRootID/WorktreeBuilt）保持为「主 worktree」
// （ensureTaskWorktree 第一次建/认领的那棵），registry 是超集：所有 worktree 都在
// task_worktrees 里，主 worktree 在两处都有记录。兼容要求：存量单 worktree 任务
// 在 registry 为空时行为与今日完全一致（FinishTaskWorktree 等只看 tasks 列）。
const (
	WorktreeStateActive    = "active"
	WorktreeStateMerged    = "merged"
	WorktreeStateClosed    = "closed"
	WorktreeStateNeedsUser = "needs_user"
)

// ActivityTypeMergeConflict 是多 worktree 自动合并撞上冲突时落的 activity 类型
// （WP-01 的 task_activities 表）。合并冲突 = needs-user：两个分支都保留，不自动
// 解、不 stash、不覆盖用户改动。
const ActivityTypeMergeConflict = "merge_conflict"

// WorktreeEntry 是一行 task_worktrees：一个 task 拥有的一棵 worktree。
type WorktreeEntry struct {
	TaskID         string    `json:"task_id"`
	WorktreeID     string    `json:"worktree_id"`
	Branch         string    `json:"branch"`
	Path           string    `json:"path"`
	RootID         string    `json:"root_id"`
	NodeRunID      string    `json:"node_run_id,omitempty"`
	State          string    `json:"state"`
	MergedCommit   string    `json:"merged_commit,omitempty"`
	ConflictFiles  []string  `json:"conflict_files,omitempty"`
	ConflictOutput string    `json:"conflict_output,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}
