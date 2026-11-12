import React from "react";
import { Handle, Position } from "@xyflow/react";
import { WorkflowNodeIcon } from "./WorkflowNodeIcons";
import type { WorkflowNodeKind } from "../../shared/workflowModel";

export type WorkflowNodeData = {
  label: string;
  kind: WorkflowNodeKind;
};

export function WorkflowNode({ data }: { data: WorkflowNodeData }) {
  return (
    <>
      <Handle type="target" position={Position.Left} />
      <div style={{ display: "flex", alignItems: "center", gap: "8px" }}>
        <WorkflowNodeIcon kind={data.kind} size={18} />
        <span>{data.label}</span>
      </div>
      <Handle type="source" position={Position.Right} />
    </>
  );
}

// 用于注册自定义节点类型
export const nodeTypes = {
  workflowNode: WorkflowNode,
};
