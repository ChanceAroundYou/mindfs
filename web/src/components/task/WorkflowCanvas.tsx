import React, { useCallback, useEffect, useMemo, useState, useRef } from "react";
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
import { panelButtonStyle, panelSurfaceStyle } from "../shell/PanelShell";
import {
  NODE_KINDS,
  newNode,
  validateWorkflowGraph,
  type WorkflowGraph,
  type WorkflowNodeDef,
  type WorkflowNodeKind,
} from "../../shared/workflowModel";
import { flowEdges, flowNodes, graphFrom } from "./workflowAuthoring/flowGraph";
import { WorkflowLoopCanvas } from "./workflowAuthoring/WorkflowLoopCanvas";
import type { AgentStatus } from "../../shared/agentTypes";
import { nodeTypes } from "./WorkflowNode";
import { WorkflowNodeIcon } from "./WorkflowNodeIcons";
import { WorkflowNodeInspector } from "./WorkflowNodeInspector";
import { getNodeStyle } from "./workflowNodeStyles";

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
  /** Agent 状态列表，用于 AgentSelector */
  agents?: AgentStatus[];
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

function WorkflowCanvasInner({ draftId, graph, onChange, dirty, readOnly, agents = [] }: WorkflowCanvasProps) {
  const { t } = useI18n();
  const [narrow] = useState(isNarrow);
  const [touched, setTouched] = useState(false);
  const [selectedId, setSelectedId] = useState<string>("");
  const [openLoopId, setOpenLoopId] = useState<string>("");
  const [inspectorPosition, setInspectorPosition] = useState<{ x: number; y: number } | null>(null);
  const inspectorRef = useRef<HTMLDivElement>(null);

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

  const handleNodeClick = useCallback((event: React.MouseEvent, node: Node) => {
    setSelectedId(node.id);
    // 计算浮动窗口位置：智能定位，避免超出屏幕
    const rect = (event.target as HTMLElement).getBoundingClientRect?.();
    if (rect) {
      const viewportWidth = window.innerWidth;
      const viewportHeight = window.innerHeight;
      const inspectorWidth = 400; // 浮动窗口最大宽度
      const inspectorHeight = 500; // 预估高度

      let x = rect.right + 10;
      let y = rect.top;

      // 如果右侧空间不够，放到左侧
      if (x + inspectorWidth > viewportWidth) {
        x = rect.left - inspectorWidth - 10;
        // 如果左侧也不够，就放在可视区域内
        if (x < 10) {
          x = Math.max(10, viewportWidth - inspectorWidth - 10);
        }
      }

      // 如果超出底部，向上调整
      if (y + inspectorHeight > viewportHeight) {
        y = Math.max(10, viewportHeight - inspectorHeight - 10);
      }

      setInspectorPosition({ x, y });
    }
  }, []);

  const handleNodeDoubleClick = useCallback((event: React.MouseEvent, node: Node) => {
    if ((graph.nodes || []).some((d) => d.id === node.id && d.kind === "loop")) {
      setOpenLoopId(node.id);
    }
  }, [graph.nodes]);

  const closeInspector = useCallback(() => {
    setSelectedId("");
    setInspectorPosition(null);
  }, []);

  const emit = useCallback(
    (nextNodes: Node[], nextEdges: Edge[]) => {
      setTouched(true);
      onChange(graphFrom(nextNodes, nextEdges, graph));
    },
    [graph, onChange],
  );

  // 删除节点。必须声明在下面的「点击外部关闭 + 键盘快捷键」之前：那个 effect 的
  // 依赖数组与 Delete 分支都引用它，放到后面会用到未初始化的 const。
  const removeSelected = useCallback(() => {
    if (!selectedId) return;
    const nextNodes = nodes.filter((n) => n.id !== selectedId);
    const nextEdges = edges.filter((e) => e.source !== selectedId && e.target !== selectedId);
    setNodes(nextNodes);
    setEdges(nextEdges);
    setSelectedId("");
    emit(nextNodes, nextEdges);
  }, [selectedId, nodes, edges, emit, setNodes, setEdges]);

  // 点击画布外部关闭浮动窗口 + 键盘快捷键
  useEffect(() => {
    if (!inspectorPosition) return;
    const handleClickOutside = (event: MouseEvent) => {
      // 这里的 Node 是 @xyflow/react 的节点类型，不能拿 DOM 的 Node 断言；
      // 先用 instanceof Element 收窄 EventTarget。
      if (inspectorRef.current && event.target instanceof Element && !inspectorRef.current.contains(event.target)) {
        closeInspector();
      }
    };
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        closeInspector();
      } else if (event.key === "Delete" && selectedId) {
        removeSelected();
      }
    };
    document.addEventListener("mousedown", handleClickOutside);
    document.addEventListener("keydown", handleKeyDown);
    return () => {
      document.removeEventListener("mousedown", handleClickOutside);
      document.removeEventListener("keydown", handleKeyDown);
    };
  }, [inspectorPosition, closeInspector, selectedId, removeSelected]);

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
      // 改为竖向排列：固定 x，每个新节点 y 向下递增
      const nextNodes = [
        ...nodes,
        { id: def.id, position: { x: 250, y: 100 + nodes.length * 120 }, data: { label: `${labels[kind]}: ${def.id}` } },
      ];
      setNodes(nextNodes);
      setSelectedId(def.id);
      emit(nextNodes, edges);
      onChange(graphFrom(nextNodes, edges, { ...graph, nodes: [...(graph.nodes || []), def] }));
    },
    [nodes, edges, labels, emit, onChange, graph, setNodes],
  );

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
          <div key={n.id} style={{ ...panelSurfaceStyle(), padding: "6px 8px", fontSize: "12px" }}>
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
        {NODE_KINDS.map((k) => {
          const style = getNodeStyle(k);
          return (
            <button
              key={k}
              type="button"
              onClick={() => addNode(k)}
              style={{
                ...panelButtonStyle("secondary"),
                display: "flex",
                alignItems: "center",
                gap: "6px",
                color: style.color,
                borderColor: style.borderColor,
              }}
            >
              <WorkflowNodeIcon kind={k} size={16} />
              {labels[k]}
            </button>
          );
        })}
        <button
          type="button"
          disabled={!selectedId}
          onClick={removeSelected}
          style={{ ...panelButtonStyle("danger"), opacity: selectedId ? 1 : 0.45, cursor: selectedId ? "pointer" : "not-allowed" }}
        >
          {t("task.workflowCanvas.removeNode")}
        </button>
      </div>

      <div style={{ ...panelSurfaceStyle(), padding: 0, height: "520px", overflow: "hidden", position: "relative" }}>
        <ReactFlow
          nodes={nodes}
          edges={edges}
          onNodesChange={onNodesChange}
          onEdgesChange={onEdgesChange}
          onConnect={onConnect}
          onNodeClick={handleNodeClick}
          onNodeDoubleClick={handleNodeDoubleClick}
          onNodesDelete={(deleted) => emit(nodes.filter((n) => !deleted.some((d) => d.id === n.id)), edges)}
          nodeTypes={nodeTypes}
          defaultEdgeOptions={{
            style: { stroke: "var(--accent-color, #3b82f6)", strokeWidth: 2 },
            animated: true,
            markerEnd: { type: "arrowclosed" as const, color: "var(--accent-color, #3b82f6)" },
          }}
          fitView
          fitViewOptions={{ padding: 0.2, maxZoom: 1 }}
        >
          <Background />
          <Controls />
        </ReactFlow>
        {/* 提示：快捷键 */}
        <div style={{
          position: "absolute",
          bottom: "8px",
          right: "8px",
          fontSize: "11px",
          color: "var(--text-secondary)",
          background: "var(--button-bg)",
          padding: "4px 8px",
          borderRadius: "4px",
          opacity: 0.7,
        }}>
          Esc: 关闭 | Delete: 删除节点
        </div>
      </div>

      {selected && inspectorPosition ? (
        <WorkflowNodeInspector
          node={selected}
          position={inspectorPosition}
          inspectorRef={inspectorRef}
          labels={labels}
          dirty={dirtyNow}
          agents={agents}
          onClose={closeInspector}
          onPatch={patchSelected}
          onOpenLoop={() => setOpenLoopId(selected.id)}
        />
      ) : null}
        </>
      )}

      {errors.length > 0 ? (
        <div style={{
          ...panelSurfaceStyle(),
          padding: "12px",
          background: "var(--danger-background, #fee)",
          border: "1px solid var(--danger-color, #ef4444)",
          borderRadius: "6px",
        }}>
          <div style={{ fontSize: "12px", fontWeight: 600, color: "var(--danger-color, #ef4444)", marginBottom: "6px" }}>
            ⚠️ 图定义错误
          </div>
          <ul data-testid="workflow-canvas-errors" style={{ margin: 0, paddingLeft: "18px", fontSize: "12px", color: "var(--danger-color, #ef4444)" }}>
            {errors.map((e) => (
              <li key={e}>{e}</li>
            ))}
          </ul>
        </div>
      ) : null}
    </div>
  );
}
