import { useEffect, useState } from "react";
import { TaskBoardView } from "../components/task/TaskBoardView";
import { WorkflowDraftEditor } from "../components/task/WorkflowDraftEditor";
import type { SessionItem } from "./appSession";
import type { KanbanTask } from "../services/tasks";
import type { WorkspaceBoard, WorkspaceBoardFilter } from "./useWorkspaceBoard";
import type { AgentStatus } from "../shared/agentTypes";
import type { KanbanColumn, MoveKanbanAction } from "../components/task/taskBoardTypes";
import type { MainViewMode } from "./appPath";
import { boardKeepsWorkflowEditor } from "../components/task/workflowEditorLayout";

/**
 * 看板面板那一个 <TaskBoardView>。
 */
export type UseAppKanbanTaskPanelParams = {
  workspaceOpen: boolean;
  /** 主视图模式：编辑器只在看板（含跨项目工作台）里存在，离开就重置。 */
  mainView: MainViewMode;
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
  kanbanColumns: KanbanColumn[];
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
  handleMoveKanbanTask: (task: KanbanTask, action: MoveKanbanAction) => Promise<void>;
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
  const { workspaceOpen, mainView, currentRootId, currentRootIdRef, workspaceBoard, workspaceFilter, changeWorkspaceFilter, workspaceCollapsedKeys, toggleWorkspaceProject, setWorkspaceBoardToken, managedRootIds, getRootDisplayName, openWorkspaceProject, openWorkspaceTaskDetail, handleWorkspaceCreateTask, setSelectedKanbanTaskId, kanbanTasksLoading, kanbanColumns, selectedKanbanTaskId, expandedTaskInputIds, setExpandedTaskInputIds, collapsedTaskCompletionGroups, setCollapsedTaskCompletionGroups, collapsedKanbanColumns, setCollapsedKanbanColumns, taskFirstInputById, taskSessionKeysById, boardSessionByKey, getDisplayNodeColor, handleSelectKanbanTask, handleMoveKanbanTask, openTaskCreateDialog, handleTaskSessionDrawerOpen, setTaskSessionErrorDialog, loadKanbanTasks, agents } = params;

  // 工作流草稿编辑器是独立页（listWorkflowDrafts 走控制面），盖掉看板本身；
  // 看板状态都住在 app 层 hook 里，来回切换不会丢数据。
  const [workflowEditorOpen, setWorkflowEditorOpen] = useState(false);

  /* 编辑器只属于看板这一次停留。看板面板在 files/chat 态根本不渲染（DefaultListView
     的 topContent 是条件渲染），但 open 标记住在这一层，会活过整段离开——用户切去
     文件/会话再切回看板，看到的第一屏还是工作流编辑器，而不是看板。所以离开看板就
     把标记清掉，回来落回看板。真正的脏改动提醒在编辑器自己的关闭路径上（confirmLeave）。 */
  const boardVisible = boardKeepsWorkflowEditor(mainView);
  useEffect(() => {
    if (!boardVisible) setWorkflowEditorOpen(false);
  }, [boardVisible]);

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
      kanbanColumns={kanbanColumns}
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
