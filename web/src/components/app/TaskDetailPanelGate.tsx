// 从 App.tsx 抽出的渲染区域（机械搬移，JSX 逐字未改）。
import { TaskDetailPanel } from "../task/TaskDetailPanel";
import type { Dispatch, SetStateAction, RefObject } from "react";
import { fetchAgents, restartAgent, type AgentStatus } from "../../services/agents";
import {
  beginTaskFinishWorktree,
  createTask,
  deleteTask,
  deleteTaskTemplate,
  // FinishWorktreeConflict 是 class 不是 type：下面要用 instanceof 分流
  // （冲突要列文件清单，其它失败只报一句）。
  FinishWorktreeConflict,
  fetchTaskDetails,
  fetchTaskTemplates,
  getCachedTaskDetails,
  getCachedTaskMeta,
  moveTask,
  pruneCachedTaskDetails,
  rebuildTaskWorktree,
  saveTaskTemplate,
  upsertCachedTaskDetails,
  type KanbanTask,
  type StageRun,
  type TaskDetail,
  type TaskOverviewItem,
  type StageTemplate,
  type TaskTemplate,
} from "../../services/tasks";

export interface TaskDetailPanelGateProps {
  applyTaskDetails: (rootId: string, details: TaskDetail[], persist?: boolean) => void;
  availableAgents: AgentStatus[];
  currentRootId: string | null;
  currentRootIdRef: RefObject<string | null>;
  getDisplayNodeColor: (rootId: string) => string | null;
  getNodeIdForRoot: (rootId: string) => string | undefined;
  handleMoveKanbanTask: (task: KanbanTask, action: "next" | "run-now" | "pause" | "resume" | "complete" | "cancel" | "delete-task" | "rebuild-worktree" | "finish-worktree") => Promise<void>;
  handleTaskSessionDrawerOpen: (sessionKey: string, rootOverride?: string | null | undefined, taskId?: string | undefined) => void;
  selectedKanbanTask: KanbanTask | null;
  setSelectedKanbanTaskId: Dispatch<SetStateAction<string>>;
  taskDetailsById: Record<string, TaskDetail>;
}

export function TaskDetailPanelGate(props: TaskDetailPanelGateProps) {
  const applyTaskDetails = props.applyTaskDetails;
  const availableAgents = props.availableAgents;
  const currentRootId = props.currentRootId;
  const currentRootIdRef = props.currentRootIdRef;
  const getDisplayNodeColor = props.getDisplayNodeColor;
  const getNodeIdForRoot = props.getNodeIdForRoot;
  const handleMoveKanbanTask = props.handleMoveKanbanTask;
  const handleTaskSessionDrawerOpen = props.handleTaskSessionDrawerOpen;
  const selectedKanbanTask = props.selectedKanbanTask;
  const setSelectedKanbanTaskId = props.setSelectedKanbanTaskId;
  const taskDetailsById = props.taskDetailsById;

  return (
    <>
    {selectedKanbanTask ? (
            <TaskDetailPanel
              detail={taskDetailsById[selectedKanbanTask.id] || { task: selectedKanbanTask, stage_runs: [], events: [] }}
              agents={availableAgents}
              // 详情面板可能从工作台的就地入口打开（任务属于别的项目/节点），
              // 路由必须按任务自己的项目走，写死当前项目会把编辑打到错节点。
              nodeId={getNodeIdForRoot(selectedKanbanTask.root_id || currentRootId || "")}
              onClose={() => setSelectedKanbanTaskId("")}
              onOpenSession={(sessionKey) => handleTaskSessionDrawerOpen(sessionKey, selectedKanbanTask.root_id || currentRootIdRef.current, selectedKanbanTask.id)}
              onMoved={(next) => applyTaskDetails(next.task.root_id || currentRootIdRef.current || "", [next])}
              onRunTask={(task) => handleMoveKanbanTask(task, "run-now")}
              accentColor={getDisplayNodeColor(String(selectedKanbanTask.root_id || currentRootId || "")) || undefined}
            />
          ) : null}
    </>
  );
}
