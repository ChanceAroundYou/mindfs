// 留言板与运行视图的共享类型（components 与 services 共用）。
//
// 为什么单独放 shared：仓库规则禁止 components 直接 import services
// （web/tests/reviewer-architecture-rules.test.mjs 钉住）。类型与纯函数放这里，
// 带副作用的取数留在 services/task/runView.ts。

export type WorkflowChoice = "specified" | "disabled" | "empty";

// 对话式 Agent 回复的生命周期状态（kanban.ActivityStatus*）。
// 用户留言与系统事件不带状态，所以字段可选。
export type ActivityStatus = "pending" | "running" | "completed" | "failed" | "cancelled" | "waiting_user";

/** HumanGate 的专用决定；普通留言永远不能代替它（后端 D4）。 */
export type HumanGateDecision = "approve" | "reject";

export type TaskActivity = {
  id: string;
  task_id: string;
  root_id?: string;
  type: string;
  author: string;
  role: "user" | "agent" | "system" | string;
  body: string;
  workflow_choice: WorkflowChoice | string;
  workflow_version_id?: string;
  // RunID 是对话模型里的 workflow_run_id：后端只落 run_id 一列，不设同义第二字段。
  run_id?: string;
  node_run_id?: string;
  status?: ActivityStatus | string;
  summary?: string;
  session_key?: string;
  // payload_json 是 question / human_gate 卡的结构化内容（问题、选项、审批说明）。
  // 后端按原样透传，前端只解析自己要用的键；解析失败按空对象处理。
  payload_json?: string;
  tool_use_id?: string;
  // Router 归属（Plan 2026-11-14）：回复绑定到触发它的留言，purpose 区分
  // chat/progress/reply/result。运行期事件的汇报是 purpose=progress：
  // 同一留言下它会与结算回复重复展示同一条结果句，前端据此折叠。
  reply_to_id?: string;
  purpose?: "chat" | "progress" | "reply" | "result" | string;
  router_decision_id?: string;
  ordinal?: number;
  hidden?: boolean;
  created_at: string;
  updated_at?: string;
};

export type TaskExecutionEngine = {
  task_id: string;
  engine: "legacy" | "workflow" | string;
  workflow_run_id?: string;
};

export type WorkflowRunPayload = {
  id: string;
  task_id: string;
  version?: string;
  revision: number;
  graph?: { nodes?: Array<{ id: string; name?: string; kind?: string }>; edges?: unknown[] };
  node_states?: Record<string, string>;
  node_outputs?: Record<string, Record<string, unknown>>;
  created_at?: string;
  updated_at?: string;
};

/**
 * 一轮历史运行：Run 快照 + 这一轮自己的节点执行行。
 *
 * 后端 `/workflow-run/nodes` 现在按 Run 分组返回，这样多轮任务不会只剩最后一次
 * 执行（旧形状把 `runs` 省掉时，展示层回落到只画当前 Run）。
 */
export type WorkflowRunNodeRuns = WorkflowRunPayload & { node_runs?: NodeRunPayload[] };

// 后端 workflow_types.go 的 NodeRun 原样映射。展示层直接复用这一份类型
// （runTimelineTypes 只做别名），避免两处字段各写一遍后漂移。
export type NodeRunPayload = {
  id: string;
  run_id: string;
  node_id: string;
  loop_path?: string;
  iteration?: number;
  attempt: number;
  agent?: string;
  harness?: string;
  model?: string;
  session_key?: string;
  status: string;
  input?: Record<string, unknown> | string;
  output?: Record<string, unknown> | string;
  error?: string;
  started_at?: string;
  finished_at?: string;
  prev_attempt_id?: string;
};

