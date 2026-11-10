// 留言板与运行视图的共享类型（components 与 services 共用）。
//
// 为什么单独放 shared：仓库规则禁止 components 直接 import services
// （web/tests/reviewer-architecture-rules.test.mjs 钉住）。类型与纯函数放这里，
// 带副作用的取数留在 services/task/comments.ts。

export type WorkflowChoice = "specified" | "disabled" | "empty";

export type TaskActivity = {
  id: string;
  task_id: string;
  type: string;
  author: string;
  role: "user" | "agent" | string;
  body: string;
  workflow_choice: WorkflowChoice | string;
  workflow_version_id?: string;
  created_at: string;
};

export type ContinueRequest = {
  request_id: string;
  task_id: string;
  comment_id: string;
  status: "queued" | "blocked" | "consumed" | string;
  created_at: string;
  updated_at: string;
};

export type PostCommentResult = {
  activity: TaskActivity;
  continue_request: ContinueRequest;
};

export type TaskExecutionEngine = {
  task_id: string;
  engine: "legacy" | "workflow" | string;
  workflow_run_id?: string;
};

export type WorkflowRunPayload = {
  id: string;
  task_id: string;
  revision: number;
  graph?: { nodes?: Array<{ id: string; name?: string; kind?: string }>; edges?: unknown[] };
  node_states?: Record<string, string>;
  node_outputs?: Record<string, Record<string, unknown>>;
  created_at?: string;
  updated_at?: string;
};

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
  input?: Record<string, unknown>;
  output?: Record<string, unknown>;
  started_at?: string;
  finished_at?: string;
  prev_attempt_id?: string;
};

export function newCommentId(): string {
  const c = globalThis.crypto as Crypto | undefined;
  if (c && typeof c.randomUUID === "function") return c.randomUUID();
  return `c-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;
}
