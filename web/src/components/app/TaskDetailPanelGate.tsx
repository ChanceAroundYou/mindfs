// 从 App.tsx 抽出的渲染区域（机械搬移，JSX 逐字未改）。
import { TaskDetailPanel } from "../task/TaskDetailPanel";
import type { Dispatch, SetStateAction, RefObject } from "react";
import type { KanbanTask, TaskDetail } from "../../shared/taskTypes";

export interface TaskDetailPanelGateProps {
  applyTaskDetails: (rootId: string, details: TaskDetail[], persist?: boolean) => void;
  currentRootId: string | null;
  currentRootIdRef: RefObject<string | null>;
  getNodeIdForRoot: (rootId: string) => string | undefined;
  dropKanbanTask: (rootId: string, taskId: string, nodeId?: string) => void;
  handleTaskSessionDrawerOpen: (sessionKey: string, rootOverride?: string | null | undefined, taskId?: string | undefined) => void;
  selectedKanbanTask: KanbanTask | null;
  setSelectedKanbanTaskId: Dispatch<SetStateAction<string>>;
  taskDetailsById: Record<string, TaskDetail>;
}

export function TaskDetailPanelGate(props: TaskDetailPanelGateProps) {
  const applyTaskDetails = props.applyTaskDetails;
  const currentRootId = props.currentRootId;
  const currentRootIdRef = props.currentRootIdRef;
  const dropKanbanTask = props.dropKanbanTask;
  const getNodeIdForRoot = props.getNodeIdForRoot;
  const handleTaskSessionDrawerOpen = props.handleTaskSessionDrawerOpen;
  const selectedKanbanTask = props.selectedKanbanTask;
  const setSelectedKanbanTaskId = props.setSelectedKanbanTaskId;
  const taskDetailsById = props.taskDetailsById;

  return (
    <>
    {selectedKanbanTask ? (
            <TaskDetailPanel
              // 面板里有任务级本地状态（留言草稿、工作流选择、改名草稿、运行视图展开态）。
              // 按任务身份重挂载，避免同一实例被复用到另一张任务卡时把上一条任务的
              // 消息与草稿一起搬过去 —— 那样一按发送就发到了错的任务里。
              key={`${selectedKanbanTask.root_id || ""}::${selectedKanbanTask.id}`}
              detail={taskDetailsById[selectedKanbanTask.id] || { task: selectedKanbanTask, events: [] }}
              // 详情面板可能从工作台的就地入口打开（任务属于别的项目/节点），
              // 路由必须按任务自己的项目走，写死当前项目会把编辑打到错节点。
              nodeId={getNodeIdForRoot(selectedKanbanTask.root_id || currentRootId || "")}
              onClose={() => setSelectedKanbanTaskId("")}
              onOpenSession={(sessionKey) => handleTaskSessionDrawerOpen(sessionKey, selectedKanbanTask.root_id || currentRootIdRef.current, selectedKanbanTask.id)}
              onMoved={(next) => applyTaskDetails(next.task.root_id || currentRootIdRef.current || "", [next])}
              // 任务在服务端已经被删掉（幽灵卡）：请看板把它从内存与缓存里摘掉，
              // 否则每点一次完成/删除都再失败一次。
              onTaskGone={(rootId, taskId) => dropKanbanTask(rootId, taskId, getNodeIdForRoot(rootId || currentRootIdRef.current || ""))}
            />
          ) : null}
    </>
  );
}