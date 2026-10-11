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
import { scopeSessionKey } from "../shared/scope";

/** 跨节点回复状态轮询周期（固定 5s + visible-only）。 */
const REMOTE_REPLY_POLL_MS = 5000;

export interface UseReplyingSessionPollParams {
  applyPendingToMultiProjectGroups: (groups: MultiProjectSessionGroup[], pendingByKey: Record<string, boolean>) => any;
  currentRootNodeIdRef: MutableRefObject<string | null>;
  loadMultiProjectSessionGroups: () => any;
  managedRootByKeyRef: MutableRefObject<Record<string, ManagedRootPayload>>;
  multiProjectPendingRef: MutableRefObject<Record<string, boolean>>;
  multiProjectSessionsEnabled: any;
  setMultiProjectPendingByKey: Dispatch<SetStateAction<Record<string, boolean>>>;
  setMultiProjectSessionGroups: Dispatch<SetStateAction<MultiProjectSessionGroup[]>>;
}

export function useReplyingSessionPoll(params: UseReplyingSessionPollParams) {
  const {
    applyPendingToMultiProjectGroups,
    currentRootNodeIdRef,
    loadMultiProjectSessionGroups,
    managedRootByKeyRef,
    multiProjectPendingRef,
    multiProjectSessionsEnabled,
    setMultiProjectPendingByKey,
    setMultiProjectSessionGroups,
  } = params;

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
    const fresh: Record<string, boolean> = {};
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
              // 必须用「该节点自己的 nid」，不能用 rootSessionKey()：后者走
              // getNodeIdForRoot(rootId)，同名项目在多节点各有一份时会解析到当前
              // 选中节点，把 Local 的条目错记到 PC 名下。
              fresh[scopeSessionKey(nid, rootID, sessionKey)] = true;
            }
          }
          okNodeIds.add(nid);
        } catch (error) {
          // 该节点没拉到：既不并入也不清除它的旧值，灯不会被误灭。
          console.warn("[multi-project-sessions] replying refresh failed", nid, error);
        }
      }),
    );
    if (okNodeIds.size === 0) {
      return;
    }
    // **只重算本轮成功节点自己的键**，其余节点原样保留。
    // 整体替换会让切节点时旧节点在跑的会话当场全灭（切节点 → nodes-changed →
    // 本函数 → 新响应里没有旧节点 → 灯全没）。
    const next = mergeReplyingStateByNode(multiProjectPendingRef.current, fresh, okNodeIds);
    // 这里**刻意不做**「静默 N 秒就本地灭灯」的降级。真在跑的会话会长时间没有 stream
    // 事件：mindfs 不转发 SDK 的 keep_alive 心跳，唯一的周期事件 ToolProgressMessage
    // 只覆盖 tool 执行（思考、tool 间隙、起始间隙都没有事件）。按静默判「已结束」会
    // 出现「明明在跑却灭灯」、亮灯时长随事件节奏漂移。灯的权威来源就是本次轮询本身
    // —— 服务端 ClearSessionPending 在回合结束时删条目，5s 一轮足够兜住 WS 断连；
    // 服务端真卡死由 broadcastSessionDone 的 2s 超时兜底。两条路都不经过静默判据。
    multiProjectPendingRef.current = next;
    setMultiProjectPendingByKey(next);
    setMultiProjectSessionGroups((groups) => applyPendingToMultiProjectGroups(groups, next));
  }, [applyPendingToMultiProjectGroups]);

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
