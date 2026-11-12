import { useState } from "react";
import { TaskBoardView } from "../components/task/TaskBoardView";
import { WorkflowDraftEditor } from "../components/task/WorkflowDraftEditor";
import type { SessionItem } from "./appSession";
import type { KanbanTask } from "../services/tasks";
import type { WorkspaceBoard, WorkspaceBoardFilter } from "./useWorkspaceBoard";
import type { AgentStatus } from "../shared/agentTypes";

/**
 * 看板面板那一个 <TaskBoardView>。
 */
export type UseAppKanbanTaskPanelParams = {
  workspaceOpen: boolean;
  currentRootId: string | null;
  currentRootIdRef: React.RefObject<string | null>;
  workspaceBoard: WorkspaceBoard;
  workspaceFilter: WorkspaceBoardFilter;
  changeWorkspaceFilter: (filter: WorkspaceBoardFilter) => void;
  workspaceCollapsedKeys: Set<string>;
  toggleWorkspaceProject: (key: string) => void;
  setWorkspaceBoardToken: React.Dispatch<React.SetStateAction<number>>;
  managedRootIds: string[];
  getRootDisplayName: (rootId: string | null | undefined) => string;
  openWorkspaceProject: (rootId: string) => Promise<void>;
  openWorkspaceTaskDetail: (item: { root_id: string; nodeId: string; task: KanbanTask; }) => Promise<void>;
  handleWorkspaceCreateTask: (rootId: string, nodeId: string) => void;
  setSelectedKanbanTaskId: React.Dispatch<React.SetStateAction<string>>;
  kanbanTasksLoading: boolean;
  kanbanStageColumns: { index: number; name: string; role: "user" | "agent"; tasks: KanbanTask[]; groups?: Array<{ key: string; name: string; tone: "success" | "danger" | "muted"; tasks: KanbanTask[]; }>; }[];
  selectedKanbanTaskId: string;
  expandedTaskInputIds: Set<string>;
  setExpandedTaskInputIds: React.Dispatch<React.SetStateAction<Set<string>>>;
  collapsedTaskCompletionGroups: Set<string>;
  setCollapsedTaskCompletionGroups: React.Dispatch<React.SetStateAction<Set<string>>>;
  collapsedKanbanColumns: Set<string>;
  setCollapsedKanbanColumns: React.Dispatch<React.SetStateAction<Set<string>>>;
  taskFirstInputById: Record<string, string>;
  taskSessionKeysById: Record<string, string[]>;
  boardSessionByKey: Record<string, SessionItem>;
  getDisplayNodeColor: (rootId: string) => string | null;
  handleSelectKanbanTask: (task: KanbanTask) => void;
  handleMoveKanbanTask: (task: KanbanTask, action: "next" | "run-now" | "pause" | "resume" | "complete" | "cancel" | "delete-task" | "rebuild-worktree" | "finish-worktree") => Promise<void>;
  openTaskCreateDialog: (targetRootId?: string, targetNodeId?: string) => void;
  handleTaskSessionDrawerOpen: (sessionKey: string, rootOverride?: string | null, taskId?: string) => void;
  setTaskSessionErrorDialog: React.Dispatch<React.SetStateAction<{ title: string; message: string; details: string[]; } | null>>;
  loadKanbanTasks: (rootId?: string | null, force?: boolean) => Promise<void>;
  agents: AgentStatus[];
};

/**
 * **方案原本要把这里做成 `components/app/KanbanTaskPanel.tsx` 组件，改成 hook 了。**
 * 包一层组件会往 React 树里插一个新的边界（新的挂载身份、新的 props 对象），
 * 那是可观察的行为变化；hook 返回的是同一个 <TaskBoardView> 元素，元素树逐字不变。
 *
 * 一处容易看错的地方：属性写作 `sessionByKey={boardSessionByKey}` ——
 * **值必须是 boardSessionByKey**（跨项目那张表）。只传当前 root 的 sessionByKey
 * 会让工作台里跨项目的卡片全都落回 AgentIcon 的「AI」占位（CUSTOM(G-Z)）。
 * 名字叫 sessionByKey 只是 TaskBoardView 的 prop 名，别被它带回错误的那张表。
 */
