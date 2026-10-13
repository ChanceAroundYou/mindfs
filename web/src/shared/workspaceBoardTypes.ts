import type { TaskOverviewItem } from "./taskTypes";

/** Cross-node task overview item marked with the node that supplied it. */
export type WorkspaceTaskItem = TaskOverviewItem & { nodeId: string };

/** One project group displayed by the cross-project workspace board. */
export type WorkspaceProjectGroup = {
  key: string;
  rootId: string;
  rootName: string;
  nodeId: string;
  color: string | null;
  tasks: WorkspaceTaskItem[];
};

export type WorkspaceBoard = {
  projects: WorkspaceProjectGroup[];
  loading: boolean;
  unreachableNodes: Array<{ id: string; name: string }>;
  refresh: () => void;
};

export const WORKSPACE_FILTERS = ["all", "active", "blocked"] as const;
export type WorkspaceBoardFilter = (typeof WORKSPACE_FILTERS)[number];
