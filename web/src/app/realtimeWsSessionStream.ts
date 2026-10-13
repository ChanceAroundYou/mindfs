import { mergeSessionItems } from "../services/session/sessionListMerge";
import { streamEventTimestamp } from "../services/session/sessionDuration";
import { reportError } from "../services/net/error";
import type { Exchange, SessionItem } from "./appSession";
import type { Session } from "../services/session";
import type { RealtimeEventsContext } from "./useRealtimeEventsContext";

/** 会话 stream 事件的归属、缓存投影与消息分发。 */
export function createRealtimeWsSessionStreamHandler(
  ctx: RealtimeEventsContext,
  local: { handleSessionStreamDone: (rootID: string, sessionKey: string) => boolean },
) {
  const {
    refs: {
      boundSessionByRootRef, currentRootIdRef,
      currentRootNodeIdRef, drawerSessionByRootRef, pendingBySessionRef,
      pendingDraftRef, selectedSessionRef, sessionCacheRef,
      suppressedAutoBindSessionByRootRef,
    },
    setters: {
      setBoundSessionForRoot, setCodexRateLimitsRefreshToken, setDrawerSessionForRoot,
      setMultiProjectSessionGroups, setSessions,
    },
    actions: {
      appendAgentChunkForSession, appendCompactNoticeForSession, appendPlanUpdateForSession,
      appendThoughtChunkForSession, appendTodoUpdateForSession, appendToolCallForSession,
      attachContextWindowToLatestAssistant, bumpCacheVersion, getNodeIdForRoot,
      markSessionPending, promotePendingSessionForRoot, resolveRootForSessionKey,
    },
    values: { rootSessionKey, scopedRootKey, t },
  } = ctx;
  const { handleSessionStreamDone } = local;
  return (payload: any) => {
    const wsNid = String((payload as any)?._nodeId || (payload as any)?.nodeId || "").trim();
    const curNid = String(currentRootNodeIdRef.current || "").trim();
    const streamKey =
      typeof payload?.session_key === "string" ? payload.session_key : "";
    const activeRoot =
      typeof payload?.root_id === "string" && payload.root_id
        ? payload.root_id
        : resolveRootForSessionKey(streamKey) || currentRootIdRef.current;
    if (!streamKey || !activeRoot) return;
    const event = payload.event;
    // Apply client-only activity before filtering stream content to the active node.
    if (event?.type && event.type !== "message_done") {
      const eventTime = Date.parse(streamEventTimestamp(event, new Date().toISOString())) || Date.now();
      const activityAt = new Date(Math.floor(eventTime / 100) * 100).toISOString();
      const activityNode = wsNid || getNodeIdForRoot(activeRoot) || "";
      const advanceItem = (item: SessionItem): SessionItem => {
        const currentTime = Math.max(
          Date.parse(String(item.updated_at || "")) || 0,
          Date.parse(String(item.activity_at || "")) || 0,
        );
        return Date.parse(activityAt) > currentTime ? { ...item, activity_at: activityAt } : item;
      };
      setMultiProjectSessionGroups((prev) => {
        let changed = false;
        const next = prev.map((group) => {
          const groupNode = String(group._nodeId || "").trim();
          if (
            group.rootId !== activeRoot ||
            (activityNode && groupNode && activityNode !== groupNode)
          ) return group;
          const groupTime = Math.max(
            Date.parse(String(group.latestSessionTime || "")) || 0,
            Date.parse(String(group.activity_at || "")) || 0,
          );
          const sessions = group.sessions.map((item) => {
            if ((item.key || item.session_key) !== streamKey) return item;
            const itemNode = String(item._nodeId || groupNode).trim();
            if (activityNode && itemNode && activityNode !== itemNode) return item;
            const advanced = advanceItem(item);
            if (advanced !== item) changed = true;
            return advanced;
          });
          const activity_at = Date.parse(activityAt) > groupTime ? activityAt : group.activity_at;
          if (activity_at !== group.activity_at || sessions.some((item, i) => item !== group.sessions[i])) {
            changed = true;
            return { ...group, activity_at, sessions: mergeSessionItems(sessions, []) };
          }
          return group;
        });
        return changed ? next : prev;
      });
      if (activeRoot === currentRootIdRef.current && (!wsNid || !curNid || wsNid === curNid)) {
        setSessions((prev) => mergeSessionItems(prev.map((item) => {
          if ((item.key || item.session_key) !== streamKey || (item.root_id && item.root_id !== activeRoot)) return item;
          const itemNode = String(item._nodeId || "").trim();
          if (wsNid && itemNode && itemNode !== wsNid) return item;
          return advanceItem(item);
        }), []));
      }
    }
    if (wsNid && curNid && wsNid !== curNid) return;
    const ck = rootSessionKey(activeRoot, streamKey);
    let pending = pendingBySessionRef.current[ck];
    if (!pending) {
      const draft = pendingDraftRef.current;
      if (
        draft &&
        draft.rootId === activeRoot &&
        streamKey !==
          (suppressedAutoBindSessionByRootRef.current[scopedRootKey(activeRoot)] || "")
      ) {
        pending = draft;
        pendingBySessionRef.current[ck] = draft;
        pendingDraftRef.current = null;
        console.info("[session/stream] attach_pending_draft", { rootId: activeRoot, streamKey, requestId: draft.requestId, tempKey: draft.tempKey || null });
      }
    }
    const boundKey = boundSessionByRootRef.current[scopedRootKey(activeRoot)] || "";
    const suppressedAutoBindKey =
      suppressedAutoBindSessionByRootRef.current[scopedRootKey(activeRoot)] || "";
    if (
      streamKey !== suppressedAutoBindKey &&
      (!boundKey ||
        (typeof boundKey === "string" && boundKey.startsWith("pending-")))
    ) {
      if (suppressedAutoBindKey && streamKey !== suppressedAutoBindKey) {
        suppressedAutoBindSessionByRootRef.current[scopedRootKey(activeRoot)] = null;
      }
      setBoundSessionForRoot(activeRoot, streamKey);
      if (pending) {
        const pendingName =
          (drawerSessionByRootRef.current[scopedRootKey(activeRoot)]?.key ===
          pending.tempKey &&
          typeof (drawerSessionByRootRef.current[scopedRootKey(activeRoot)] as any)?.name ===
            "string"
            ? ((drawerSessionByRootRef.current[scopedRootKey(activeRoot)] as any).name as string)
            : "") ||
          ((selectedSessionRef.current?.key ||
            selectedSessionRef.current?.session_key) === pending.tempKey &&
          (((selectedSessionRef.current?.root_id as string | undefined) ||
            currentRootIdRef.current) === activeRoot) &&
          typeof selectedSessionRef.current?.name === "string"
            ? selectedSessionRef.current.name
            : "") ||
          t("session.new");
        const userEx = {
          role: "user",
          content: pending.message,
          timestamp: pending.timestamp,
          model: pending.model,
            mode: pending.agentMode,
            effort: pending.effort,
            fast_service: pending.fastService || "",
            shell: pending.shell || "",
        };
        const cached =
          sessionCacheRef.current[ck] ||
          ({
            key: streamKey,
            type: pending.mode,
            agent: pending.agent,
            model: pending.model,
              mode: pending.agentMode,
              effort: pending.effort,
              fast_service: pending.fastService || "",
              shell: pending.shell || "",
            name: pendingName,
            created_at: pending.timestamp,
            updated_at: pending.timestamp,
            exchanges: [],
          } as any);
        const prevExchanges = Array.isArray((cached as any).exchanges)
          ? ((cached as any).exchanges as Exchange[])
          : [];
        sessionCacheRef.current[ck] = {
          ...(cached as any),
          exchanges: prevExchanges.length > 0 ? prevExchanges : [userEx],
          updated_at: new Date().toISOString(),
        } as Session;
        bumpCacheVersion();
      }
      const seeded = sessionCacheRef.current[ck];
      if (seeded) {
        // pending 不写在会话对象上（唯一真相是 multiProjectPendingByKey，由上面的
        // markSessionPending 负责写入）。
        setDrawerSessionForRoot(activeRoot, seeded);
      }
    }
    if (pending?.tempKey) {
      console.info("[session/stream] promote_pending", { rootId: activeRoot, tempKey: pending.tempKey, streamKey, requestId: pending.requestId });
      promotePendingSessionForRoot(
        activeRoot,
        pending.tempKey,
        streamKey,
        sessionCacheRef.current[ck] || null,
      );
    }
    if (!event?.type) return;
    // message_chunk/thought_chunk 是最高频事件：首次进 pending 用幂等
    // markSessionPending（内部已在 pending 时只更新 ref 不 setState），
    // 缓存版本 bump 走 30ms debounce —— 100 token/s 时渲染次数从 100/s 降到 ~33/s。
    const markStreamPending = () => {
      if (event.type === "message_done" || event.type === "error") return;
      markSessionPending(activeRoot, streamKey);
    };
    markStreamPending();
    const isStreamingChunk =
      event.type === "message_chunk" || event.type === "thought_chunk";
    if (!isStreamingChunk) {
      bumpCacheVersion();
    }
    switch (event.type) {
      case "message_chunk":
        appendAgentChunkForSession(
          activeRoot,
          streamKey,
          event.data?.content || "",
          pending
            ? {
                agent: pending.agent,
                model: pending.model,
                  mode: pending.agentMode,
                  effort: pending.effort,
                fast_service: pending.fastService || "",
              }
            : undefined,
          event.timestamp,
          event.mindfs_id,
        );
        break;
      case "thought_chunk":
        appendThoughtChunkForSession(
          activeRoot,
          streamKey,
          event.data?.content || "",
          event.data?.id || "",
          event.mindfs_id,
        );
        break;
      case "tool_call":
        appendToolCallForSession(
          activeRoot,
          streamKey,
          event.data || {},
          false,
          event.mindfs_id,
        );
        break;
      case "tool_call_update":
        appendToolCallForSession(
          activeRoot,
          streamKey,
          event.data || {},
          true,
          event.mindfs_id,
        );
        break;
      case "todo_update":
        appendTodoUpdateForSession(
          activeRoot,
          streamKey,
          event.data || {},
          event.mindfs_id,
        );
        break;
      case "plan_update":
        appendPlanUpdateForSession(
          activeRoot,
          streamKey,
          event.data || {},
          event.mindfs_id,
        );
        break;
      case "compact_notice": {
        appendCompactNoticeForSession(
          activeRoot,
          streamKey,
          event.data || {},
          event.mindfs_id,
        );
        // **这里曾经清掉该会话所有瞬时行，已删除（2026-10-06）。** 原注释给的两条依据
        // 都不成立：
        //   · 「服务端历史整个重置了」—— 交换 JSONL 是 **append-only**（唯一 `os.Remove`
        //     在两处 `DeleteSession`），compact 只写一条 `CompactNotice` aux；
        //   · 「此刻没有在途回合，不存在误伤」—— `compact_notice` 是**轮内**流事件
        //     （本 `handleSessionStream` 的一个 case），当时正有在途回合。
        // 于是 compact 一到就把**本轮 compact 之前**还没落盘的内容一起丢掉（连刚追加的
        // 这条通知自己也在 seq=0 里）。陈旧瞬时行由另外两处负责：一轮结束 / reset 的
        // `dropTransientExchanges`，以及加载时 `composeLoadedExchanges(inFlight=false)`。
        // 见 docs/message-mechanisms.md 冲突⑨ 与 tests 里的【红·⑨c】。
        break;
      }
      case "message_done":
        attachContextWindowToLatestAssistant(
          activeRoot,
          streamKey,
          event.data?.contextWindow,
          event.data?.tokenUsage,
        );
        setCodexRateLimitsRefreshToken((value) => value + 1);
        break;
      case "error":
        reportError(
          "session.resume_failed",
          event.data?.message || t("session.resumeFailed"),
          {
            details: {
              rootId: activeRoot,
              sessionKey: streamKey,
              eventType: event.type,
            },
          },
        );
        handleSessionStreamDone(activeRoot, streamKey);
        break;
    }
  };
}
