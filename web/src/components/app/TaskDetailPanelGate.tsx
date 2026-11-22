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
  handleTaskSessionDrawerOpen: (sessionKey: string, rootOverride?: string | null | undefined, taskId?: string | undefined) => boolean;
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
  // 任务自己那台机器优先：工作台是跨节点扇出，同名项目按 rootId 只能查到首个节点。
  const ownNodeId = String(selectedKanbanTask?._nodeId || "").trim();
  const taskNodeId = (rootId: string | null | undefined): string | undefined =>
    ownNodeId || getNodeIdForRoot(rootId || "");

  return (
    <>
    {selectedKanbanTask ? (
            <TaskDetailPanel
              // 面板里有任务级本地状态（留言草稿、工作流选择、改名草稿、运行视图展开态）。
              // 按任务身份重挂载，避免同一实例被复用到另一张任务卡时把上一条任务的
              // 消息与草稿一起搬过去 —— 那样一按发送就发到了错的任务里。
              key={`${selectedKanbanTask._nodeId || ""}::${selectedKanbanTask.root_id || ""}::${selectedKanbanTask.id}`}
              detail={taskDetailsById[selectedKanbanTask.id] || { task: selectedKanbanTask, events: [] }}
              // 详情面板可能从工作台的就地入口打开（任务属于别的项目/节点），
              // 路由必须按**任务自己的**项目+节点走：只按 rootId 反查会把同名项目
              // （fn/pc 都有 `docs`）的编辑打到另一台机器。
              nodeId={taskNodeId(selectedKanbanTask.root_id || currentRootId)}
              onClose={() => setSelectedKanbanTaskId("")}
              onOpenSession={(sessionKey) => {
                // 先导航、后关面板：跳转要用的 root/taskId 在这里按当前任务算好并交给
                // handleTaskSessionDrawerOpen（内部同步装配 selectedSession + URL + 模式），
                // 只有它真的发起了跳转才关详情；解析失败时面板留着，不无谓卸载。
                const navigated = handleTaskSessionDrawerOpen(
                  sessionKey,
                  selectedKanbanTask.root_id || currentRootIdRef.current,
                  selectedKanbanTask.id,
                );
                if (navigated) setSelectedKanbanTaskId("");
              }}
              onMoved={(next) => applyTaskDetails(next.task.root_id || currentRootIdRef.current || "", [next])}
              // 任务在服务端已经被删掉（幽灵卡）：请看板把它从内存与缓存里摘掉，
              // 否则每点一次完成/删除都再失败一次。
              onTaskGone={(rootId, taskId) => dropKanbanTask(rootId, taskId, taskNodeId(rootId || currentRootIdRef.current))}
            />
          ) : null}
    </>
  );
}