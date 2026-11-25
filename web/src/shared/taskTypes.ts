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
  /**
   * 非空 = 已归档（服务端落库）。归档只是任务自己的标记：卡片从工作台隐藏、
   * 留在看板「已结束 → 归档」子组；相关会话同样只被打上归档标记，没有被删除。
   */
  archived_at?: string;
  /**
   * 前端**传输级**标记：这条任务是从哪台机器取回来的（后端不返）。
   *
   * 同名项目在多台机器上各有一份是常态（fn 与 pc 都有 `docs`），而按 rootId
   * 反查节点的那张表每个 id 只留得下一条。卡片动作（完成/删除/暂停/重开）必须
   * 认这个标记，否则会把 A 机器的卡派给 B 机器，服务端回 `sql: no rows`。
   * 见 services/task/routing.ts 与 useWorkspaceBoard 的扇出打标。
   */
  _nodeId?: string;
  aux_flags?: {
    ask_user_waiting?: boolean;
    has_plan?: boolean;
    has_todos?: boolean;
    has_task?: boolean;
    session_error?: string;
  };
  /**
   * Router 还没处理完的巡检/恢复事件（后端派生字段，不落库）。
   * `kind` 只会是 `turn_interrupted` / `turn_stalled`；处理后服务端不再下发。
   */
  router_alert?: {
    kind: string;
    count?: number;
    since?: string;
  };
  /**
   * Router 自己派发的提问（后端派生字段，不落库）。与会话 worker 透传的 ask
   * （`aux_flags.ask_user_waiting`）分源：这里只认 `session_key` 为空的
   * `waiting_user` 提问卡。
   */
  router_ask?: {
    count?: number;
    since?: string;
    question?: string;
  };
  /**
   * 最新一条可见提醒（后端派生字段，不落库）。severity 为 info / warning / error，
   * 存量行可能为空（按 info 着色）。
   */
  latest_attention?: {
    body?: string;
    severity?: string;
    created_at?: string;
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