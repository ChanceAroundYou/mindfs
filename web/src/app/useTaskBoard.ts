/**
 * 看板/任务编排核心 —— T1 簇（5 个 useCallback + 3 个 useEffect + useRefreshSpin，~308 行）。
 *
 * 覆盖：任务详情缓存合并与淘汰、看板加载（Abort/seq 防旧请求覆盖）、
 * 任务移动/删除/worktree rebuild/finish、工作台结构刷新。
 *
 * 做法：hook，参数面 UseTaskBoardParams 即完整契约；函数体逐字搬入
 * （保持源码正则断言可命中，配合 source-map 注册）。
 *
 * TDZ：handleMoveKanbanTask 引用 refreshTaskWorktree（来自 useProjectTreeWorktrees，
 * 在 App.tsx 中后置初始化）。通过 refreshTaskWorktreeRef 传入，调用点用可选链，
 * 保持原 lazy 语义（回调在渲染后执行，ref 已赋值）。
 */
import { useCallback, useEffect, type MutableRefObject, type Dispatch, type SetStateAction } from "react";
import { useI18n } from "../i18n";
import { getRootNodeId } from "../services/net/rootNode";
import { shouldApplyTaskDetail } from "../services/taskDetailOrder";
import {
  beginTaskFinishWorktree,
  deleteTask,
  fetchTaskDetails,
  getCachedTaskDetails,
  getCachedTaskMeta,
  moveTask,
  pruneCachedTaskDetails,
  rebuildTaskWorktree,
  upsertCachedTaskDetails,
  FinishWorktreeConflict,
  FinishWorktreeDirty,
  FinishWorktreeUserChanges,
  type KanbanTask,
  type TaskDetail,
} from "../services/tasks";
import { protectedAPIReady } from "../services/net/api";
import { reportError } from "../services/net/error";
import { confirmDialog, promptDialog } from "../services/dialog";
import { firstTaskInputFromDetail, taskSessionKeysFromDetail } from "./appTask";
import { TASK_TEMPLATE_ALL_FILTER } from "./appStorage";
import { type ManagedRootPayload } from "./appMisc";
import { useRefreshSpin } from "../hooks";

export interface UseTaskBoardParams {
  currentRootId: string | null;
  currentRootNodeId: string | null;
  currentRootIdRef: MutableRefObject<string | null>;
  currentRootNodeIdRef: MutableRefObject<string | null>;
  managedRootByIdRef: MutableRefObject<Record<string, ManagedRootPayload>>;
  taskDetailsById: Record<string, TaskDetail>;
  taskDetailsByIdRef: MutableRefObject<Record<string, TaskDetail>>;
  taskTemplateFilter: string;
  kanbanTasks: KanbanTask[];
  selectedKanbanTaskId: string;
  kanbanLoadSeqRef: MutableRefObject<number>;
  kanbanAbortRef: MutableRefObject<AbortController | null>;
  workspaceBoardRefreshRef: MutableRefObject<(() => void) | null>;
  workspaceOpenRef: MutableRefObject<boolean>;
  refreshTaskWorktreeRef: MutableRefObject<((rootId: string, worktreePath: string) => Promise<void>) | null>;
  setTaskDetailsById: Dispatch<SetStateAction<Record<string, TaskDetail>>>;
  setTaskFirstInputById: Dispatch<SetStateAction<Record<string, string>>>;
  setTaskSessionKeysById: Dispatch<SetStateAction<Record<string, string[]>>>;
  setKanbanTaskCountItems: Dispatch<SetStateAction<KanbanTask[]>>;
  setKanbanTasks: Dispatch<SetStateAction<KanbanTask[]>>;
  setKanbanTasksLoading: Dispatch<SetStateAction<boolean>>;
  setSelectedKanbanTaskId: Dispatch<SetStateAction<string>>;
  setTaskSessionErrorDialog: Dispatch<SetStateAction<{ title: string; message: string; details: string[] } | null>>;
  getNodeIdForRoot: (rootId: string) => string | undefined;
  t: ReturnType<typeof useI18n>["t"];
}

