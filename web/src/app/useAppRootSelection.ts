import { useCallback } from "react";
import { clearFileMemoryCacheForView } from "../services/file";
import { clearGitHistoryCache } from "../services/git";
import { getActiveNodeId, getNodeById, setActiveNodeId } from "../services/net/nodeRegistry";
import { setRootNodeId, setRootNodeMap } from "../services/net/rootNode";
import { scopeKey, scopeSessionKey } from "../shared/scope";
import { rootNodeKey } from "./appPath";
import type { ManagedRootPayload } from "./appMisc";
import type { SessionItem } from "./appSession";

type Ref<T> = { current: T };
type TaskState = ReturnType<typeof import("./useTaskWorkspaceState").useTaskWorkspaceState>;
type GitState = ReturnType<typeof import("./useGitData").useGitData>;

type UseAppRootSelectionParams = {
  currentRootNodeIdRef: Ref<string | null>;
  currentRootIdRef: Ref<string | null>;
  getSessionListReloadTimerRef: () => Ref<number | null>;
  sessionListAbortRef: Ref<AbortController | null>;
  kanbanAbortRef: TaskState["kanbanAbortRef"];
  kanbanLoadSeqRef: TaskState["kanbanLoadSeqRef"];
  sessionListLoadSeqRef: TaskState["sessionListLoadSeqRef"];
  setCurrentRootNodeId: (value: string | null) => void;
  selectedSessionByRootRef: Ref<Record<string, string | null>>;
  boundSessionByRootRef: Ref<Record<string, string | null>>;
  drawerSessionByRootRef: Ref<Record<string, SessionItem | null>>;
  setSelectedSession: (value: SessionItem | null) => void;
  sessionCacheRef: Ref<Record<string, import("../services/session").Session>>;
  taskDetailsByIdRef: TaskState["taskDetailsByIdRef"];
  setTaskDetailsById: TaskState["setTaskDetailsById"];
  setTaskFirstInputById: TaskState["setTaskFirstInputById"];
  setTaskSessionKeysById: TaskState["setTaskSessionKeysById"];
  setTaskRelatedFilesById: TaskState["setTaskRelatedFilesById"];
  setKanbanTasks: TaskState["setKanbanTasks"];
  setKanbanTaskCountItems: TaskState["setKanbanTaskCountItems"];
  setKanbanTasksLoading: TaskState["setKanbanTasksLoading"];
  setSessions: (value: SessionItem[]) => void;
  getGitState: () => Pick<GitState, "setGitStatus" | "setGitHistory" | "setGitStatusLoading" | "setGitHistoryLoading"> & {
    setGitDiff: (value: import("../services/git").GitDiffPayload | null) => void;
  };
  managedRootByKeyRef: Ref<Record<string, ManagedRootPayload>>;
  managedRootByIdRef: Ref<Record<string, ManagedRootPayload>>;
};

export function useAppRootSelection({
  currentRootNodeIdRef,
  currentRootIdRef,
  getSessionListReloadTimerRef,
  sessionListAbortRef,
  kanbanAbortRef,
  kanbanLoadSeqRef,
  sessionListLoadSeqRef,
  setCurrentRootNodeId,
  selectedSessionByRootRef,
  boundSessionByRootRef,
  drawerSessionByRootRef,
  setSelectedSession,
  sessionCacheRef,
  taskDetailsByIdRef,
  setTaskDetailsById,
  setTaskFirstInputById,
  setTaskSessionKeysById,
  setTaskRelatedFilesById,
  setKanbanTasks,
  setKanbanTaskCountItems,
  setKanbanTasksLoading,
  setSessions,
  getGitState,
  managedRootByKeyRef,
  managedRootByIdRef,
}: UseAppRootSelectionParams) {
  return useCallback((rootId: string, nodeId?: string) => {
    const rid = String(rootId || "").trim();
    if (!rid) return;
    const nid = String(nodeId || "").trim();
    const prevNid = String(currentRootNodeIdRef.current || "").trim();
    const isSameRootSwitch = rid === String(currentRootIdRef.current || "");
    if (prevNid && nid && prevNid !== nid && isSameRootSwitch) {
      // 同名项目跨节点切换：取消上一节点的防抖重拉与请求守卫，避免旧定时器覆盖新节点
      const reloadTimerRef = getSessionListReloadTimerRef();
      if (reloadTimerRef.current) {
        window.clearTimeout(reloadTimerRef.current);
        reloadTimerRef.current = null;
      }
      sessionListAbortRef.current?.abort();
      kanbanAbortRef.current?.abort();
      // 递增序号使旧 in-flight 请求的 seq 校验直接失效
      kanbanLoadSeqRef.current += 1;
      sessionListLoadSeqRef.current += 1;
    }
    currentRootNodeIdRef.current = nid || null;
    setCurrentRootNodeId(nid || null);
    // 无条件更新模块级 root→node 映射，保证路由/作用域解析在任何时机都正确
    setRootNodeId(rid, nid || undefined);
    // active node 跟着选中的项目走：选中哪个项目，请求就该发到那台机器。
    // 之前 active node 只由节点切换器改，于是「本机项目 + 上次点过 pc」的组合下，
    // 所有不带 nodeId 的请求（模板、会话…）都发去了 pc，页面看着在本机、数据却是
    // 另一台机器的。项目本身知道自己在哪个节点，选中它时把 active 一起切过去。
    if (nid) {
      const known = getNodeById(nid);
      if (known && getActiveNodeId() !== nid) {
        setActiveNodeId(nid);
        window.dispatchEvent(new CustomEvent("mindfs:nodes-changed"));
      }
    }
    if (prevNid && nid && prevNid !== nid) {
      // 同名项目跨节点切换：清掉该 root 的会话选择状态，避免把另一节点的会话恢复到错误节点
      const scopedRid = scopeKey(nid, rid);
      selectedSessionByRootRef.current[scopedRid] = "";
      boundSessionByRootRef.current[scopedRid] = "";
      delete drawerSessionByRootRef.current[scopedRid];
      setSelectedSession(null);
      const prefix = scopeSessionKey(nid, rid, "");
      for (const key of Object.keys(sessionCacheRef.current)) {
        if (key.startsWith(prefix)) delete sessionCacheRef.current[key];
      }
      if (isSameRootSwitch) {
        // 同名项目跨节点：清空 Kanban/文件/git 历史/会话视图残留，避免旧节点 incrementally merged 的 taskDetailsById/旧历史/旧会话串台
        taskDetailsByIdRef.current = {};
        setTaskDetailsById({});
        setTaskFirstInputById({});
        setTaskSessionKeysById({});
        setTaskRelatedFilesById({});
        setKanbanTasks([]);
        setKanbanTaskCountItems([]);
        setKanbanTasksLoading(true);
        setSessions([]);
        clearFileMemoryCacheForView();
        clearGitHistoryCache(rid, prevNid);
        const gitState = getGitState();
        gitState.setGitStatus(null);
        gitState.setGitHistory(null);
        gitState.setGitDiff(null);
        gitState.setGitStatusLoading(true);
        gitState.setGitHistoryLoading(true);
      }
    }
    // 让按裸 rootId 的 payload 读取命中当前选中节点的数据
    const entry = managedRootByKeyRef.current[rootNodeKey(nid, rid)];
    if (entry && managedRootByIdRef.current[rid] !== entry) {
      managedRootByIdRef.current = {
        ...managedRootByIdRef.current,
        [rid]: entry,
      };
      setRootNodeMap(managedRootByIdRef.current as Record<string, any>);
    }
  }, []);
}
