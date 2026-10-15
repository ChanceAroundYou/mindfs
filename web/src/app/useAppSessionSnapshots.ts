import { useMemo } from "react";
import type { SessionItem, MultiProjectSessionGroup } from "./appSession";
import type { KanbanTask, TaskDetail } from "../services/task/types";
import type { useSessionPending } from "./useSessionPending";
import { presenceFromTree, type SessionPresence } from "../shared/sessionPresence";

/**
 * 会话快照与查找表：当前主视图选中的会话、当前 root 的会话索引、
 * 跨项目的会话索引，以及看板/工作台选中任务的那一份快照。
 */
export type UseAppSessionSnapshotsParams = {
  selectedSession: SessionItem | null;
  selectedSessionLoading: boolean;
  currentRootId: string | null;
  sessions: SessionItem[];
  multiProjectSessionGroups: MultiProjectSessionGroup[];
  selectedKanbanTaskId: string;
  kanbanTasks: KanbanTask[];
  taskDetailsById: Record<string, TaskDetail>;
  taskSessionKeysById: Record<string, string[]>;
  getSessionSnapshot: ReturnType<typeof useSessionPending>["getSessionSnapshot"];
  multiProjectPendingByKey: Record<string, boolean>;
  rootSessionKey: (rootId: string, sessionKey: string) => string;
};

/**
 * 这些 memo 是一条链：两张查找表（sessionByKey 按当前 root、boardSessionByKey 再叠
 * 跨项目分组）喂给两个「选中项快照」。放在一起是因为它们的失效条件高度重叠 ——
 * 会话列表一刷新，四五个 memo 会同时重算，散开后没人说得清谁该先算。
 *
 * boardSessionByKey 是 CUSTOM(G-Z)：工作台列的任务大多不属于当前 root，只看
 * sessionByKey 会让跨项目卡片全都落回 AgentIcon 的「AI」占位。当前 root 的会话
 * 放后面覆盖，同键以它为准。
 */
