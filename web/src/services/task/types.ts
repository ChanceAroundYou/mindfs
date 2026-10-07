export type StageRole = "user" | "agent";
// 状态词表里没有 queued：该状态已退役（task_store 的 migrate() 无条件把 queued 归入 pending，
// 见 G-Y 看板重做）。留着它只会让「排队/调度位」这套已删模型在前端有复活路径。
export type TaskStatus =
  | "pending"
  | "running"
  | "waiting_user"
  | "paused"
  | "success"
  | "fail"
  | "cancelled";

export type StageTemplate = {
  id?: string;
  name: string;
  role: StageRole;
  auto_advance?: boolean;
  /** 首段（任务输入）专用：建完立刻开跑，不用等用户点「立即执行」 */
  start_immediately?: boolean;
  agent?: string;
  model?: string;
  mode?: string;
  effort?: string;
  fast_service?: string;
  plan_mode?: boolean;
  session_reuse_policy?: "task_main" | "same_stage" | "always_new";
  prompt_template?: string;
  agent_can_control_stage?: boolean;
  /**
   * 服务端生成的段标记（"" = 普通段，"worktree_finish" = 收尾段）。
   *
   * **必须在这里带上**：updateTaskStage 发的是整个 stage 对象，服务端那边是整段替换
   * （UpdateStage）。类型里没有这个字段的话，用户在面板上编辑一次收尾段，标记就被
   * 静默抹掉，那个任务再也认不出自己走过收尾流程。
   */
  kind?: string;
  created_at?: string;
  updated_at?: string;
};

export type TaskTemplateStage = {
  id?: string;
  stage_template_id?: string;
  position: number;
  snapshot: StageTemplate;
};

export type TaskTemplate = {
  id?: string;
  name: string;
  description?: string;
  /** 限定到某个项目；不传/空 = 全局模板，任何项目都能套用。 */
  root_id?: string;
  stages: TaskTemplateStage[];
  created_at?: string;
  updated_at?: string;
};

export type KanbanTask = {
  id: string;
  task_number?: number;
  root_id: string;
  name?: string;
  task_template_id: string;
  task_template_name: string;
  stages?: StageTemplate[];
  create_worktree?: boolean;
  worktree_branch_mode?: "new" | "existing";
  worktree_branch?: string;
  current_stage_index: number;
  status: TaskStatus;
  main_session_key?: string;
  worktree_root_id?: string;
  worktree_path?: string;
  /** 派生（服务端算，不落库）：worktree_path 指向的目录已经不在了。 */
  worktree_missing?: boolean;
  /**
   * 这个任务**曾经建出过一个 worktree**（服务端落库，清归属时不清它）。
   *
   * 为什么必须要：worktree_path 为空有两种相反的含义 ——
   * 「还没建」（首段还是 user 段）和「建过、记录被清掉了」。只有这一个字段能
   * 把两者分开。没有它，「路径为空」会被一律显示成「已收尾」—— 2026-10-01 实测
   * 就是这样：一个仍在使用的 worktree 被标成了活已经并回主干。
   */
  worktree_built?: boolean;
  /**
   * stages 的派生布尔：至少有一个 role === "agent" 的段。
   *
   * 只有工作台的 overview 投影带它 —— 那份响应刻意丢了 stages（体积），
   * 而收尾键的判据要它。项目看板走完整任务，读 stages 即可。
   */
  has_agent_stage?: boolean;
  labels?: string[];
  created_at: string;
  updated_at: string;
  completed_at?: string;
  current_stage_name?: string;
  current_stage_status?: string;
  aux_flags?: {
    ask_user_waiting?: boolean;
    has_plan?: boolean;
    has_todos?: boolean;
    has_task?: boolean;
    session_error?: string;
  };
};

export type StageRun = {
  id: string;
  task_id: string;
  stage_index: number;
  stage_name: string;
  role: StageRole;
  status: string;
  session_key?: string;
  input?: string;
  rendered_prompt?: string;
  started_at?: string;
  finished_at?: string;
  created_at: string;
  updated_at: string;
};

export type TaskEvent = {
  id: string;
  task_id: string;
  stage_run_id?: string;
  type: string;
  payload_json?: string;
  created_at: string;
};

export type TaskDetail = {
  task: KanbanTask;
  stage_runs: StageRun[];
  events: TaskEvent[];
};

export type TaskOverviewItem = {
  root_id: string;
  root_name: string;
  task: KanbanTask;
  /**
   * 前端跨节点扇出时打的标，**后端不返**（后端 Overview 遍历本节点 roots，不知道自己在哪个节点上）。
   * 可选是为了让直接用 /api/tasks/overview 的调用方类型照旧成立。
   */
  nodeId?: string;
};
