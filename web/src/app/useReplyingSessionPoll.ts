/**
 * 多项目「正在回复」旁路状态轮询 —— R 簇（~140 行）。
 *
 * 覆盖：本轮要问的节点集合（ref 稳定）、逐节点拉 /api/replying-sessions、
 * 唯一真相表的按节点增量合并、初始加载与 5s visible-only 轮询。
 *
 * 做法：hook，参数面 UseReplyingSessionPollParams 即完整契约；函数体逐字搬入
 * （保持源码正则断言可命中，配合 source-map 注册）。
 */
import { useCallback, useEffect, useRef, type MutableRefObject, type Dispatch, type SetStateAction } from "react";
import { ManagedRootPayload } from "./appMisc";
import { mergeReplyingStateByNode, MultiProjectSessionGroup } from "./appSession";
import { protectedJSON as apiProtectedJSON, withNodeRetry } from "../services/net/api";
import { appPath } from "../services/net/base";
import { getActiveNodeId, getNodes } from "../services/net/nodeRegistry";
import { parseSessionScopeKey, scopeSessionKey } from "../shared/scope";
import {
  alertableCompletedKeys,
  carrySubSessionKeys,
  completedPendingKeys,
  newlyCompletedPendingKeys,
  selectCompletedSelectedSession,
} from "../shared/pendingCompletion";
import { unreadCompletedAdd, unreadKey } from "../shared/unreadCompleted";

/** 跨节点回复状态轮询周期（固定 5s + visible-only）。 */
const REMOTE_REPLY_POLL_MS = 5000;

export interface UseReplyingSessionPollParams {
  applyPendingToMultiProjectGroups: (groups: MultiProjectSessionGroup[], pendingByKey: Record<string, boolean>, awaitingByKey?: Record<string, boolean>) => any;
  currentRootNodeIdRef: MutableRefObject<string | null>;
  loadMultiProjectSessionGroups: () => any;
  managedRootByKeyRef: MutableRefObject<Record<string, ManagedRootPayload>>;
  multiProjectPendingRef: MutableRefObject<Record<string, boolean>>;
  multiProjectAwaitingRef: MutableRefObject<Record<string, boolean>>;
  multiProjectSessionsEnabled: any;
  /** 远端会话刚跑完时放完成音（由调用方注入，保持本 hook 不依赖声音实现）。 */
  playCompletionSound?: () => void;
  /** 「正在看的会话」—— 与 WS 侧同一判据，正在看的完成后不标未读。 */
  selectedSessionRef?: MutableRefObject<{ key?: string; session_key?: string; _nodeId?: string } | null>;
  /**
   * 「正在看的会话」刚跑完时，用持久化窗口重锚当前视图。
   *
   * 远端节点（fn / pc）的 `session.done` 不会推到本节点 WS，5s 轮询是唯一的完成通道；
   * 只标未读/放完音而不重锚，画面会一直停在流式瞬时行上（思考块留在正文之后）。
   */
  onSelectedSessionCompleted?: (completed: { nodeId: string; rootId: string; sessionKey: string }) => void;
  /** 会话列表数据的重拉入口（带合并窗口）。只在「有会话刚跑完」时调用。 */
  scheduleMultiProjectSessionReload?: () => void;
  setMultiProjectPendingByKey: Dispatch<SetStateAction<Record<string, boolean>>>;
  setMultiProjectAwaitingByKey: Dispatch<SetStateAction<Record<string, boolean>>>;
  setMultiProjectSessionGroups: Dispatch<SetStateAction<MultiProjectSessionGroup[]>>;
}

