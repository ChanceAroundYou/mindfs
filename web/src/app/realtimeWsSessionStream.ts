import { mergeSessionItems } from "../services/session/sessionListMerge";
import { streamEventTimestamp } from "../services/session/sessionDuration";
import { reportError } from "../services/net/error";
import type { SessionItem } from "./appSession";
import type { RealtimeEventsContext } from "./useRealtimeEventsContext";

type PendingChunk = {
  rootID: string;
  sessionKey: string;
  content: string;
  thoughtID?: string;
  runtimeMeta?: unknown;
  timestamp?: string;
  mindfsID?: string;
};

type ChunkAppliers = {
  agent: (
    rootID: string,
    sessionKey: string,
    content: string,
    runtimeMeta: any,
    timestamp?: any,
    mindfsID?: string,
  ) => void;
  thought: (
    rootID: string,
    sessionKey: string,
    content: string,
    thoughtID: string,
    mindfsID?: string,
  ) => void;
};

/**
 * 流式文本块按 ~16ms 合流后再写缓存。
 *
 * 每条 chunk 单独 upsert 的代价不是渲染（那已由 30ms debounce 收口），而是
 * `[...exchanges]`、按 mindfs_id 线性查找、`content + chunk` 拼接 —— 在长回合里
 * E 和 L 都很大，逐条应用是 O(E) / O(L)，整轮累计 O(E²) / O(L²)。同一帧内把
 * 同一 exchange 的文本拼好再写一次，数量级直接降一个档。
 *
 * 放在模块级而不是 handler 闭包里：`createRealtimeWsSessionStreamHandler` 会因为
 * 依赖变化重建，闭包里的缓冲会丢文本。
 */
const pendingAgentChunks = new Map<string, PendingChunk>();
const pendingThoughtChunks = new Map<string, PendingChunk>();
let chunkFlushTimer: ReturnType<typeof setTimeout> | null = null;
let chunkAppliers: ChunkAppliers | null = null;

function scheduleChunkFlush() {
  if (chunkFlushTimer !== null) return;
  chunkFlushTimer = setTimeout(() => {
    chunkFlushTimer = null;
    flushPendingChunks();
  }, 16);
}

export function flushPendingChunks() {
  if (chunkFlushTimer !== null) {
    clearTimeout(chunkFlushTimer);
    chunkFlushTimer = null;
  }
  const appliers = chunkAppliers;
  if (!appliers) return;
  const agents = Array.from(pendingAgentChunks.values());
  const thoughts = Array.from(pendingThoughtChunks.values());
  pendingAgentChunks.clear();
  pendingThoughtChunks.clear();
  for (const entry of agents) {
    appliers.agent(
      entry.rootID,
      entry.sessionKey,
      entry.content,
      entry.runtimeMeta,
      entry.timestamp,
      entry.mindfsID,
    );
  }
  for (const entry of thoughts) {
    appliers.thought(
      entry.rootID,
      entry.sessionKey,
      entry.content,
      entry.thoughtID || "",
      entry.mindfsID,
    );
  }
}

function enqueueAgentChunk(entry: PendingChunk) {
  const key = `${entry.rootID}|${entry.sessionKey}|${entry.mindfsID || ""}`;
  const prev = pendingAgentChunks.get(key);
  if (prev) {
    prev.content += entry.content;
    if (entry.timestamp) prev.timestamp = entry.timestamp;
    if (entry.runtimeMeta) prev.runtimeMeta = entry.runtimeMeta;
  } else {
    pendingAgentChunks.set(key, { ...entry });
  }
  scheduleChunkFlush();
}

function enqueueThoughtChunk(entry: PendingChunk) {
  const key = `${entry.rootID}|${entry.sessionKey}|${entry.mindfsID || ""}|${entry.thoughtID || ""}`;
  const prev = pendingThoughtChunks.get(key);
  if (prev) {
    prev.content += entry.content;
    if (entry.timestamp) prev.timestamp = entry.timestamp;
  } else {
    pendingThoughtChunks.set(key, { ...entry });
  }
  scheduleChunkFlush();
}

