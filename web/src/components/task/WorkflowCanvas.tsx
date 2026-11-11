import React, { useCallback, useEffect, useMemo, useState } from "react";
import {
  Background,
  Controls,
  ReactFlow,
  ReactFlowProvider,
  addEdge,
  useEdgesState,
  useNodesState,
  type Connection,
  type Edge,
  type Node,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { useI18n } from "../../i18n/index";
import {
  LOOP_DEFAULT,
  LOOP_MAX,
  LOOP_MIN,
  NODE_KINDS,
  newNode,
  validateWorkflowGraph,
  type WorkflowGraph,
  type WorkflowNodeDef,
  type WorkflowNodeKind,
} from "../../shared/workflowModel";
import { flowEdges, flowNodes, graphFrom } from "./workflowAuthoring/flowGraph";
import { WorkflowLoopCanvas } from "./workflowAuthoring/WorkflowLoopCanvas";

// 工作流草稿画布（WP-07）：**桌面端**的 Draft 图编辑器。
//
// 职责边界：画布只管「图长什么样、谁连谁、每个节点怎么配」。校验用
// services/task/workflowDraft 的同构规则（与服务端 workflow_validate.go 对齐），
// 落库由父层调 A5 的 Draft API。本组件不 import fetch —— 无副作用、可单测。
//
// 移动端只读：视口窄于 720px 时不渲染画布，而是渲染按图顺序的只读清单
// （触屏拖拽体验差；spec 明确「移动端简单能看就行」）。
//
// 脏标记：只有结构性编辑（增删节点、增删边、改参数）才置脏；拖动节点位置不算
// —— 位置是展示信息，不该逼用户为「挪了一下」去保存。脏时标题右侧显示红点，
// 并在 beforeunload 上挂离开提醒（关闭/刷新）；页面内切走由父层确认。

export type WorkflowCanvasProps = {
  draftId: string;
  graph: WorkflowGraph;
  onChange: (graph: WorkflowGraph) => void;
  /** 父层权威脏状态；缺省用内部编辑计数。 */
  dirty?: boolean;
  readOnly?: boolean;
};

const MOBILE_BREAKPOINT = 720;

function isNarrow(): boolean {
  if (typeof window === "undefined" || typeof window.innerWidth !== "number") return false;
  return window.innerWidth < MOBILE_BREAKPOINT;
}

export function WorkflowCanvas(props: WorkflowCanvasProps) {
  return (
    <ReactFlowProvider>
      <WorkflowCanvasInner {...props} />
    </ReactFlowProvider>
  );
}

function WorkflowCanvasInner({ draftId, graph, onChange, dirty, readOnly }: WorkflowCanvasProps) {
  const { t } = useI18n();
  const [narrow] = useState(isNarrow);
  const [touched, setTouched] = useState(false);
  const [selectedId, setSelectedId] = useState<string>("");
  const [openLoopId, setOpenLoopId] = useState<string>("");

  const labels = useMemo(() => {
    const out: Record<string, string> = {};
    for (const k of NODE_KINDS) out[k] = t(`task.workflowCanvas.kind.${k}`);
    return out;
  }, [t]);

  const [nodes, setNodes, onNodesChange] = useNodesState(flowNodes(graph, labels));
  const [edges, setEdges, onEdgesChange] = useEdgesState(flowEdges(graph));

  // 只有外部 graph 的结构（节点/边 id 集合与参数）变了才重建 flow 状态，
  // 否则每次 onChange 回灌都会把用户正在拖的位置重置。
  const structureKey = useMemo(
    () => JSON.stringify({ n: (graph.nodes || []).map((n) => [n.id, n.kind, n.name, n.params]), e: graph.edges || [] }),
    [graph],
  );
  useEffect(() => {
    setNodes(flowNodes(graph, labels));
    setEdges(flowEdges(graph));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [structureKey]);

  const dirtyNow = dirty ?? touched;

  useEffect(() => {
    if (!dirtyNow || typeof window === "undefined") return;
    const onBeforeUnload = (e: BeforeUnloadEvent) => {
      e.preventDefault();
      e.returnValue = "";
    };
    window.addEventListener("beforeunload", onBeforeUnload);
    return () => window.removeEventListener("beforeunload", onBeforeUnload);
  }, [dirtyNow]);

  const emit = useCallback(
    (nextNodes: Node[], nextEdges: Edge[]) => {
      setTouched(true);
      onChange(graphFrom(nextNodes, nextEdges, graph));
    },
    [graph, onChange],
  );

  const onConnect = useCallback(
    (c: Connection) => {
      setEdges((es) => {
        const next = addEdge(c, es);
        emit(nodes, next);
        return next;
      });
    },
    [emit, nodes, setEdges],
  );

  const addNode = useCallback(
    (kind: WorkflowNodeKind) => {
      const def = newNode(kind);
      const nextNodes = [
        ...nodes,
        { id: def.id, position: { x: 120 + nodes.length * 30, y: 140 + nodes.length * 20 }, data: { label: `${labels[kind]}: ${def.id}` } },
      ];
      setNodes(nextNodes);
      setSelectedId(def.id);
      emit(nextNodes, edges);
      onChange(graphFrom(nextNodes, edges, { ...graph, nodes: [...(graph.nodes || []), def] }));
    },
    [nodes, edges, labels, emit, onChange, graph, setNodes],
  );

  const removeSelected = useCallback(() => {
    if (!selectedId) return;
    const nextNodes = nodes.filter((n) => n.id !== selectedId);
    const nextEdges = edges.filter((e) => e.source !== selectedId && e.target !== selectedId);
    setNodes(nextNodes);
    setEdges(nextEdges);
    setSelectedId("");
    emit(nextNodes, nextEdges);
  }, [selectedId, nodes, edges, emit, setNodes, setEdges]);

  const errors = useMemo(() => validateWorkflowGraph(graph), [graph]);
  const selected = (graph.nodes || []).find((n) => n.id === selectedId) || null;
  // 子画布只对仍然存在的 Loop 生效；节点被删除或改类型后自动回到顶层图。
  const openLoop = openLoopId
    ? (graph.nodes || []).find((n) => n.id === openLoopId && n.kind === "loop") || null
    : null;

  const replaceNode = useCallback(
    (next: WorkflowNodeDef) => {
      setTouched(true);
      onChange({ ...graph, nodes: (graph.nodes || []).map((n) => (n.id === next.id ? next : n)) });
    },
    [graph, onChange],
  );

  const patchSelected = useCallback(
    (patch: Partial<WorkflowNodeDef>) => {
      if (!selected) return;
      const nextGraph: WorkflowGraph = {
        ...graph,
        nodes: (graph.nodes || []).map((n) => (n.id === selected.id ? { ...n, ...patch } : n)),
      };
      setTouched(true);
      onChange(nextGraph);
    },
    [graph, onChange, selected],
  );

  if (narrow || readOnly) {
    return (
      <div data-testid="workflow-canvas-readonly" style={{ display: "flex", flexDirection: "column", gap: "6px" }}>
        <div style={{ fontSize: "12px", color: "var(--text-secondary)" }}>{t("task.workflowCanvas.mobileHint")}</div>
        {(graph.nodes || []).map((n) => (
          <div key={n.id} style={{ border: "1px solid var(--border-color)", borderRadius: "6px", padding: "6px 8px", fontSize: "12px" }}>
            <strong>{labels[n.kind] || n.kind}</strong> {n.name || n.id}
          </div>
        ))}
        {(graph.edges || []).length > 0 ? (
          <div style={{ fontSize: "12px", color: "var(--text-secondary)" }}>
            {graph.edges!.map((e) => `${e.from} → ${e.to}${e.route ? ` (${e.route})` : ""}`).join("，")}
          </div>
        ) : null}
      </div>
    );
  }

  return (
    <div data-testid="workflow-canvas" data-draft-id={draftId} style={{ display: "flex", flexDirection: "column", gap: "8px" }}>
      <div style={{ display: "flex", alignItems: "center", gap: "8px", flexWrap: "wrap" }}>
        <span style={{ fontSize: "12px", fontWeight: 600 }}>{t("task.workflowCanvas.title")}</span>
        {dirtyNow ? (
          <span
            data-testid="workflow-canvas-dirty"
            title={t("task.workflowCanvas.unsaved")}
            style={{ width: "7px", height: "7px", borderRadius: "50%", background: "var(--danger-color, #ef4444)" }}
          />
        ) : null}
        <span style={{ flex: 1 }} />
      </div>

      {openLoop ? (
        <WorkflowLoopCanvas loop={openLoop} onChange={replaceNode} onBack={() => setOpenLoopId("")} />
      ) : (
        <>
      <div style={{ display: "flex", alignItems: "center", gap: "8px", flexWrap: "wrap" }}>
        <span style={{ flex: 1 }} />
        {NODE_KINDS.map((k) => (
          <button
            key={k}
            type="button"
            onClick={() => addNode(k)}
            style={{ border: "1px solid var(--border-color)", background: "transparent", borderRadius: "6px", padding: "2px 8px", fontSize: "12px", cursor: "pointer" }}
          >
            + {labels[k]}
          </button>
        ))}
        <button
          type="button"
          disabled={!selectedId}
          onClick={removeSelected}
          style={{ border: "1px solid var(--border-color)", background: "transparent", borderRadius: "6px", padding: "2px 8px", fontSize: "12px", cursor: selectedId ? "pointer" : "not-allowed" }}
        >
          {t("task.workflowCanvas.removeNode")}
        </button>
      </div>

      <div style={{ height: "360px", border: "1px solid var(--border-color)", borderRadius: "8px", overflow: "hidden" }}>
        <ReactFlow
          nodes={nodes}
          edges={edges}
          onNodesChange={onNodesChange}
          onEdgesChange={onEdgesChange}
          onConnect={onConnect}
          onNodeClick={(_, n) => setSelectedId(n.id)}
          onNodeDoubleClick={(_, n) => {
            if ((graph.nodes || []).some((d) => d.id === n.id && d.kind === "loop")) setOpenLoopId(n.id);
          }}
          onNodesDelete={(deleted) => emit(nodes.filter((n) => !deleted.some((d) => d.id === n.id)), edges)}
          fitView
        >
          <Background />
          <Controls />
        </ReactFlow>
      </div>

      {selected ? (
        <div data-testid="workflow-canvas-inspector" style={{ display: "flex", flexDirection: "column", gap: "6px", fontSize: "12px" }}>
          <div style={{ display: "flex", gap: "6px", alignItems: "center" }}>
            <span>{t("task.workflowCanvas.name")}</span>
            <input
              value={selected.name || ""}
              onChange={(e) => patchSelected({ name: e.target.value })}
              style={{ flex: 1, border: "1px solid var(--border-color)", borderRadius: "6px", padding: "2px 6px", background: "transparent", color: "inherit" }}
            />
          </div>
          {selected.kind === "agent" ? (
            <>
              <div style={{ display: "flex", gap: "6px", alignItems: "center" }}>
                <span>{t("task.workflowCanvas.agent")}</span>
                <input
                  value={String(selected.params?.agent ?? "")}
                  onChange={(e) => patchSelected({ params: { ...selected.params, agent: e.target.value } })}
                  style={{ flex: 1, border: "1px solid var(--border-color)", borderRadius: "6px", padding: "2px 6px", background: "transparent", color: "inherit" }}
                />
                <span>{t("task.workflowCanvas.model")}</span>
                <input
                  value={String(selected.params?.model ?? "")}
                  onChange={(e) => patchSelected({ params: { ...selected.params, model: e.target.value } })}
                  style={{ flex: 1, border: "1px solid var(--border-color)", borderRadius: "6px", padding: "2px 6px", background: "transparent", color: "inherit" }}
                />
              </div>
              <label style={{ display: "flex", gap: "6px", alignItems: "center" }}>
                <input
                  type="checkbox"
                  checked={selected.params?.writes_workspace !== false}
                  onChange={(e) => patchSelected({ params: { ...selected.params, writes_workspace: e.target.checked } })}
                />
                {t("task.workflowCanvas.writesWorkspace")}
              </label>
            </>
          ) : null}
          {selected.kind === "loop" ? (
            <>
              <label style={{ display: "flex", gap: "6px", alignItems: "center" }}>
                {t("task.workflowCanvas.maxIterations")}
                <input
                  type="number"
                  min={LOOP_MIN}
                  max={LOOP_MAX}
                  value={Number(selected.params?.max_iterations ?? LOOP_DEFAULT)}
                  onChange={(e) => patchSelected({ params: { ...selected.params, max_iterations: Number(e.target.value) } })}
                  style={{ width: "72px", border: "1px solid var(--border-color)", borderRadius: "6px", padding: "2px 6px", background: "transparent", color: "inherit" }}
                />
              </label>
              <label style={{ display: "flex", gap: "6px", alignItems: "center" }}>
                {t("task.workflowCanvas.exitExpression")}
                <input
                  data-testid="workflow-loop-exit"
                  value={String(selected.params?.exit_expression ?? "")}
                  placeholder={t("task.workflowCanvas.exitPlaceholder")}
                  onChange={(e) => patchSelected({ params: { ...selected.params, exit_expression: e.target.value } })}
                  style={{ flex: 1, border: "1px solid var(--border-color)", borderRadius: "6px", padding: "2px 6px", background: "transparent", color: "inherit" }}
                />
              </label>
              <div style={{ fontSize: "11px", color: "var(--text-secondary)" }}>{t("task.workflowCanvas.exitHint")}</div>
              <button
                type="button"
                data-testid="workflow-loop-enter"
                onClick={() => setOpenLoopId(selected.id)}
                style={{ alignSelf: "flex-start", border: "1px solid var(--border-color)", background: "transparent", borderRadius: "6px", padding: "2px 8px", cursor: "pointer" }}
              >
                {t("task.workflowCanvas.editInnerGraph")}
              </button>
            </>
          ) : null}
          {selected.kind === "condition" ? (
            <div style={{ display: "flex", gap: "6px", alignItems: "center" }}>
              <span>{t("task.workflowCanvas.routes")}</span>
              <input
                value={((selected.params?.routes as string[] | undefined) ?? []).join(",")}
                onChange={(e) =>
                  patchSelected({
                    params: { ...selected.params, routes: e.target.value.split(",").map((s) => s.trim()).filter(Boolean) },
                  })
                }
                style={{ flex: 1, border: "1px solid var(--border-color)", borderRadius: "6px", padding: "2px 6px", background: "transparent", color: "inherit" }}
              />
            </div>
          ) : null}
        </div>
      ) : null}
        </>
      )}

      {errors.length > 0 ? (
        <ul data-testid="workflow-canvas-errors" style={{ margin: 0, paddingLeft: "16px", fontSize: "12px", color: "var(--danger-color, #ef4444)" }}>
          {errors.map((e) => (
            <li key={e}>{e}</li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}
