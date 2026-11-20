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

/**
 * 工作流节点会话归属（2026-11-20 用户决策）：一轮 Run 默认共用一条会话
 * （后端 `session_reuse_policy=run`）。存量的 per_node / same_stage / always_new
 * 都表示「这个节点自己一段对话」，所以「另起会话」的判据是「显式策略且不是 run」。
 */
export const WORKFLOW_NODE_NEW_SESSION_POLICY = "always_new";

export function workflowNodeStartsNewSession(node: WorkflowNodeDef): boolean {
  const policy = String(node.params?.session_reuse_policy ?? "").trim();
  return !!policy && policy !== "run";
}

/** 勾选写 always_new；取消删掉参数，回到这一轮 Run 的共享会话。 */
export function setWorkflowNodeStartsNewSession(
  params: Record<string, unknown> | undefined,
  startsNew: boolean,
): Record<string, unknown> {
  const next: Record<string, unknown> = { ...(params ?? {}) };
  if (startsNew) {
    next.session_reuse_policy = WORKFLOW_NODE_NEW_SESSION_POLICY;
  } else {
    delete next.session_reuse_policy;
  }
  return next;
}

/**
 * 节点独立 worktree（后端 D10 的显式 opt-in）：`worktree=independent` 让这个
 * Agent 节点单建一棵树，天然可与共享主树并发；节点结束时按 Run 创建时快照的
 * 集成分支合并。别的值（含拼错）后端一律回落共享主树，所以这里只认这一个值。
 */
export const WORKFLOW_NODE_INDEPENDENT_WORKTREE = "independent";

export function workflowNodeUsesOwnWorktree(node: WorkflowNodeDef): boolean {
  return String(node.params?.worktree ?? "").trim() === WORKFLOW_NODE_INDEPENDENT_WORKTREE;
}

/** 勾选写 worktree=independent；取消删掉参数，回到共享主树。 */
export function setWorkflowNodeUsesOwnWorktree(
  params: Record<string, unknown> | undefined,
  independent: boolean,
): Record<string, unknown> {
  const next: Record<string, unknown> = { ...(params ?? {}) };
  if (independent) {
    next.worktree = WORKFLOW_NODE_INDEPENDENT_WORKTREE;
  } else {
    delete next.worktree;
  }
  return next;
}

/**
 * 节点 Plan 模式（后端 `params.plan_mode`）：这一回合以只读的规划模式跑，
 * 先出方案、不动文件。它同时就是「写不写工作区」的开关（用户 2026-11-20：
 * 「写入这个按钮被 plan 替代，开 plan 就不写入，反之也是」），所以勾选/取消
 * 时一起改 `writes_workspace`，不再有独立的写入开关。
 */
export function workflowNodeUsesPlanMode(node: WorkflowNodeDef): boolean {
  return node.params?.plan_mode === true;
}

/** 勾选：plan_mode=true 且 writes_workspace=false；取消：两个都删掉（缺省即会写）。 */
export function setWorkflowNodeUsesPlanMode(
  params: Record<string, unknown> | undefined,
  plan: boolean,
): Record<string, unknown> {
  const next: Record<string, unknown> = { ...(params ?? {}) };
  if (plan) {
    next.plan_mode = true;
    next.writes_workspace = false;
  } else {
    delete next.plan_mode;
    delete next.writes_workspace;
  }
  return next;
}

export type WorkflowGraph = {
  nodes: WorkflowNodeDef[];
  edges?: WorkflowEdgeDef[];
  root_id?: string;
};

export const NODE_KINDS: WorkflowNodeKind[] = ["agent", "condition", "human_gate", "loop", "notify"];

/**
 * 节点里「人要看的那段配置」。移动端只读清单靠它展示每个节点的 prompt，
 * 不能只显示类型和名字——那正好是用户最需要核对的内容。
 */
export function workflowNodeConfigText(node: WorkflowNodeDef): string {
  const params = node.params ?? {};
  if (node.kind === "agent" || node.kind === "human_gate") {
    return String(params.prompt ?? "").trim();
  }
  if (node.kind === "notify") {
    return String(params.message ?? "").trim();
  }
  if (node.kind === "condition") {
    const expression = String(params.expression ?? "").trim();
    const routes = Array.isArray(params.routes) ? params.routes : [];
    const routeText = routes.map((route) => String(route)).filter(Boolean).join(" / ");
    return [expression, routeText].filter(Boolean).join(" · ");
  }
  if (node.kind === "loop") {
    const exit = String(params.exit_expression ?? "").trim();
    const max = Number(params.max_iterations ?? LOOP_DEFAULT);
    return `最多 ${Number.isFinite(max) ? max : LOOP_DEFAULT} 轮${exit ? ` · 退出条件 ${exit}` : ""}`;
  }
  return "";
}

export const LOOP_MIN = 1;
export const LOOP_MAX = 20;
export const LOOP_DEFAULT = 5;

export type WorkflowDraft = {
  workflow_id: string;
  revision: string;
  graph: WorkflowGraph;
  author?: string;
  updated_at?: string;
  /** 用户可见名称（控制面 WorkflowMeta）；列表接口附带，缺省回退到 workflow_id。 */
  name?: string;
  /** 工作流用途描述（控制面 WorkflowMeta）；列表接口附带。 */
  description?: string;
};

/** 工作流列表/标题上给用户看的名字；没起名时只去掉内部前缀，不暴露 workflow-。 */
export function workflowDraftDisplayName(draft: { workflow_id: string; name?: string }): string {
  return `${draft.name || ""}`.trim() || draft.workflow_id.replace(/^workflow-/, "");
}

export type WorkflowVersion = {
  version: string;
  workflow_id: string;
  revision: string;
  graph: WorkflowGraph;
  published_by?: string;
  published_at?: string;
};

/**
 * Workflow 的可读元数据（控制面 `workflows.json` 的 `meta` 段）。
 *
 * 读取端是对话式任务创建时的自动选择；`recommended` / `match_score` 是
 * `/api/workflows/available?user_input=` 的**瞬时**回包字段，不落库。
 */
export type WorkflowMeta = {
  id: string;
  name: string;
  description: string;
  use_cases: string[];
  tags: string[];
  auto_select_enabled?: boolean;
  recommended?: boolean;
  match_score?: number;
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
  // 末端 human_gate 没有可批的下游，审批只能拦在执行中间（G-MY；服务端
  // ValidateWorkflowDefinitionForAuthoring 同一条规则，画布先拦一道）。
  const hasOutgoing = new Set((graph?.edges ?? []).map((e) => e.from));
  for (const n of nodes) {
    if (n.kind !== "human_gate" || !n.id || hasOutgoing.has(n.id)) continue;
    errors.push(`人工确认（human_gate）节点 ${n.id} 不能是最后一个节点：它后面必须还有节点，否则批了也不会发生任何事`);
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

