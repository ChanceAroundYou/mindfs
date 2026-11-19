import React, { useCallback, useEffect, useMemo, useState, useRef } from "react";
import { createPortal } from "react-dom";
import {
  Background,
  Controls,
  ReactFlow,
  ReactFlowProvider,
  addEdge,
  useEdgesState,
  useNodesState,
  useReactFlow,
  type Connection,
  type Edge,
  type Node,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { useI18n } from "../../i18n/index";
import { panelButtonStyle, panelSurfaceStyle } from "../shell/PanelShell";
import {
  NODE_KINDS,
  loopInnerGraph,
  newNode,
  validateWorkflowGraph,
  workflowNodeConfigText,
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
import { nodeScreenRect, placeInspectorNear } from "./workflowInspectorPlacement";
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
  const { fitView } = useReactFlow();
  // 断点判定必须跟着 resize 走：只取一次快照的话，窄窗口打开后再放大，
  // 画布永远停在只读清单上（看起来就像「节点看不见、也拖不动」）。
  const [narrow, setNarrow] = useState(isNarrow);
  const [touched, setTouched] = useState(false);
  const [selectedId, setSelectedId] = useState<string>("");
  const [openLoopId, setOpenLoopId] = useState<string>("");
  const [inspectorPosition, setInspectorPosition] = useState<{ x: number; y: number } | null>(null);
  const inspectorRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const onResize = () => setNarrow(isNarrow());
    window.addEventListener("resize", onResize);
    onResize();
    return () => window.removeEventListener("resize", onResize);
  }, []);

  const fitSoon = useCallback(() => {
    // 新节点要等一帧才被测出尺寸；不重算视口的话，第 5 个及以后的节点
    // 会落在 520px 画布之外 —— 那正是「加完节点却看不见」的第二个来源。
    requestAnimationFrame(() => {
      void fitView({ padding: 0.2, maxZoom: 1, duration: 200 });
    });
  }, [fitView]);

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
    // 窄屏（手机）上卡片换成底部抽屉：不做「贴着节点」的定位数学，抽屉固定贴底。
    if (narrow) {
      setInspectorPosition({ x: 0, y: 0 });
      return;
    }
    // 锚点取整个节点框（点到的可能是内部图标/文字），不是被点中的那个子元素。
    const target = event.target instanceof Element ? event.target.closest(".react-flow__node") : null;
    const rect = target?.getBoundingClientRect() ?? nodeScreenRect(document, node.id);
    if (rect) {
      setInspectorPosition(placeInspectorNear(rect, { width: window.innerWidth, height: window.innerHeight }));
    }
  }, [narrow]);

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

  // 点画布外部只关卡片，**不清选中**。清选中会让「删除选中节点」按钮在
  // mousedown 与 mouseup 之间变成 disabled，浏览器随后就不再派发 click ——
  // 那正是「删除按钮按了没反应」的根因。（清选中只由 Esc / 关闭键 / 删除负责。）
  useEffect(() => {
    if (!inspectorPosition) return;
    const handleClickOutside = (event: MouseEvent) => {
      // 这里的 Node 是 @xyflow/react 的节点类型，不能拿 DOM 的 Node 断言；
      // 先用 instanceof Element 收窄 EventTarget。
      if (inspectorRef.current && event.target instanceof Element && !inspectorRef.current.contains(event.target)) {
        setInspectorPosition(null);
      }
    };
    document.addEventListener("mousedown", handleClickOutside);
    return () => document.removeEventListener("mousedown", handleClickOutside);
  }, [inspectorPosition]);

  // 快捷键与卡片开合无关：Esc 关卡片，Delete 删选中节点。Delete 必须避开
  // 输入框，否则在 prompt 里退格删字会顺手把节点删掉。
  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        closeInspector();
        return;
      }
      if (event.key !== "Delete" || !selectedId) return;
      const target = event.target;
      if (target instanceof HTMLElement && (target.isContentEditable || ["INPUT", "TEXTAREA", "SELECT"].includes(target.tagName))) {
        return;
      }
      removeSelected();
    };
    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, [closeInspector, selectedId, removeSelected]);

  // 画布平移/缩放后节点在屏幕上换了位置，卡片得跟着走；否则用户看到的就是
  // 「卡片还留在原地」。每帧最多重算一次，拖动视口时不至于每次都 setState。
  const viewportFrame = useRef(0);
  const handleViewportMove = useCallback(() => {
    // 抽屉模式不贴节点，平移/缩放不需要重锚。
    if (!selectedId || narrow || viewportFrame.current) return;
    viewportFrame.current = requestAnimationFrame(() => {
      viewportFrame.current = 0;
      const rect = nodeScreenRect(document, selectedId);
      if (rect) setInspectorPosition(placeInspectorNear(rect, { width: window.innerWidth, height: window.innerHeight }));
    });
  }, [selectedId, narrow]);
  useEffect(
    () => () => {
      if (viewportFrame.current) cancelAnimationFrame(viewportFrame.current);
    },
    [],
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
      // 与 flowNodes 的默认排布同一套网格：4 列换行，新节点永远不叠在已有节点上。
      const index = nodes.length;
      const nextNodes = [
        ...nodes,
        {
          id: def.id,
          position: { x: 80 + (index % 4) * 230, y: 80 + Math.floor(index / 4) * 150 },
          data: { label: labels[kind] ?? kind, kind },
        },
      ];
      setNodes(nextNodes);
      setSelectedId(def.id);
      setTouched(true);
      // 只发一次 onChange：graphFrom 的 kind/params 来自 prev，所以 prev 必须已经带上新定义。
      onChange(graphFrom(nextNodes, edges, { ...graph, nodes: [...(graph.nodes || []), def] }));
      fitSoon();
    },
    [nodes, edges, labels, onChange, graph, setNodes, fitSoon],
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

  // 只读模式保留成显式开关（父层不再按移动端传它）：窄屏现在是可编辑的，
  // 尺寸只影响排布，不影响能力。
  if (readOnly) {
    return (
      <div data-testid="workflow-canvas-readonly" style={{ display: "flex", flexDirection: "column", gap: "6px" }}>
        <div style={{ fontSize: "12px", color: "var(--text-secondary)" }}>{t("task.workflowCanvas.readOnlyHint")}</div>
        {(graph.nodes || []).map((n) => {
          const config = workflowNodeConfigText(n);
          const innerNodes = n.kind === "loop" ? loopInnerGraph(n)?.nodes ?? [] : [];
          return (
            <div key={n.id} data-testid="workflow-readonly-node" style={{ ...panelSurfaceStyle(), padding: "6px 8px", fontSize: "12px", display: "flex", flexDirection: "column", gap: 4 }}>
              <div><strong>{labels[n.kind] || n.kind}</strong> {n.name || n.id}</div>
              {config ? (
                <div style={{ color: "var(--text-secondary)", whiteSpace: "pre-wrap", wordBreak: "break-word" }}>{config}</div>
              ) : null}
              {innerNodes.map((inner) => {
                const innerConfig = workflowNodeConfigText(inner);
                return (
                  <div key={inner.id} style={{ paddingLeft: 12, display: "flex", flexDirection: "column", gap: 2 }}>
                    <div><strong>{labels[inner.kind] || inner.kind}</strong> {inner.name || inner.id}</div>
                    {innerConfig ? (
                      <div style={{ color: "var(--text-secondary)", whiteSpace: "pre-wrap", wordBreak: "break-word" }}>{innerConfig}</div>
                    ) : null}
                  </div>
                );
              })}
            </div>
          );
        })}
        {(graph.edges || []).length > 0 ? (
          <div style={{ fontSize: "12px", color: "var(--text-secondary)" }}>
            {graph.edges!.map((e) => `${e.from} → ${e.to}${e.route ? ` (${e.route})` : ""}`).join("，")}
          </div>
        ) : null}
      </div>
    );
  }

  return (
    <div data-testid="workflow-canvas" data-draft-id={draftId} style={{ display: "flex", flexDirection: "column", gap: "8px", height: narrow ? "auto" : "100%", minHeight: 0 }}>
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
              aria-label={labels[k]}
              title={labels[k]}
              onClick={() => addNode(k)}
              style={{
                ...panelButtonStyle("secondary"),
                display: "flex",
                alignItems: "center",
                gap: narrow ? 0 : "6px",
                padding: narrow ? "6px 8px" : undefined,
                color: style.color,
                borderColor: style.borderColor,
              }}
            >
              <WorkflowNodeIcon kind={k} size={16} />
              {narrow ? null : labels[k]}
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

      {/* 画布吃掉剩余高度（下限 360px）：固定 520px 在矮窗口里会被挤到折叠线以下，
          看着就像「显示不完整」。父列有确定高度时 flex 生效，没有时退回 minHeight。
          手机上父列是自然高度（整页滚动），这里改成固定高度，否则 flex 解析成 0。 */}
      <div style={{ ...panelSurfaceStyle(), padding: 0, flex: narrow ? "none" : 1, height: narrow ? "min(58vh, 520px)" : undefined, minHeight: narrow ? "320px" : "360px", overflow: "hidden", position: "relative" }}>
        <ReactFlow
          nodes={nodes}
          edges={edges}
          onNodesChange={onNodesChange}
          onEdgesChange={onEdgesChange}
          onConnect={onConnect}
          onNodeClick={handleNodeClick}
          onNodeDoubleClick={handleNodeDoubleClick}
          onMove={handleViewportMove}
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
        {/* 提示：桌面写快捷键，手机写触屏手势（手机没有 Esc/Delete 键）。 */}
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
          {narrow ? t("task.workflowCanvas.touchHint") : "Esc: 关闭 | Delete: 删除节点"}
        </div>
      </div>

      {/* portal 到 body：祖先 <main> 的 contain: paint 会给 position: fixed 建立
          包含块，不逃出去卡片就会整体偏移一个侧边栏宽度。 */}
      {selected && inspectorPosition
        ? createPortal(
            <WorkflowNodeInspector
              node={selected}
              position={inspectorPosition}
              layout={narrow ? "sheet" : "anchored"}
              inspectorRef={inspectorRef}
              labels={labels}
              dirty={dirtyNow}
              agents={agents}
              onClose={closeInspector}
              onPatch={patchSelected}
              onOpenLoop={() => setOpenLoopId(selected.id)}
            />,
            document.body,
          )
        : null}
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
