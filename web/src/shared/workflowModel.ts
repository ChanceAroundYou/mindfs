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

/** Loop 的内部图；`params.graph` 是与后端 workflow_engine.go 约定的唯一持久化键。 */
export function loopInnerGraph(node: WorkflowNodeDef | null | undefined): WorkflowGraph | undefined {
  const raw = node?.params?.graph;
  if (!raw || typeof raw !== "object") return undefined;
  const inner = raw as WorkflowGraph;
  if (!Array.isArray(inner.nodes)) return undefined;
  return inner;
}

/** 只替换内部图，保留 max_iterations / exit_expression 等 Loop 自身配置。 */
export function withLoopInnerGraph(node: WorkflowNodeDef, inner: WorkflowGraph): WorkflowNodeDef {
  return {
    ...node,
    params: { ...node.params, graph: { nodes: inner.nodes, edges: inner.edges ?? [] } },
  };
}

function validateNodeShape(n: WorkflowNodeDef, errors: string[]) {
  if (!NODE_KINDS.includes(n.kind)) errors.push(`未知节点类型：${n.kind}`);
  if (n.kind === "condition") {
    const routes = (n.params?.routes as string[] | undefined) ?? [];
    if (!Array.isArray(routes) || routes.length === 0) errors.push(`条件节点 ${n.id} 需要至少一条 route`);
  }
}

function validateLoopNode(n: WorkflowNodeDef, topIds: Set<string>, errors: string[]) {
  const max = Number(n.params?.max_iterations ?? LOOP_DEFAULT);
  if (!Number.isInteger(max) || max < LOOP_MIN || max > LOOP_MAX) {
    errors.push(`Loop ${n.id} 的 max_iterations 必须在 ${LOOP_MIN}..${LOOP_MAX}`);
  }
  const exit = String(n.params?.exit_expression ?? "").trim();
  if (!exit) errors.push(`Loop ${n.id} 需要 exit_expression`);
  const inner = loopInnerGraph(n);
  if (!inner) {
    errors.push(`Loop ${n.id} 缺少内部图`);
    return;
  }
  if (inner.nodes.length === 0) errors.push(`Loop ${n.id} 的内部图至少需要一个节点`);
  const innerIds = new Set<string>();
  for (const innerNode of inner.nodes) {
    if (!innerNode.id) {
      errors.push(`Loop ${n.id} 内部节点缺少 id`);
      continue;
    }
    if (innerIds.has(innerNode.id)) errors.push(`Loop ${n.id} 内部节点 id 重复：${innerNode.id}`);
    innerIds.add(innerNode.id);
    if (topIds.has(innerNode.id)) errors.push(`Loop ${n.id} 内部节点 id 与顶层节点冲突：${innerNode.id}`);
    if (innerNode.kind === "loop") {
      errors.push(`Loop ${n.id} 内部不允许嵌套 Loop`);
      continue;
    }
    if (innerNode.kind === "human_gate") {
      errors.push(`Loop ${n.id} 内部暂不支持 human_gate`);
      continue;
    }
    validateNodeShape(innerNode, errors);
  }
  for (const e of inner.edges ?? []) {
    if (!innerIds.has(e.from)) errors.push(`Loop ${n.id} 内部边起点不存在：${e.from}`);
    if (!innerIds.has(e.to)) errors.push(`Loop ${n.id} 内部边终点不存在：${e.to}`);
    if (e.from === e.to) errors.push(`Loop ${n.id} 内部边不能自指：${e.from}`);
  }
}

/** 前端同构校验；返回人类可读错误列表（空 = 通过）。 */
export function validateWorkflowGraph(graph: WorkflowGraph): string[] {
  const errors: string[] = [];
  const nodes = graph?.nodes ?? [];
  if (nodes.length === 0) errors.push("至少需要一个节点");
  const ids = new Set<string>();
  // 先收齐顶层 id，再校验 Loop 内部图：内部 id 冲突要看得到后面声明的节点。
  for (const n of nodes) {
    if (!n.id) {
      errors.push("节点缺少 id");
      continue;
    }
    if (ids.has(n.id)) errors.push(`节点 id 重复：${n.id}`);
    ids.add(n.id);
  }
  for (const n of nodes) {
    if (!n.id) continue;
    validateNodeShape(n, errors);
    if (n.kind === "loop") validateLoopNode(n, ids, errors);
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
  if (kind === "loop") {
    base.params = {
      max_iterations: LOOP_DEFAULT,
      exit_expression: "",
      // 内部图是 Loop 的唯一真源；新 Loop 先放一个空的 Agent 节点，便于直接进入子画布。
      graph: { nodes: [newNode("agent")], edges: [] },
    };
  }
  if (kind === "notify") base.params = { message: "" };
  return base;
}

