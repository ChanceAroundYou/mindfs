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

	// StageKindWorktreeFinish 标记「收尾段」：由 BeginFinishWorktree 追加的一段
	// agent 工作，让 agent 自己 commit 并 merge 回主干；这段成功后由服务端接着清场
	// （合并 → 拆 worktree → 删分支 → 会话 repoint 回主 checkout）。
	//
	// 为什么分段跑而不是一键收尾：agent 的活没 commit 时，服务端直接 merge 等于白干；
	// 有未提交改动时 `git worktree remove` 又会拒绝。commit 这件事只有 agent 知道
	// 哪些是成品、哪些是半成品 —— 与 wt-finish.sh 把 commit 交给 agent 是同一个分工。
	StageKindWorktreeFinish = "worktree_finish"

	// 任务状态对齐全局状态列：待开始 / 进行中 / 等待你 / 已完成 / 失败·取消。
	// 旧数据中的 queued/paused 在迁移时分别归入 pending/running。
	StatusPending     = "pending"
	StatusRunning     = "running"
	StatusWaitingUser = "waiting_user"
	StatusPaused      = "paused"
	StatusSuccess     = "success"
	StatusFail        = "fail"
	StatusCancelled   = "cancelled"

	StageStatusPending     = "pending"
	StageStatusRunning     = "running"
	StageStatusWaitingUser = "waiting_user"
	StageStatusSuccess     = "success"
	StageStatusFail        = "fail"
	StageStatusCancelled   = "cancelled"
	StageStatusApproved    = "approved"
	StageStatusRejected    = "rejected"

	SessionReuseTaskMain  = "task_main"
	SessionReuseSameStage = "same_stage"
	SessionReuseAlwaysNew = "always_new"
)

// StageTemplate 是一段可执行的流水阶段定义；既用于模板（预设），也直接存储在任务身上。
type StageTemplate struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Role        string `json:"role"`
	AutoAdvance bool   `json:"auto_advance"`
	// StartImmediately 只对首段（任务输入）有意义：建完任务立刻开跑，
	// 不用等用户点「立即执行」。user 段的 AutoAdvance 是引擎不读的死字段
	// （user 段被批准后一律推进），别拿它表达「要不要开跑」。
	StartImmediately     bool   `json:"start_immediately,omitempty"`
	Agent                string `json:"agent,omitempty"`
	Model                string `json:"model,omitempty"`
	Mode                 string `json:"mode,omitempty"`
	Effort               string `json:"effort,omitempty"`
	FastService          string `json:"fast_service,omitempty"`
	PlanMode             bool   `json:"plan_mode,omitempty"`
	SessionReusePolicy   string `json:"session_reuse_policy,omitempty"`
	PromptTemplate       string `json:"prompt_template,omitempty"`
	AgentCanControlStage bool   `json:"agent_can_control_stage,omitempty"`
	// Kind 标这一段是「哪一种由服务端生成的段」，空 = 用户/模板定义的普通段。
	//
	// 为什么存在独立字段而不是在 Name/PromptTemplate 里塞标记：这两处用户都能在看板上改
	// （UpdateStage 是整段替换，任务编辑器直接改 name/prompt），标记会被改掉，之后就再也
	// 认不出这段是收尾段。段定义存在 tasks.task_stages_json 里（task_store.go 的
	// json.Marshal），加这个字段零迁移、零 SQL 改动。
	//
	// 现在只有一个取值 StageKindWorktreeFinish —— 收尾段。它同时意味着「成功后要跟着
	// 清场」，所以没有再拆第二个布尔字段：等真有第二种内置段、且两者语义开始分叉时再拆。
	Kind string `json:"kind,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type TaskTemplateStage struct {
	ID              string        `json:"id"`
	StageTemplateID string        `json:"stage_template_id,omitempty"`
	Position        int           `json:"position"`
	Snapshot        StageTemplate `json:"snapshot"`
}

// TaskTemplate 仅作为「预设」存在：新建任务时可选套用，之后与任务完全解耦，可随时修改/删除。
type TaskTemplate struct {
	ID          string              `json:"id"`
	Name        string              `json:"name"`
	Description string              `json:"description,omitempty"`
	// RootID 把模板限定到某个项目；空 = 全局模板，任何项目都能套用。
	//
	// 模板库只有主节点一份（见 docs/multi-node-control-plane.md）：跨项目复用的写全局，
	// 某个项目专用的写该项目的 root id。列表按 root 过滤（全局 + 当前项目）。
	RootID   string              `json:"root_id,omitempty"`
	Stages   []TaskTemplateStage `json:"stages"`
	CreatedAt time.Time          `json:"created_at"`
	UpdatedAt time.Time          `json:"updated_at"`
}

// Task 自带流水（Stages）：执行永远读任务自己的阶段，不再回查模板。
type Task struct {
	ID                 string          `json:"id"`
	TaskNumber         int             `json:"task_number"`
	RootID             string          `json:"root_id"`
	Name               string          `json:"name,omitempty"`
	TaskTemplateID     string          `json:"task_template_id,omitempty"`
	TaskTemplateName   string          `json:"task_template_name,omitempty"`
	Stages             []StageTemplate `json:"stages"`
	CreateWorktree     bool            `json:"create_worktree"`
	WorktreeBranchMode string          `json:"worktree_branch_mode,omitempty"`
	WorktreeBranch     string          `json:"worktree_branch,omitempty"`
	CurrentStageIndex  int             `json:"current_stage_index"`
	Status             string          `json:"status"`
	MainSessionKey     string          `json:"main_session_key,omitempty"`
	WorktreeRootID     string          `json:"worktree_root_id,omitempty"`
	WorktreePath       string          `json:"worktree_path,omitempty"`
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
	WorktreeBuilt bool `json:"worktree_built"`
	Labels             []string        `json:"labels"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
	CompletedAt        string          `json:"completed_at,omitempty"`
	CurrentStageName   string          `json:"current_stage_name,omitempty"`
	CurrentStageStatus string          `json:"current_stage_status,omitempty"`
	AuxFlags           TaskAuxFlags    `json:"aux_flags"`
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

