package kanban

import (
	"context"
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
	StartImmediately     bool      `json:"start_immediately,omitempty"`
	Agent                string    `json:"agent,omitempty"`
	Model                string    `json:"model,omitempty"`
	Mode                 string    `json:"mode,omitempty"`
	Effort               string    `json:"effort,omitempty"`
	FastService          string    `json:"fast_service,omitempty"`
	PlanMode             bool      `json:"plan_mode,omitempty"`
	SessionReusePolicy   string    `json:"session_reuse_policy,omitempty"`
	PromptTemplate       string    `json:"prompt_template,omitempty"`
	AgentCanControlStage bool      `json:"agent_can_control_stage,omitempty"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
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
	Stages      []TaskTemplateStage `json:"stages"`
	CreatedAt   time.Time           `json:"created_at"`
	UpdatedAt   time.Time           `json:"updated_at"`
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
	SchedulerAdmitted  bool            `json:"scheduler_admitted,omitempty"` // 兼容保留：位无调度器时恒为 true
	MainSessionKey     string          `json:"main_session_key,omitempty"`
	WorktreeRootID     string          `json:"worktree_root_id,omitempty"`
	WorktreePath       string          `json:"worktree_path,omitempty"`
	Labels             []string        `json:"labels"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
	CompletedAt        string          `json:"completed_at,omitempty"`
	CurrentStageName   string          `json:"current_stage_name,omitempty"`
	CurrentStageStatus string          `json:"current_stage_status,omitempty"`
	AuxFlags           TaskAuxFlags    `json:"aux_flags"`
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

type Runner interface {
	CreateTaskWorktree(ctx context.Context, rootID, name, branchMode, branch string) (WorktreeInfo, error)
	EnsureAgentSession(ctx context.Context, exec AgentStageExecution) (string, error)
	RunAgentStage(ctx context.Context, exec AgentStageExecution) error
	TaskUpdated(rootID string, detail TaskDetail)
}
