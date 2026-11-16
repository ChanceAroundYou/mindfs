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

export type KanbanTask = {
  id: string;
  task_number?: number;
  root_id: string;
  name?: string;
  task_template_id: string;
  task_template_name: string;
  create_worktree?: boolean;
  worktree_branch_mode?: "new" | "existing";
  worktree_branch?: string;
  status: TaskStatus;
  main_session_key?: string;
  /**
   * 最近一轮结算后的可读结果（成功摘要是节点汇报，失败/等待是原因）。
   * 卡片用它回答「这个任务最后做到了什么」；空 = 还没有一轮结算过。
   */
  result_summary?: string;
  worktree_root_id?: string;
  worktree_path?: string;
  /** 派生（服务端算，不落库）：worktree_path 指向的目录已经不在了。 */
  worktree_missing?: boolean;
  /**
   * 这个任务**曾经建出过一个 worktree**（服务端落库，清归属时不清它）。
   *
   * 为什么必须要：worktree_path 为空有两种相反的含义 ——
   * 「还没建」和「建过、记录被清掉了」。只有这一个字段能把两者分开。
   * 没有它，「路径为空」会被一律显示成「已收尾」—— 2026-10-01 实测
   * 就是这样：一个仍在使用的 worktree 被标成了活已经并回主干。
   */
  worktree_built?: boolean;
  labels?: string[];
  created_at: string;
  updated_at: string;
  completed_at?: string;
  aux_flags?: {
    ask_user_waiting?: boolean;
    has_plan?: boolean;
    has_todos?: boolean;
    has_task?: boolean;
    session_error?: string;
  };
};

export type TaskEvent = {
  id: string;
  task_id: string;
  type: string;
  payload_json?: string;
  created_at: string;
};

export type TaskDetail = {
  task: KanbanTask;
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