/** 会话 stream 事件的归属、缓存投影与消息分发。 */
export function createRealtimeWsSessionStreamHandler(
  ctx: RealtimeEventsContext,
  local: { handleSessionStreamDone: (rootID: string, sessionKey: string) => boolean },
) {
  const {
    refs: {
      currentRootIdRef, currentRootNodeIdRef, pendingBySessionRef,
    },
    setters: {
      setCodexRateLimitsRefreshToken, setMultiProjectSessionGroups, setSessions,
    },
    actions: {
      appendAgentChunkForSession, appendCompactNoticeForSession, appendPlanUpdateForSession,
      appendThoughtChunkForSession, appendTodoUpdateForSession, appendToolCallForSession,
      attachContextWindowToLatestAssistant, bumpCacheVersion, getNodeIdForRoot,
      markSessionPending, playNotificationSound, resolveRootForSessionKey,
    },
    values: { rootSessionKey, t },
  } = ctx;
  const { handleSessionStreamDone } = local;
  // 模块级合流器拿最新的 append 回调（回调本身是 useCallback 稳定的，这里只是
  // 防止 handler 重建后引用变旧）。
  chunkAppliers = {
    agent: appendAgentChunkForSession,
    thought: appendThoughtChunkForSession,
  };
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
        const currentTime = Date.parse(String(item.updated_at || "")) || 0;
        return Date.parse(activityAt) > currentTime ? { ...item, updated_at: activityAt } : item;
      };
      setMultiProjectSessionGroups((prev) => {
        let changed = false;
        const next = prev.map((group) => {
          const groupNode = String(group._nodeId || "").trim();
          if (
            group.rootId !== activeRoot ||
            (activityNode && groupNode && activityNode !== groupNode)
          ) return group;
          const groupTime = Date.parse(String(group.latestSessionTime || "")) || 0;
          const sessions = group.sessions.map((item) => {
            if ((item.key || item.session_key) !== streamKey) return item;
            const itemNode = String(item._nodeId || groupNode).trim();
            if (activityNode && itemNode && activityNode !== itemNode) return item;
            const advanced = advanceItem(item);
            if (advanced !== item) changed = true;
            return advanced;
          });
          if (Date.parse(activityAt) > groupTime || sessions.some((item, i) => item !== group.sessions[i])) {
            changed = true;
            return { ...group, sessions: mergeSessionItems(sessions, []) };
          }
          return group;
        });
        return changed ? next : prev;
      });
      if (activeRoot === currentRootIdRef.current && (!wsNid || !curNid || wsNid === curNid)) {
        // 高频道事件（message_chunk ~100/s）本来会被这里逐条重排整个列表。advanceItem
        // 按 100ms 桶推进，同一桶内返回原对象；没有任何条目变化时直接返回 prev，
        // 让 React bail out，而不是每次 mergeSessionItems 都造一个新数组。
        setSessions((prev) => {
          let changed = false;
          const next = prev.map((item) => {
            if ((item.key || item.session_key) !== streamKey || (item.root_id && item.root_id !== activeRoot)) return item;
            const itemNode = String(item._nodeId || "").trim();
            if (wsNid && itemNode && itemNode !== wsNid) return item;
            const advanced = advanceItem(item);
            if (advanced !== item) changed = true;
            return advanced;
          });
          return changed ? mergeSessionItems(next, []) : prev;
        });
      }
    }
    if (wsNid && curNid && wsNid !== curNid) return;
    const ck = rootSessionKey(activeRoot, streamKey);
    const pending = pendingBySessionRef.current[ck];
    if (!event?.type) return;
    // message_chunk/thought_chunk 是最高频事件：首次进 pending 用幂等
    // markSessionPending（内部已在 pending 时只更新 ref 不 setState），
    // 缓存版本 bump 走 30ms debounce —— 100 token/s 时渲染次数从 100/s 降到 ~33/s。
    const markStreamPending = () => {
      if (event.type === "message_done" || event.type === "error") return;
      markSessionPending(activeRoot, streamKey);
    };
    markStreamPending();
    // message_chunk / thought_chunk / tool_call_update 是最高频事件：缓存版本 bump
    // 走 30ms debounce（append* 内部已调 bumpCacheVersionDebounced）。工具输出流式
    // 续传以前走立即 bump，一条长命令能把整棵会话视图按事件频率重渲染。
    const isCoalescedEvent =
      event.type === "message_chunk" ||
      event.type === "thought_chunk" ||
      event.type === "tool_call_update";
    if (!isCoalescedEvent) {
      bumpCacheVersion();
    }
    // 文本块是延迟合流的；结构性事件之前必须先落盘，否则「文本→工具→文本」的
    // 顺序会因为延迟 flush 而错位。
    if (event.type !== "message_chunk" && event.type !== "thought_chunk") {
      flushPendingChunks();
    }
    switch (event.type) {
      case "message_chunk":
        enqueueAgentChunk({
          rootID: activeRoot,
          sessionKey: streamKey,
          content: event.data?.content || "",
          runtimeMeta: pending
            ? {
                agent: pending.agent,
                model: pending.model,
                  mode: pending.agentMode,
                  effort: pending.effort,
                fast_service: pending.fastService || "",
              }
            : undefined,
          timestamp: event.timestamp,
          mindfsID: event.mindfs_id,
        });
        break;
      case "thought_chunk":
        enqueueThoughtChunk({
          rootID: activeRoot,
          sessionKey: streamKey,
          content: event.data?.content || "",
          thoughtID: event.data?.id || "",
          timestamp: event.timestamp,
          mindfsID: event.mindfs_id,
        });
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
        // agent 侧出错（含 resume 失败）：这条不是 WS 层 session.error，
        // 后者只覆盖「请求没发出去」，两者都要响，用同一个错误音。
        playNotificationSound("error");
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