type StageRun struct {
	ID             string    `json:"id"`
	TaskID         string    `json:"task_id"`
	StageIndex     int       `json:"stage_index"`
	StageName      string    `json:"stage_name"`
	Role           string    `json:"role"`
	Status         string    `json:"status"`
	SessionKey     string    `json:"session_key,omitempty"`
	Input          string    `json:"input,omitempty"`
	RenderedPrompt string    `json:"rendered_prompt,omitempty"`
	StartedAt      string    `json:"started_at,omitempty"`
	FinishedAt     string    `json:"finished_at,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type TaskEvent struct {
	ID         string    `json:"id"`
	TaskID     string    `json:"task_id"`
	StageRunID string    `json:"stage_run_id,omitempty"`
	Type       string    `json:"type"`
	Payload    string    `json:"payload_json,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type TaskDetail struct {
	Task      Task        `json:"task"`
	StageRuns []StageRun  `json:"stage_runs"`
	Events    []TaskEvent `json:"events"`
}

type WorktreeInfo struct {
	RootID string
	Path   string
}

type AgentStageExecution struct {
	RootID          string
	RuntimeRootPath string
	Task            Task
	Stage           StageTemplate
	Run             StageRun
	Prompt          string
}

// StageOutcome 是 agent 阶段跑完之后的结论。
//
// 以前这里只有 error，而 error 只表示「消息投递失败」，不表示「活干完了」——
// 于是 runAgentStage 把「没抛错」直接写成 success，executeTask 接着推进下一段，
// 前一段没完成也在错误前提上开跑，阶段就此错乱。
// 现在必须由 agent 显式回报，回报不了就是 Silent，停下等人。
type StageOutcome int

const (
	// StageOutcomeDone：agent 输出了本段的完成标记。
	StageOutcomeDone StageOutcome = iota
	// StageOutcomeBlocked：agent 明确说受阻或未完成，Reason 是它给的原因。
	StageOutcomeBlocked
	// StageOutcomeSilent：跑完了但没有回报完成。判为未完成。
	StageOutcomeSilent
)

// StageResult 随 StageOutcome 一起带回 Reason（agent 给的受阻原因，或空）。
type StageResult struct {
	Outcome StageOutcome
	Reason  string
}

type Runner interface {
	CreateTaskWorktree(ctx context.Context, rootID, name, branchMode, branch string) (WorktreeInfo, error)
	EnsureAgentSession(ctx context.Context, exec AgentStageExecution) (string, error)
	// RunAgentStage 返回 error 表示传输层失败（沿用 failTask 路径）；
	// 返回 nil error 时 StageResult 才有意义，决定这一段算不算完成。
	RunAgentStage(ctx context.Context, exec AgentStageExecution) (StageResult, error)
	TaskUpdated(rootID string, detail TaskDetail)
}