export function useTaskBoard(params: UseTaskBoardParams) {
  const {
    currentRootId,
    currentRootNodeId,
    currentRootIdRef,
    currentRootNodeIdRef,
    managedRootByIdRef,
    taskDetailsById,
    taskDetailsByIdRef,
    taskTemplateFilter,
    kanbanTasks,
    selectedKanbanTaskId,
    kanbanLoadSeqRef,
    kanbanAbortRef,
    workspaceBoardRefreshRef,
    workspaceOpenRef,
    refreshTaskWorktreeRef,
    setTaskDetailsById,
    setTaskFirstInputById,
    setTaskSessionKeysById,
    setKanbanTaskCountItems,
    setKanbanTasks,
    setKanbanTasksLoading,
    setSelectedKanbanTaskId,
    setTaskSessionErrorDialog,
    getNodeIdForRoot,
    t,
  } = params;

  const applyTaskDetails = useCallback((rootId: string, details: TaskDetail[], persist = true) => {
    const curNid = String(currentRootNodeIdRef.current || "").trim();
    const curRoot = String(currentRootIdRef.current || "");
    // 节点隔离：当前根的旧节点数据不污染视图（旧请求迟到或 WS 跨节点）
    // 不直接依赖 getNodeIdForRoot（前向引用），改用 ref + 回退表
    const sid = rootId === curRoot ? curNid : String((managedRootByIdRef.current as Record<string, any>)[rootId]?._nodeId || getRootNodeId(rootId) || "").trim();
    if (sid && curNid && sid !== curNid && rootId === curRoot) {
      const filteredByNode = details.filter((d) => {
        const did = String((d.task as any)?._nodeId || "").trim();
        return !did || did === sid;
      });
      if (filteredByNode.length === 0) return;
      details = filteredByNode;
    }
    const accepted = details.filter(
      (detail) =>
        detail?.task?.id &&
        shouldApplyTaskDetail(taskDetailsByIdRef.current[detail.task.id], detail),
    );
    if (accepted.length === 0) return;
    const nextSnapshot = { ...taskDetailsByIdRef.current };
    accepted.forEach((detail) => {
      nextSnapshot[detail.task.id] = detail;
    });
    taskDetailsByIdRef.current = nextSnapshot;
    setTaskDetailsById((prev) => {
      const next = { ...prev };
      accepted.forEach((detail) => {
        if (shouldApplyTaskDetail(next[detail.task.id], detail)) {
          next[detail.task.id] = detail;
        }
      });
      return next;
    });
    setTaskFirstInputById((prev) => {
      const next = { ...prev };
      accepted.forEach((detail) => {
        next[detail.task.id] = firstTaskInputFromDetail(detail);
      });
      return next;
    });
    setTaskSessionKeysById((prev) => {
      const next = { ...prev };
      accepted.forEach((detail) => {
        next[detail.task.id] = taskSessionKeysFromDetail(detail);
      });
      return next;
    });
    setKanbanTaskCountItems((prev) => {
      const byId = new Map(prev.map((task) => [task.id, task]));
      accepted.forEach((detail) => byId.set(detail.task.id, detail.task));
      return Array.from(byId.values()).sort((a, b) => String(b.updated_at || "").localeCompare(String(a.updated_at || "")));
    });
    if (persist) {
      const persistNode = String(currentRootNodeIdRef.current || "").trim() || String((managedRootByIdRef.current as Record<string, any>)[rootId]?._nodeId || getRootNodeId(rootId) || "").trim() || undefined;
      void upsertCachedTaskDetails(rootId, accepted, persistNode);
    }
  }, []);

  /**
   * 重拉一遍工作台**结构**（有哪些项目、每组几张卡）。
   *
   * 卡片上挂的那个 task 本身已经由 getLiveTask 就地更新了（状态文字/颜色立刻变），
   * 这里补的是 applyTaskDetails 看不见的那半边：任务被删掉、worktree 被清掉、
   * 筛选下某张卡该不该还在。为此重拉的是整包扇出，代价不低，所以只在
   * 「结构真的可能变了」的用户动作与后台收尾上调用 ——
   * **不要**挂到 WS 的 task.updated 上，那会变成每条推送扇出一次。
   * 转发 ref 为 null 表示工作台没打开（refresh 还没挂上来），直接跳过。
   */
  const refreshWorkspaceBoard = useCallback(() => {
    workspaceBoardRefreshRef.current?.();
  }, []);

  /**
   * 把「服务端全量响应里已经没有」的任务从内存里摘掉。
   *
   * 缓存以前只写不删，内存这边同样只有合并（applyTaskDetails）没有移除，于是
   * 服务端删掉的任务会被一直渲染。必须真的 delete 键：kanbanTasks 和
   * kanbanTaskCountItems 都是从 taskDetailsById 派生的，只过滤数组不改 map 的话
   * 下一轮派生又会把它们带回来。
   */
  const pruneTaskDetails = useCallback((rootId: string, keepTaskIds: Iterable<string>) => {
    const keep = new Set(Array.from(keepTaskIds, (id) => String(id || "")).filter(Boolean));
    // taskDetailsById 是跨 root 共享的，权威集合只覆盖本次拉取的那个 root ——
    // 不按 root_id 圈定的话，刷新 A 项目会把 B 项目的任务一起清掉。
    const inScope = (task: KanbanTask) => task.root_id === rootId;
    const dropped = Object.entries(taskDetailsByIdRef.current)
      .filter(([taskId, detail]) => !keep.has(String(taskId)) && inScope((detail as TaskDetail).task))
      .map(([taskId]) => taskId);
    if (dropped.length === 0) return;
    const droppedSet = new Set(dropped);
    const kept = <V,>(entries: [string, V][]) =>
      Object.fromEntries(entries.filter(([taskId]) => !droppedSet.has(taskId))) as Record<string, V>;
    taskDetailsByIdRef.current = kept(Object.entries(taskDetailsByIdRef.current));
    setTaskDetailsById((prev) => kept(Object.entries(prev)));
    // 这两张表只有 taskId → 值，没有 root 信息可判归属，所以直接吃上面算好的
    // 淘汰名单：跟着 taskDetailsById 一起摘掉，不自己再推导一遍。
    setTaskFirstInputById((prev) => kept(Object.entries(prev)));
    setTaskSessionKeysById((prev) => kept(Object.entries(prev)));
    setKanbanTasks((prev) => {
      const next = prev.filter((task) => !inScope(task) || keep.has(String(task.id)));
      return next.length === prev.length ? prev : next;
    });
    setKanbanTaskCountItems((prev) => {
      const next = prev.filter((task) => !inScope(task) || keep.has(String(task.id)));
      return next.length === prev.length ? prev : next;
    });
  }, []);

  const loadKanbanTasks = useCallback(async (rootId?: string | null, force = false) => {
    if (!protectedAPIReady()) {
      return;
    }
    const targetRoot = rootId || currentRootIdRef.current;
    if (!targetRoot) {
      setKanbanTasks([]);
      setKanbanTaskCountItems([]);
      setTaskDetailsById({});
      setTaskFirstInputById({});
      setTaskSessionKeysById({});
      return;
    }
    // 不直接依赖 getNodeIdForRoot（前向引用），内联解析避免 TSC 提前引用
    const resolveNodeId = (rid: string): string | undefined => {
      const r = String(rid || "").trim();
      if (!r) return undefined;
      if (r === String(currentRootIdRef.current || "")) {
        const nid = String(currentRootNodeIdRef.current || "").trim();
        if (nid) return nid;
      }
      const entry = (managedRootByIdRef.current as Record<string, any>)[r];
      const enid = String(entry?._nodeId || "").trim();
      if (enid) return enid;
      return String(getRootNodeId(r) || "").trim() || undefined;
    };
    const snapNode = resolveNodeId(targetRoot) || null;
    const seq = ++kanbanLoadSeqRef.current;
    kanbanAbortRef.current?.abort();
    const controller = new AbortController();
    kanbanAbortRef.current = controller;
    setKanbanTasksLoading(true);
    try {
      const cached = await getCachedTaskDetails(targetRoot, snapNode || undefined);
      if (cached.length > 0 && seq === kanbanLoadSeqRef.current && !controller.signal.aborted && (resolveNodeId(targetRoot) || null) === snapNode) {
        applyTaskDetails(targetRoot, cached, false);
        if (seq === kanbanLoadSeqRef.current) setKanbanTasksLoading(false);
      }
      const meta = await getCachedTaskMeta(targetRoot, snapNode || undefined);
      const [details, recent] = await Promise.all([
        fetchTaskDetails(targetRoot, force ? undefined : { after: meta?.newestUpdatedAt || "" }, resolveNodeId(targetRoot)),
        !force && meta?.newestUpdatedAt
          ? fetchTaskDetails(targetRoot, { limit: 20 }, resolveNodeId(targetRoot))
          : Promise.resolve([] as TaskDetail[]),
      ]);
      if (controller.signal.aborted || seq !== kanbanLoadSeqRef.current) { return; }
      if ((resolveNodeId(targetRoot) || null) !== snapNode) { return; }
      const tagDetails = (arr: TaskDetail[]) => { for (const d of arr) (d.task as any)._nodeId = (d.task as any)._nodeId || snapNode; };
      if (details.length > 0) { tagDetails(details); applyTaskDetails(targetRoot, details); }
      if (recent.length > 0) { tagDetails(recent); applyTaskDetails(targetRoot, recent); }
      // 只有全量响应才是权威集合：增量只含更新的行、限流只含前 20 条，
      // 拿它们去淘汰会把「没被这次响应覆盖到」的任务误删。
      if (force) {
        const keepTaskIds = details.map((d) => d.task?.id);
        pruneTaskDetails(targetRoot, keepTaskIds);
        void pruneCachedTaskDetails(targetRoot, keepTaskIds, snapNode || undefined);
      }
    } catch (err) {
      if ((err as any)?.name === "AbortError") return;
      if (kanbanAbortRef.current?.signal.aborted) return;
      reportError("file.write_failed", String((err as Error)?.message || t("task.loadFailed")));
    } finally {
      if (seq === kanbanLoadSeqRef.current && !controller.signal.aborted) setKanbanTasksLoading(false);
    }
  }, [applyTaskDetails, pruneTaskDetails, t]);

  // 刷新必须走全量：增量响应在结构上就看不见「少了谁」，删除永远刷不掉。
  const kanbanRefreshSpin = useRefreshSpin(() => loadKanbanTasks(currentRootId, true));

	  useEffect(() => {
	    void loadKanbanTasks(currentRootId);
	  }, [currentRootId, currentRootNodeId, loadKanbanTasks]);

	  useEffect(() => {
	    const curNid = String(currentRootNodeIdRef.current || "").trim();
	    const allTasks = Object.values(taskDetailsById)
	      .map((detail) => detail.task)
	      .filter((task) => {
	        if (currentRootId && task.root_id !== currentRootId) return false;
	        const tid = String((task as any)._nodeId || "").trim();
	        if (tid && curNid && tid !== curNid) return false;
	        return true;
	      })
	      .sort((a, b) => String(b.updated_at || "").localeCompare(String(a.updated_at || "")));
	    const selectedTemplateId = taskTemplateFilter || "";
	    const allTemplatesSelected = selectedTemplateId === TASK_TEMPLATE_ALL_FILTER;
	    const filtered = selectedTemplateId && !allTemplatesSelected
	      ? allTasks.filter((task) => task.task_template_id === selectedTemplateId)
	      : allTasks;
	    setKanbanTaskCountItems(allTasks);
	    // 模板筛选只按 task_template_id 收窄，不要在这里再滤终态：
	    // 四块看板里「已结束」那一列唯一的任务来源就是 success/fail/cancelled，
	    // 前面多滤一道，终态卡在子看板里就凭空消失（连已完成列都进不去）。
	    setKanbanTasks(filtered);
	  }, [currentRootId, taskDetailsById, taskTemplateFilter]);

	  useEffect(() => {
	    if (!selectedKanbanTaskId) return;
	    if (kanbanTasks.some((task) => task.id === selectedKanbanTaskId)) return;
	    // 工作台上选中的任务往往属于**别的**项目，而 kanbanTasks 按 currentRootId 过滤，
	    // 天然不含它们 —— 不放行的话，刚弹出来的详情会被这个守卫立刻关掉。
	    // 详情已经在 taskDetailsById 里（openWorkspaceTaskDetail 灌的），选中是合法的。
	    if (workspaceOpenRef.current && taskDetailsByIdRef.current[selectedKanbanTaskId]) return;
	    setSelectedKanbanTaskId("");
	  }, [kanbanTasks, selectedKanbanTaskId]);

	  // 跨项目工作台状态与逻辑见 kanbanTaskPanel 定义前（workspaceOpen 等）

  const handleMoveKanbanTask = useCallback(async (task: KanbanTask, action: "next" | "run-now" | "pause" | "resume" | "complete" | "cancel" | "delete-task" | "rebuild-worktree" | "finish-worktree") => {
    const rootId = task.root_id || currentRootIdRef.current;
    if (!rootId) return;
    let reason = "";
    if (action === "pause") {
      const input = await promptDialog({ message: t("task.reasonPrompt", { action: t("task.actionPause") }) });
      if (input === null) {
        return;
      }
      reason = input.trim();
    }
    const nodeId = getNodeIdForRoot(rootId);
    try {
      // 删除任务卡：只删卡片，worktree / 分支 / 会话都不动 —— 所以它跟「取消」
      // 不是一回事（那个只改状态，卡片还在）。不可逆，先弹窗确认。
      // 删完卡片就从板上消失了，applyTaskDetails 那套「换掉这张卡的内容」帮不上忙，
      // 得让板子重拉一次（服务端还会广播 task.deleted，别的客户端也同步）。
      if (action === "delete-task") {
        const ok = await confirmDialog({
          message: t("task.deleteTaskConfirm"),
          confirmLabel: t("common.delete"),
          danger: true,
        });
        if (!ok) return;
        await deleteTask(rootId, task.id, nodeId);
        refreshWorkspaceBoard();
        return;
      }
      if (action === "finish-worktree") {
        // 收尾是不可逆的：合回主干 + 拆目录 + 删分支 + 搬会话，没有撤销。
        // 而它就长在执行键旁边，位置一撞就点得到 —— 确认弹窗是唯一的闸门。
        // 用 info 而不是 danger：这不是「删掉就没了」，走完的分支都已并进主干。
        const ok = await confirmDialog({
          message: t("task.finishWorktreeConfirm"),
          confirmLabel: t("task.finishWorktree"),
          danger: true,
        });
        if (!ok) return;
        // 服务端按真实状态分流，前端只把结论说清楚 —— 见 BeginFinishAction。
        const res = await beginTaskFinishWorktree(rootId, task.id, nodeId);
        if (res.detail) {
          applyTaskDetails(rootId, [res.detail]);
        }
        switch (res.action) {
          case "session_running":
            // 正常等待，不是失败：agent 正在回复，收尾要拆它的 cwd，得等它停。
            reportError("file.write_failed", t("task.finishWorktreeSessionRunning"), { severity: "info", recoverable: false });
            break;
          case "teardown":
            // 分支早就在主干里了，这次点的是机械清场，**同步做完了**。
            // 结论不在这里播报：服务端已经把 task.finish_teardown 推给所有客户端，
            // 由那条统一弹窗，免得同一个结论出现两种说法。板子要重拉 —— 卡片的
            // worktree 徽标从「有树」变成「已收尾」，applyTaskDetails 管不到这个。
            refreshWorkspaceBoard();
            break;
          case "nudged":
            reportError("file.write_failed", t("task.finishWorktreeNudged"), { severity: "info", recoverable: false });
            break;
          default:
            // stage_added：只追加了收尾段并起 agent。清场由服务端在那一段成功之后
            // 自己做，结论走 WS task.finish_teardown —— 这会儿还什么都没成，不说成功。
            reportError("file.write_failed", t("task.finishWorktreeStarted"), { severity: "info", recoverable: false });
            break;
        }
        return;
      }
      const detail = action === "rebuild-worktree"
        ? await rebuildTaskWorktree(rootId, task.id, nodeId)
        : await moveTask(rootId, task.id, action, reason, nodeId);
      applyTaskDetails(rootId, [detail]);
      if (detail.task.worktree_path) {
        void refreshTaskWorktreeRef.current?.(rootId, detail.task.worktree_path);
      }
      // 取消会把卡挪出当前筛选、重建 worktree 会改 worktree 徽标 —— 这些
      // applyTaskDetails 管不到（它只换那张卡的内容，管不了卡片还在不在板上）。
      // finish-worktree 不在此列：它在上面就 return 了，而清场真正落地是几秒后
      // 服务端 goroutine 里的事，那时由 WS task.finish_teardown 负责重拉。
      if (action === "cancel" || action === "rebuild-worktree") {
        refreshWorkspaceBoard();
      }
    } catch (err) {
      // 冲突要列出文件清单，不能只 toast 一句：仓库现在停在 MERGE_HEAD，
      // 用户得知道具体是哪些文件、然后自己去解。taskSessionErrorDialog 已经是
      // 「标题 + 详情列表」这个形状，直接复用而不是再造一个弹窗。
      if (err instanceof FinishWorktreeConflict) {
        setTaskSessionErrorDialog({
          title: t("task.finishWorktreeConflict"),
          message: err.message,
          details: err.conflictFiles,
        });
        return;
      }
      // 主 checkout 不干净：和冲突一样要列出文件清单 —— 那是「你自己的活还没收」，
      // 用户得知道具体是哪几个文件、然后自己去提交或暂存。
      if (err instanceof FinishWorktreeDirty) {
        setTaskSessionErrorDialog({
          title: t("task.finishWorktreeDirty"),
          message: t("task.finishWorktreeDirtyHint"),
          details: err.files,
        });
        return;
      }
      // worktree 里还有没提交的东西：合并**已经成功了**，只是目录拆不掉。
      // 同样要列文件 —— 而且要让用户知道「活已经合了」，否则他会以为白干一场。
      if (err instanceof FinishWorktreeUserChanges) {
        setTaskSessionErrorDialog({
          title: t("task.finishWorktreeUserChanges"),
          message: t("task.finishWorktreeUserChangesHint"),
          details: err.files,
        });
        return;
      }
      reportError("task.action_failed", String((err as Error)?.message || t("task.actionFailed")));
    }
  }, [applyTaskDetails, refreshWorkspaceBoard, t, getNodeIdForRoot, refreshTaskWorktreeRef]);

  return {
    applyTaskDetails,
    pruneTaskDetails,
    refreshWorkspaceBoard,
    loadKanbanTasks,
    handleMoveKanbanTask,
    kanbanRefreshSpin,
  };
}
