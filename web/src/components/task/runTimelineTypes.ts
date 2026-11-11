// 运行视图的展示类型（对齐后端 workflow_types.go 的 WorkflowRun/NodeRun）。
//
// 单独成文件，避免 RunTimeline 与节点行组件互相 import 类型形成环。
// 数据由父层（useRunViewEntry）取好传入；这里没有任何副作用。

export type WorkflowRunNodeState = "pending" | "queued" | "running" | "success" | "fail" | "waiting_user" | "skipped" | "unknown" | "cancelled";

export type WorkflowRunSnapshot = {
  id: string;
  task_id?: string;
  version?: string;
  revision?: number;
  node_states?: Record<string, string>;
  created_at?: string;
  updated_at?: string;
};

export type NodeRunSnapshot = {
  id: string;
  run_id?: string;
  node_id: string;
  session_key?: string;
  status: string;
  input?: Record<string, unknown> | string;
  output?: Record<string, unknown> | string;
  error?: string;
  started_at?: string;
  finished_at?: string;
  agent?: string;
  model?: string;
};