export function useAppSessionSnapshots(params: UseAppSessionSnapshotsParams) {
  const { selectedSession, selectedSessionLoading, currentRootId, sessions, multiProjectSessionGroups, multiProjectPendingByKey, rootSessionKey, selectedKanbanTaskId, kanbanTasks, taskDetailsById, taskSessionKeysById, getSessionSnapshot } = params;

  const selectedSessionSnapshot = useMemo(
    () => {
      if (!selectedSession) {
        return null;
      }
      return getSessionSnapshot(
        selectedSession.root_id || currentRootId,
        selectedSession,
      );
    },
    [selectedSession, selectedSessionLoading, currentRootId, getSessionSnapshot],
  );

  // 选中会话的委托态：父自己 pending 灭了、子孙还在跑 → 空心「子代理运行中」，
  // 而不是误报完成。拓扑与运行态都在本层手上（分组含跨项目/跨节点会话），
  // 不必为一个选中项另开数据通道。
  const selectedSessionPresence: SessionPresence = useMemo(() => {
    if (!selectedSession) return "idle";
    const key = selectedSession.key || selectedSession.session_key || "";
    if (!key) return "idle";
    const selfPending = !!multiProjectPendingByKey[rootSessionKey(selectedSession.root_id || currentRootId || "", key)];
    const tree: SessionItem[] = [];
    for (const group of multiProjectSessionGroups) {
      for (const item of group.sessions || []) {
        tree.push({
          ...item,
          pending:
            typeof (item as any)?.pending === "boolean"
              ? (item as any).pending
              : !!multiProjectPendingByKey[
                  rootSessionKey(item.root_id || group.rootId, item.key || item.session_key || "")
                ],
        } as SessionItem);
      }
    }
    const own = tree.find((item) => (item.key || item.session_key) === key);
    if (!own) tree.push({ ...(selectedSession as any), pending: selfPending } as SessionItem);
    return presenceFromTree(key, selfPending, tree, selectedSession.root_id || currentRootId || "");
  }, [selectedSession, currentRootId, multiProjectSessionGroups, multiProjectPendingByKey, rootSessionKey]);

  const sessionByKey = useMemo(() => sessions.reduce<Record<string, SessionItem>>((acc, session) => {
    const key = session.key || session.session_key || "";
    if (key) acc[key] = session;
    return acc;
  }, {}), [sessions]);

  // 看板/工作台的卡片读这张表拿会话的 agent（卡片角上的小徽标）。sessionByKey 只装
  // **当前 root** 的会话，而工作台是跨项目视图 —— 它列的任务大多不属于当前项目，
  // 取不到就落回 AgentIcon 的「AI」文字占位（2026-10-07 实测「大多数用 dsh 的任务
  // 没有 agent 徽标」）。多项目分组本来就把跨项目/跨节点的会话拉全了，并进来即可，
  // 不需要第二条扇出。当前 root 的会话放后面覆盖，同键以它为准。
  const boardSessionByKey = useMemo(() => {
    const merged: Record<string, SessionItem> = {};
    for (const group of multiProjectSessionGroups) {
      for (const session of group.sessions || []) {
        const key = session.key || session.session_key || "";
        if (key) merged[key] = session;
      }
    }
    const all: Record<string, SessionItem> = { ...merged, ...sessionByKey };
    // pending 纯派生：唯一真相是 multiProjectPendingByKey。会话列表项上那份是「拉列表
    // 那一刻」的快照，跑完不重拉列表就会一直挂着（卡片上的「在回复」圆点就是这么卡住的）。
    for (const [key, item] of Object.entries(all)) {
      const root = String((item as any)?.root_id || "");
      if (!root) continue;
      const pending = !!multiProjectPendingByKey[rootSessionKey(root, key)];
      if (pending !== !!item.pending) {
        all[key] = { ...item, pending };
      }
    }
    return all;
  }, [multiProjectSessionGroups, sessionByKey, multiProjectPendingByKey, rootSessionKey]);

  const selectedKanbanTask = useMemo(() => {
    if (!selectedKanbanTaskId) return null;
    // 看板命中优先：那一路本来带模板筛选与状态分组的语义。
    const fromBoard = kanbanTasks.find((task) => task.id === selectedKanbanTaskId);
    if (fromBoard) return fromBoard;
    // 回退 taskDetailsById：工作台是跨项目视图，它列的任务大多不属于当前项目，
    // 而 kanbanTasks 按 currentRootId 过滤，天然不含它们。openWorkspaceTaskDetail
    // 灌的就是这张表，而面板的渲染门槛读的是本 memo —— 两份数据不并轨，
    // 工作台上点卡片就永远是「选中了但面板不弹」（守卫层放行了选中也救不了）。
    return taskDetailsById[selectedKanbanTaskId]?.task || null;
  }, [kanbanTasks, selectedKanbanTaskId, taskDetailsById]);

  const selectedKanbanTaskSessionKey = useMemo(() => {
    if (!selectedKanbanTask) return "";
    const keys = taskSessionKeysById[selectedKanbanTask.id] || [];
    return keys[0] || selectedKanbanTask.main_session_key || "";
  }, [selectedKanbanTask, taskSessionKeysById]);

  const selectedKanbanTaskSessionSnapshot = useMemo(() => {
    if (!selectedKanbanTask || !selectedKanbanTaskSessionKey) return null;
    const root = selectedKanbanTask.root_id || currentRootId || "";
    const session = sessionByKey[selectedKanbanTaskSessionKey] || {
      key: selectedKanbanTaskSessionKey,
      session_key: selectedKanbanTaskSessionKey,
      root_id: root,
      task_id: selectedKanbanTask.id,
    };
    return getSessionSnapshot(root, session as any);
  }, [currentRootId, getSessionSnapshot, selectedKanbanTask, selectedKanbanTaskSessionKey, sessionByKey]);

  return {
    selectedSessionSnapshot,
    selectedSessionPresence,
    sessionByKey,
    boardSessionByKey,
    selectedKanbanTask,
    selectedKanbanTaskSessionKey,
    selectedKanbanTaskSessionSnapshot,
  };
}