export function useAppKanbanTaskPanel(params: UseAppKanbanTaskPanelParams) {
  const { workspaceOpen, currentRootId, currentRootIdRef, workspaceBoard, workspaceFilter, changeWorkspaceFilter, workspaceCollapsedKeys, toggleWorkspaceProject, setWorkspaceBoardToken, managedRootIds, getRootDisplayName, openWorkspaceProject, openWorkspaceTaskDetail, handleWorkspaceCreateTask, setSelectedKanbanTaskId, kanbanTasksLoading, kanbanStageColumns, selectedKanbanTaskId, expandedTaskInputIds, setExpandedTaskInputIds, collapsedTaskCompletionGroups, setCollapsedTaskCompletionGroups, collapsedKanbanColumns, setCollapsedKanbanColumns, taskFirstInputById, taskSessionKeysById, boardSessionByKey, getDisplayNodeColor, handleSelectKanbanTask, handleMoveKanbanTask, openTaskCreateDialog, handleTaskSessionDrawerOpen, setTaskSessionErrorDialog, loadKanbanTasks, agents } = params;

  // 工作流草稿编辑器是独立页（listWorkflowDrafts 走控制面），盖掉看板本身；
  // 看板状态都住在 app 层 hook 里，来回切换不会丢数据。
  const [workflowEditorOpen, setWorkflowEditorOpen] = useState(false);

  const kanbanTaskPanel = workflowEditorOpen ? (
    <WorkflowDraftEditor onClose={() => setWorkflowEditorOpen(false)} agents={agents} />
  ) : (
    <TaskBoardView
      workspaceOpen={workspaceOpen}
      currentRootId={currentRootId}
      currentRootIdRef={currentRootIdRef}
      workspaceBoard={workspaceBoard}
      workspaceFilter={workspaceFilter}
      onWorkspaceFilterChange={changeWorkspaceFilter}
      workspaceCollapsedKeys={workspaceCollapsedKeys}
      onToggleWorkspaceProject={toggleWorkspaceProject}
      onRefreshWorkspaceBoard={() => setWorkspaceBoardToken((token) => token + 1)}
      managedRootIds={managedRootIds}
      getRootDisplayName={getRootDisplayName}
      openWorkspaceProject={openWorkspaceProject}
      openWorkspaceTaskDetail={openWorkspaceTaskDetail}
      handleWorkspaceCreateTask={handleWorkspaceCreateTask}
      setSelectedKanbanTaskId={setSelectedKanbanTaskId}
      kanbanTasksLoading={kanbanTasksLoading}
      kanbanStageColumns={kanbanStageColumns}
      selectedKanbanTaskId={selectedKanbanTaskId}
      expandedTaskInputIds={expandedTaskInputIds}
      setExpandedTaskInputIds={setExpandedTaskInputIds}
      collapsedTaskCompletionGroups={collapsedTaskCompletionGroups}
      setCollapsedTaskCompletionGroups={setCollapsedTaskCompletionGroups}
      collapsedKanbanColumns={collapsedKanbanColumns}
      setCollapsedKanbanColumns={setCollapsedKanbanColumns}
      taskFirstInputById={taskFirstInputById}
      taskSessionKeysById={taskSessionKeysById}
      sessionByKey={boardSessionByKey}
      getDisplayNodeColor={getDisplayNodeColor}
      handleSelectKanbanTask={handleSelectKanbanTask}
      handleMoveKanbanTask={handleMoveKanbanTask}
      openTaskCreateDialog={openTaskCreateDialog}
      handleTaskSessionDrawerOpen={handleTaskSessionDrawerOpen}
      setTaskSessionErrorDialog={setTaskSessionErrorDialog}
      loadKanbanTasks={loadKanbanTasks}
      onOpenWorkflowEditor={() => setWorkflowEditorOpen(true)}
    />
  );

  return {
    kanbanTaskPanel,
  };
}