export function useReplyingSessionPoll(params: UseReplyingSessionPollParams) {
  const {
    applyPendingToMultiProjectGroups,
    currentRootNodeIdRef,
    loadMultiProjectSessionGroups,
    managedRootByKeyRef,
    multiProjectPendingRef,
    multiProjectAwaitingRef,
    multiProjectSessionsEnabled,
    onSelectedSessionCompleted,
    playCompletionSound,
    scheduleMultiProjectSessionReload,
    selectedSessionRef,
    setMultiProjectPendingByKey,
    setMultiProjectAwaitingByKey,
    setMultiProjectSessionGroups,
  } = params;

  /**
   * 声音回调走 ref：调用方多半写成内联箭头（`() => playNotificationSound("complete")`），
   * 每次渲染都是新引用。直接进 useCallback 依赖会让 refresh 每渲染换一次身份，
   * 下面那两个 effect 跟着重建 —— 每帧都触发一次轮询，等于把 5s 轮询变成死循环。
   */
  const playCompletionRef = useRef(playCompletionSound);
  useEffect(() => {
    playCompletionRef.current = playCompletionSound;
  }, [playCompletionSound]);

  /**
   * 列表重拉同样走 ref：调用方是 `useAppMultiProjectReload` 里的 useCallback，
   * 它的依赖链（loadMultiProjectSessionGroups）一变身份就会连带重建 refresh，
   * 5s 轮询 effect 跟着重启 —— 与上面声音回调同一个坑，别把参数直接放进依赖。
   */
  const reloadListRef = useRef(scheduleMultiProjectSessionReload);
  useEffect(() => {
    reloadListRef.current = scheduleMultiProjectSessionReload;
  }, [scheduleMultiProjectSessionReload]);

  // 重锚回调同上：调用方内联箭头每次渲染换引用，不能进 refresh 的依赖。
  const reanchorSelectedRef = useRef(onSelectedSessionCompleted);
  useEffect(() => {
    reanchorSelectedRef.current = onSelectedSessionCompleted;
  }, [onSelectedSessionCompleted]);

  /**
   * 本轮要问的节点集合：与 loadMultiProjectSessionGroups 同源（getNodes() ∪
   * managedRootByKeyRef 里的 _nodeId），但走 ref 读最新值——refreshMultiProjectReplyingSessions
   * 的引用必须稳定，否则下面每 REMOTE_REPLY_POLL_MS 的轮询 effect 会跟着重建。
   */
  const multiProjectReplyNodeIdsRef = useRef<() => string[]>(() => []);
  useEffect(() => {
    multiProjectReplyNodeIdsRef.current = () => {
      const fromNodes = getNodes()
        .map((n) => String((n as any)?.id || "").trim())
        .filter(Boolean);
      const fromRoots = Array.from(
        new Set(
          Object.values(managedRootByKeyRef.current as Record<string, any>)
            .map((v) => String((v as any)?._nodeId || "").trim())
            .filter(Boolean),
        ),
      );
      const all = Array.from(new Set([...fromNodes, ...fromRoots]));
      if (all.length > 0) {
        return all;
      }
      // 节点表未就绪（初始化竞态）：回退当前选中/激活节点，否则会只建出一批空 nodeId 的键。
      return [
        String(currentRootNodeIdRef.current || "").trim() || String(getActiveNodeId() || "").trim(),
      ];
    };
  });

  /**
   * 当前仍标记为子会话的作用域键。判定「刚完成的是不是子会话」必须靠它而不是当前
   * 那一帧的快照：完成是靠「从 pending 表里消失」差分出来的，那一帧里会话已经不在
   * 列表上，它的 subSession 字段也就读不到了。跨帧保留，消失即忘。
   */
  const subSessionKeysRef = useRef<Set<string>>(new Set());

  /**
   * 「正在回复」是独立于会话列表数据的旁路状态（SessionItem 没有 running 字段，
   * 蓝灯只由 session.pending 决定），所以必须自己按节点去问。
   *
   * 曾经只发一个不带 nodeId 的 appPath(...) —— 那会打到**当前激活节点**，于是
   * 「在 PC 上看、任务跑在 Local」时 Local 的在跑会话永远不进 map，灯不亮、结束也不灭。
   * 会话列表本身早就是多节点正确的（loadMultiProjectSessionGroups 逐节点遍历），
   * 这里补上同一个对称：逐节点拉、逐节点建键。
   */
  const refreshMultiProjectReplyingSessions = useCallback(async () => {
    const nodeIds = multiProjectReplyNodeIdsRef.current();
    const okNodeIds = new Set<string>();
    const freshPending: Record<string, boolean> = {};
    const freshAwaiting: Record<string, boolean> = {};
    // 本轮快照里显式标了 subSession（子会话）或 silent（任务内部步骤）的键。
    // 两者的完成音都要跳过，所以合在一张「静默表」里。
    const freshSubSessions = new Set<string>();
    await Promise.all(
      nodeIds.map(async (nid) => {
        try {
          const payload = await withNodeRetry(() =>
            apiProtectedJSON<any>(appPath("/api/replying-sessions", nid)),
          );
          const items = Array.isArray(payload?.sessions) ? payload.sessions : [];
          for (const item of items) {
            const rootID = String(item?.rootId || item?.root_id || "");
            const sessionKey = String(item?.sessionKey || item?.session_key || "");
            if (rootID && sessionKey) {
              const scoped = scopeSessionKey(nid, rootID, sessionKey);
              // sidecar 状态拆成两张表：pending=实心灯，awaiting=空心灯，绝不混。
              if (item?.status === "replying" || item?.active === true) {
                freshPending[scoped] = true;
              }
              if (item?.subSession === true || item?.silent === true) {
                freshSubSessions.add(scoped);
              }
              // 「等回答」要记**显式 false**：轮询里出现过的会话 = 我们知道它的真实
              // 状态（不在等）；缺席的会话才是不知情。徽标（问号）靠这个区分来灭 ——
              // 用「缺席=不在等」会误清那些没被轮询到的会话。
              freshAwaiting[scoped] =
                item?.awaiting_answer === true || item?.awaitingAnswer === true;
            }
          }
          okNodeIds.add(nid);
        } catch (error) {
          console.warn("[multi-project-sessions] sidecar refresh failed", nid, error);
        }
      }),
    );
    if (okNodeIds.size === 0) {
      return;
    }
    const previousPending = multiProjectPendingRef.current;
    const nextPending = mergeReplyingStateByNode(multiProjectPendingRef.current, freshPending, okNodeIds);
    const nextAwaiting = mergeReplyingStateByNode(multiProjectAwaitingRef.current, freshAwaiting, okNodeIds);
    // 跨节点「刚完成」：远端节点的 session.done 浏览器收不到，完全靠这张 pending 表
    // 的 true→false 差分补完成音与未读徽标。**不新增通道**——轮询本来就在跑，
    // 这里只是读它前后两帧。
    const completed = newlyCompletedPendingKeys(
      previousPending,
      nextPending,
      okNodeIds,
      String(currentRootNodeIdRef.current || "").trim(),
    );
    // 子会话（Claude Code Task 子代理 / 看板节点 attach）跑完不响：父会话那一轮还在跑，
    // 用户要的是「活干完了」，不是「其中一个子任务回来了」。与 WS 路径
    // （realtimeWsHandlers 的 parent_session_key 判断）保持同一语义。
    const subSessions = subSessionKeysRef.current;
    const alertable = alertableCompletedKeys(completed, subSessions);
    // **列表时间**用的是不排除激活节点的差分：`session.done` 只推给挂载了该会话的
    // 客户端，后台会话（别的项目、看板任务）跑完收不到，只有这里能看到；不重拉，
    // 卡片就一直停在开跑前那条 updated_at 上（2026-11-22 用户报的就是这个：完成后
    // 仍显示 49m）。重拉走调用方的 300ms 合并窗口，与 WS 路径撞上也不会多拉一次。
    const completedAnyNode = completedPendingKeys(previousPending, nextPending, okNodeIds);
    multiProjectPendingRef.current = nextPending;
    multiProjectAwaitingRef.current = nextAwaiting;
    setMultiProjectPendingByKey(nextPending);
    setMultiProjectAwaitingByKey(nextAwaiting);
    setMultiProjectSessionGroups((groups) => applyPendingToMultiProjectGroups(groups, nextPending, nextAwaiting));
    // 正在看的会话刚跑完：必须用持久化窗口重锚，不能只放完音/标未读。
    // 远端节点的 done 不会推过来；即使本节点，WS 断线期间也可能错过 done。
    // 放在状态表更新之后，重锚读到的是「已结束」的真相，merge 时才会丢掉 seq=0 残留。
    const selectedCompleted = selectCompletedSelectedSession(
      completedAnyNode,
      selectedSessionRef?.current || null,
    );
    if (selectedCompleted) {
      reanchorSelectedRef.current?.(selectedCompleted);
    }
    if (completedAnyNode.length > 0) {
      reloadListRef.current?.();
    }
    if (alertable.length > 0) {
      // 每轮最多响一次：切回标签页那一轮可能一次补出好几条「刚完成」，
      // 逐条响会连成一片噪音。未读徽标仍逐条标 —— 那是静态信息，不吵人。
      playCompletionRef.current?.();
    }
    if (completed.length > 0) {
      const selectedKey = String(
        selectedSessionRef?.current?.key || selectedSessionRef?.current?.session_key || "",
      );
      const visible = typeof document === "undefined" || document.visibilityState === "visible";
      for (const key of completed) {
        const { nodeId, rootId, sessionKey } = parseSessionScopeKey(key);
        if (visible && selectedKey && selectedKey === sessionKey) continue;
        unreadCompletedAdd(unreadKey(nodeId, rootId, sessionKey));
      }
    }
    // 子会话标记只在「还在 replying」时才有意义：消失的键下一轮就该忘掉（会话完成后
    // 不会再有它的 subSession 字段可读）。失败的节点由 nextPending 的增量合并保住。
    subSessionKeysRef.current = carrySubSessionKeys(subSessions, freshSubSessions, nextPending);
  }, [applyPendingToMultiProjectGroups, currentRootNodeIdRef, selectedSessionRef]);

  useEffect(() => {
    if (!multiProjectSessionsEnabled) {
      return;
    }
    void refreshMultiProjectReplyingSessions();
    void loadMultiProjectSessionGroups();
  }, [loadMultiProjectSessionGroups, multiProjectSessionsEnabled, refreshMultiProjectReplyingSessions]);

  // 跨节点时远端节点的 session.user_message / session.done 都收不到（没订阅那些会话），
  // 低频轮询兜住「开始/结束」这两个时刻 —— 有 WS 实时兜底的本机节点不必重复问。
  //
  // 后台标签页**照样轮询**：浏览器会把定时器节流到约 1 次/分钟，那仍然能在标签页被
  // 冻结前抓到完成时刻。真正的后台保障是 Web Push（见 useBackgroundPush）——页面
  // 被冻结后这里一次都跑不到，只能靠推送。早先这里在隐藏时直接 return，等于把
  // 「后台 1 次/分钟」也扔掉了，用户看到的就是「切回前台才响」。
  useEffect(() => {
    if (!multiProjectSessionsEnabled) {
      return;
    }
    const timer = window.setInterval(() => {
      void refreshMultiProjectReplyingSessions();
    }, REMOTE_REPLY_POLL_MS);
    return () => window.clearInterval(timer);
  }, [multiProjectSessionsEnabled, refreshMultiProjectReplyingSessions]);

  // 标签页从后台切回来 / bfcache 恢复时立刻对账一次：后台期间定时器被浏览器节流甚至
  // 冻结，切回来时第一轮可能要等到下一个 5s 周期 —— 用户看到的是「刚才还在跑，
  // 回来灯还亮着」。这两条事件是「恢复可见」的唯一可靠信号。
  useEffect(() => {
    if (!multiProjectSessionsEnabled || typeof window === "undefined") {
      return;
    }
    const refreshOnResume = () => {
      if (typeof document !== "undefined" && document.visibilityState !== "visible") {
        return;
      }
      void refreshMultiProjectReplyingSessions();
    };
    document.addEventListener("visibilitychange", refreshOnResume);
    window.addEventListener("pageshow", refreshOnResume);
    return () => {
      document.removeEventListener("visibilitychange", refreshOnResume);
      window.removeEventListener("pageshow", refreshOnResume);
    };
  }, [multiProjectSessionsEnabled, refreshMultiProjectReplyingSessions]);

  return { refreshMultiProjectReplyingSessions };
}
