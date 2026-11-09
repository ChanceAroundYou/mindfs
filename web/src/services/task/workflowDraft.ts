import { appURL } from "../net/base";
import { protectedJSON } from "../net/api";

// 工作流草稿的类型与校验：画布与页面共用。
//
// 为什么单独一层：schema 校验（节点 id 唯一、边两端存在、Loop 上限 1..20、
// condition 必须有 routes）必须与后端 workflow_validate.go 对齐，但前端不能
// 依赖后端代码，因此这里只做「提交前的同构校验」，真正的权威仍在后端。

export type WorkflowNodeKind = "agent" | "condition" | "human_gate" | "loop" | "notify";

export type WorkflowNodeDef = {
  id: string;
  name?: string;
  kind: WorkflowNodeKind;
  params?: Record<string, unknown>;
  position?: { x: number; y: number };
};

export type WorkflowEdgeDef = { from: string; to: string; route?: string };

export type WorkflowGraph = {
  nodes: WorkflowNodeDef[];
  edges?: WorkflowEdgeDef[];
  root_id?: string;
};

export const NODE_KINDS: WorkflowNodeKind[] = ["agent", "condition", "human_gate", "loop", "notify"];

export const LOOP_MIN = 1;
export const LOOP_MAX = 20;
export const LOOP_DEFAULT = 5;

export type WorkflowDraft = {
  workflow_id: string;
  revision: string;
  graph: WorkflowGraph;
  author?: string;
  updated_at?: string;
};

export type WorkflowVersion = {
  version: string;
  workflow_id: string;
  revision: string;
  graph: WorkflowGraph;
  published_by?: string;
  published_at?: string;
};

/** 前端同构校验；返回人类可读错误列表（空 = 通过）。 */
export function validateWorkflowGraph(graph: WorkflowGraph): string[] {
  const errors: string[] = [];
  const nodes = graph?.nodes ?? [];
  if (nodes.length === 0) errors.push("至少需要一个节点");
  const ids = new Set<string>();
  for (const n of nodes) {
    if (!n.id) {
      errors.push("节点缺少 id");
      continue;
    }
    if (ids.has(n.id)) errors.push(`节点 id 重复：${n.id}`);
    ids.add(n.id);
    if (!NODE_KINDS.includes(n.kind)) errors.push(`未知节点类型：${n.kind}`);
    if (n.kind === "condition") {
      const routes = (n.params?.routes as string[] | undefined) ?? [];
      if (!Array.isArray(routes) || routes.length === 0) errors.push(`条件节点 ${n.id} 需要至少一条 route`);
    }
    if (n.kind === "loop") {
      const max = Number(n.params?.max_iterations ?? LOOP_DEFAULT);
      if (!Number.isInteger(max) || max < LOOP_MIN || max > LOOP_MAX) {
        errors.push(`Loop ${n.id} 的 max_iterations 必须在 ${LOOP_MIN}..${LOOP_MAX}`);
      }
      if (n.params?.graph) errors.push(`Loop ${n.id} 不允许嵌套 Loop 容器（首版）`);
    }
  }
  for (const e of graph?.edges ?? []) {
    if (!ids.has(e.from)) errors.push(`边起点不存在：${e.from}`);
    if (!ids.has(e.to)) errors.push(`边终点不存在：${e.to}`);
    if (e.from === e.to) errors.push(`边不能自指：${e.from}`);
  }
  return errors;
}

export function nodeKindLabel(kind: WorkflowNodeKind, t: (k: string) => string): string {
  return t(`task.workflowCanvas.kind.${kind}`);
}

const nodeId = () => `n-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 7)}`;

export function newNode(kind: WorkflowNodeKind): WorkflowNodeDef {
  const base: WorkflowNodeDef = { id: nodeId(), name: "", kind, params: {} };
  if (kind === "agent") base.params = { writes_workspace: true, agent: "", model: "", prompt: "" };
  if (kind === "condition") base.params = { expression: "", routes: ["pass", "fail"] };
  if (kind === "human_gate") base.params = { prompt: "" };
  if (kind === "loop") base.params = { max_iterations: LOOP_DEFAULT, exit_expression: "" };
  if (kind === "notify") base.params = { message: "" };
  return base;
}

export async function listWorkflowDrafts(nodeIdParam?: string): Promise<WorkflowDraft[]> {
  const payload = await protectedJSON<{ drafts?: WorkflowDraft[] }>(appURL("/api/workflows", undefined, nodeIdParam));
  return Array.isArray(payload?.drafts) ? payload.drafts : [];
}

type DraftEnvelope = { draft?: WorkflowDraft };

export async function createWorkflowDraft(
  input: { workflowId: string; rootId?: string; author?: string; graph: WorkflowGraph },
  nodeIdParam?: string,
): Promise<WorkflowDraft> {
  const payload = await protectedJSON<DraftEnvelope>(appURL("/api/workflows", undefined, nodeIdParam), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ workflow_id: input.workflowId, root_id: input.rootId || "", author: input.author || "user", graph: input.graph }),
  });
  if (!payload?.draft) throw new Error("draft create: empty response");
  return payload.draft;
}

export async function updateWorkflowDraft(
  workflowId: string,
  input: { baseRevision: string; author?: string; graph: WorkflowGraph },
  nodeIdParam?: string,
): Promise<WorkflowDraft> {
  const payload = await protectedJSON<DraftEnvelope>(appURL(`/api/workflows/${encodeURIComponent(workflowId)}`, undefined, nodeIdParam), {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ base_revision: input.baseRevision, author: input.author || "user", graph: input.graph }),
  });
  if (!payload?.draft) throw new Error("draft update: empty response");
  return payload.draft;
}

export async function publishWorkflowDraft(
  workflowId: string,
  input: { revision: string; publishedBy?: string },
  nodeIdParam?: string,
): Promise<WorkflowVersion> {
  const payload = await protectedJSON<{ version?: WorkflowVersion }>(appURL(`/api/workflows/${encodeURIComponent(workflowId)}/publish`, undefined, nodeIdParam), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ confirmed_revision: input.revision, published_by: input.publishedBy || "user" }),
  });
  if (!payload?.version) throw new Error("publish: empty response");
  return payload.version;
}
