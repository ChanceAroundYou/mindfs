import React, { useCallback, useEffect, useMemo, useState } from "react";
import {
  Background,
  Controls,
  ReactFlow,
  addEdge,
  useEdgesState,
  useNodesState,
  type Connection,
  type Edge,
  type Node,
} from "@xyflow/react";
import { useI18n } from "../../../i18n/index";
import {
  newNode,
  withLoopInnerGraph,
  loopInnerGraph,
  type WorkflowGraph,
  type WorkflowNodeDef,
  type WorkflowNodeKind,
} from "../../../shared/workflowModel";
import { flowEdges, flowNodes, graphFrom } from "./flowGraph";

// Loop 内部图编辑器：独立子画布，但仍写回同一个 `params.graph`。
// 只渲染内部节点，因此连线不可能跨容器；嵌套 Loop 与人工确认节点不提供入口，
// 服务端/前端校验也会拒绝手工构造出的这两种结构。

const INNER_KINDS: WorkflowNodeKind[] = ["agent", "condition", "notify"];

export type WorkflowLoopCanvasProps = {
  loop: WorkflowNodeDef;
  onChange: (next: WorkflowNodeDef) => void;
  onBack: () => void;
};

export function WorkflowLoopCanvas({ loop, onChange, onBack }: WorkflowLoopCanvasProps) {
  const { t } = useI18n();
  const inner = useMemo<WorkflowGraph>(() => loopInnerGraph(loop) ?? { nodes: [], edges: [] }, [loop]);
  const labels = useMemo(() => {
    const out: Record<string, string> = {};
    for (const k of INNER_KINDS) out[k] = t(`task.workflowCanvas.kind.${k}`);
    return out;
  }, [t]);

  const [nodes, setNodes, onNodesChange] = useNodesState(flowNodes(inner, labels));
  const [edges, setEdges, onEdgesChange] = useEdgesState(flowEdges(inner));
  const [selectedId, setSelectedId] = useState("");

  const structureKey = useMemo(
    () => `${loop.id}:${JSON.stringify({ n: inner.nodes.map((n) => [n.id, n.kind, n.name, n.params]), e: inner.edges || [] })}`,
    [loop.id, inner],
  );
  useEffect(() => {
    setNodes(flowNodes(inner, labels));
    setEdges(flowEdges(inner));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [structureKey]);

  const emit = useCallback(
    (nextNodes: Node[], nextEdges: Edge[], prev: WorkflowGraph = inner) => {
      onChange(withLoopInnerGraph(loop, graphFrom(nextNodes, nextEdges, prev)));
    },
    [inner, loop, onChange],
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
        { id: def.id, position: { x: 120 + nodes.length * 30, y: 120 + nodes.length * 20 }, data: { label: `${labels[kind]}: ${def.id}` } },
      ];
      setNodes(nextNodes);
      setSelectedId(def.id);
      emit(nextNodes, edges, { ...inner, nodes: [...inner.nodes, def] });
    },
    [nodes, edges, labels, emit, inner, setNodes],
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

  const selected = inner.nodes.find((n) => n.id === selectedId) || null;

  const patchSelected = useCallback(
    (patch: Partial<WorkflowNodeDef>) => {
      if (!selected) return;
      const nextInner: WorkflowGraph = {
        ...inner,
        nodes: inner.nodes.map((n) => (n.id === selected.id ? { ...n, ...patch } : n)),
      };
      onChange(withLoopInnerGraph(loop, nextInner));
    },
    [inner, loop, onChange, selected],
  );

  return (
    <div data-testid="workflow-loop-canvas" style={{ display: "flex", flexDirection: "column", gap: "8px" }}>
      <div data-testid="workflow-loop-breadcrumb" style={{ display: "flex", alignItems: "center", gap: "8px", fontSize: "12px" }}>
        <button
          type="button"
          onClick={onBack}
          style={{ border: "1px solid var(--border-color)", background: "transparent", borderRadius: "6px", padding: "2px 8px", cursor: "pointer" }}
        >
          ← {t("task.workflowCanvas.loopBack")}
        </button>
        <span>{t("task.workflowCanvas.loopBreadcrumb", { name: loop.name || loop.id })}</span>
      </div>

      <div style={{ display: "flex", alignItems: "center", gap: "8px", flexWrap: "wrap" }}>
        <div data-testid="workflow-loop-add" style={{ display: "flex", gap: "6px", flexWrap: "wrap" }}>
          {INNER_KINDS.map((k) => (
            <button
              key={k}
              type="button"
              onClick={() => addNode(k)}
              style={{ border: "1px solid var(--border-color)", background: "transparent", borderRadius: "6px", padding: "2px 8px", fontSize: "12px", cursor: "pointer" }}
            >
              + {labels[k]}
            </button>
          ))}
        </div>
        <button
          type="button"
          disabled={!selectedId}
          onClick={removeSelected}
          style={{ border: "1px solid var(--border-color)", background: "transparent", borderRadius: "6px", padding: "2px 8px", fontSize: "12px", cursor: selectedId ? "pointer" : "not-allowed" }}
        >
          {t("task.workflowCanvas.removeNode")}
        </button>
        <span style={{ fontSize: "11px", color: "var(--text-secondary)" }}>{t("task.workflowCanvas.loopInnerHint")}</span>
      </div>

      <div style={{ height: "300px", border: "1px solid var(--border-color)", borderRadius: "8px", overflow: "hidden" }}>
        <ReactFlow
          nodes={nodes}
          edges={edges}
          onNodesChange={onNodesChange}
          onEdgesChange={onEdgesChange}
          onConnect={onConnect}
          onNodeClick={(_, n) => setSelectedId(n.id)}
          onNodesDelete={(deleted) => emit(nodes.filter((n) => !deleted.some((d) => d.id === n.id)), edges)}
          fitView
        >
          <Background />
          <Controls />
        </ReactFlow>
      </div>

      {selected ? (
        <div data-testid="workflow-loop-inspector" style={{ display: "flex", flexDirection: "column", gap: "6px", fontSize: "12px" }}>
          <div style={{ display: "flex", gap: "6px", alignItems: "center" }}>
            <span>{t("task.workflowCanvas.name")}</span>
            <input
              value={selected.name || ""}
              onChange={(e) => patchSelected({ name: e.target.value })}
              style={{ flex: 1, border: "1px solid var(--border-color)", borderRadius: "6px", padding: "2px 6px", background: "transparent", color: "inherit" }}
            />
          </div>
          {selected.kind === "agent" ? (
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
    </div>
  );
}