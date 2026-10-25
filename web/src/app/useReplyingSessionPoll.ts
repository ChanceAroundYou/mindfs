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
import { newlyCompletedPendingKeys } from "../shared/pendingCompletion";
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
  selectedSessionRef?: MutableRefObject<{ key?: string; session_key?: string } | null>;
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
    playCompletionSound,
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
    multiProjectPendingRef.current = nextPending;
    multiProjectAwaitingRef.current = nextAwaiting;
    setMultiProjectPendingByKey(nextPending);
    setMultiProjectAwaitingByKey(nextAwaiting);
    setMultiProjectSessionGroups((groups) => applyPendingToMultiProjectGroups(groups, nextPending, nextAwaiting));
    if (completed.length > 0) {
      // 每轮最多响一次：切回标签页那一轮可能一次补出好几条「刚完成」，
      // 逐条响会连成一片噪音。未读徽标仍逐条标 —— 那是静态信息，不吵人。
      playCompletionRef.current?.();
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
  // ponytail: 固定 5s + visible-only。调小到 2s 更跟手但请求翻倍；
  // 要零延迟得让远端节点把 pending 变更跨机广播出去，那是另一个议题。
  useEffect(() => {
    if (!multiProjectSessionsEnabled) {
      return;
    }
    const timer = window.setInterval(() => {
      if (typeof document !== "undefined" && document.visibilityState !== "visible") {
        return;
      }
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
