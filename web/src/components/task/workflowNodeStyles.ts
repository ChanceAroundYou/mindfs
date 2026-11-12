import type { WorkflowNodeKind } from "../../shared/workflowModel";

// 工作流节点的视觉样式配置

export type NodeStyleConfig = {
  color: string;
  bgColor: string;
  borderColor: string;
  shape: "rectangle" | "diamond" | "circle";
  borderStyle: "solid" | "dashed";
  borderWidth: number;
};

export const NODE_STYLE_CONFIG: Record<WorkflowNodeKind, NodeStyleConfig> = {
  agent: {
    color: "#3b82f6", // 蓝色 - 主要执行节点
    bgColor: "#eff6ff",
    borderColor: "#3b82f6",
    shape: "rectangle",
    borderStyle: "solid",
    borderWidth: 2,
  },
  condition: {
    color: "#f59e0b", // 橙色 - 决策节点
    bgColor: "#fffbeb",
    borderColor: "#f59e0b",
    shape: "diamond",
    borderStyle: "solid",
    borderWidth: 2,
  },
  human_gate: {
    color: "#8b5cf6", // 紫色 - 人工审批
    bgColor: "#f5f3ff",
    borderColor: "#8b5cf6",
    shape: "rectangle",
    borderStyle: "dashed",
    borderWidth: 2,
  },
  loop: {
    color: "#10b981", // 绿色 - 循环节点
    bgColor: "#ecfdf5",
    borderColor: "#10b981",
    shape: "circle",
    borderStyle: "solid",
    borderWidth: 2,
  },
  notify: {
    color: "#06b6d4", // 青色 - 通知节点
    bgColor: "#ecfeff",
    borderColor: "#06b6d4",
    shape: "rectangle",
    borderStyle: "solid",
    borderWidth: 1.5,
  },
};

export function getNodeStyle(kind: WorkflowNodeKind): NodeStyleConfig {
  return NODE_STYLE_CONFIG[kind];
}

// ReactFlow 节点样式生成器
export function generateReactFlowNodeStyle(kind: WorkflowNodeKind) {
  const config = getNodeStyle(kind);
  return {
    background: config.bgColor,
    border: `${config.borderWidth}px ${config.borderStyle} ${config.borderColor}`,
    color: config.color,
    padding: "12px 16px",
    borderRadius: config.shape === "circle" ? "50%" : config.shape === "diamond" ? "4px" : "8px",
    fontSize: "13px",
    fontWeight: 500,
    minWidth: config.shape === "circle" ? "80px" : "120px",
    minHeight: config.shape === "circle" ? "80px" : "auto",
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    boxShadow: "0 2px 8px rgba(0, 0, 0, 0.08)",
    transform: config.shape === "diamond" ? "rotate(45deg)" : "none",
  };
}

// 连接线样式配置
export type EdgeStyleConfig = {
  stroke: string;
  strokeWidth: number;
  animated: boolean;
  type: "default" | "step" | "smoothstep" | "straight";
};

export const EDGE_STYLE_CONFIG: EdgeStyleConfig = {
  stroke: "#94a3b8",
  strokeWidth: 2,
  animated: true,
  type: "smoothstep",
};

// Loop 内部连接线使用不同颜色
export const LOOP_INNER_EDGE_STYLE: EdgeStyleConfig = {
  stroke: "#10b981",
  strokeWidth: 2,
  animated: true,
  type: "smoothstep",
};

// 条件分支连接线
export function getConditionalEdgeStyle(route?: string): Partial<EdgeStyleConfig> {
  if (!route) return {};

  // 根据分支名称使用不同颜色
  if (route.toLowerCase().includes("pass") || route.toLowerCase().includes("success")) {
    return { stroke: "#10b981" }; // 绿色表示成功路径
  }
  if (route.toLowerCase().includes("fail") || route.toLowerCase().includes("error")) {
    return { stroke: "#ef4444" }; // 红色表示失败路径
  }

  return { stroke: "#f59e0b" }; // 橙色表示其他分支
}
