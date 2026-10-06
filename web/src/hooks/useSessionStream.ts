import { useEffect, useMemo, useRef, useState } from "react";
import {
  sessionService,
  type CompactNotice,
  type ExchangeAux,
  type PlanUpdate,
  type TodoUpdate,
  type ToolCall,
  type TokenUsage,
} from "../services/session";
import { translateNow } from "../i18n";

type ExchangeLike = {
  seq?: number;
  role?: string;
  agent?: string;
  model?: string;
  model_display_name?: string;
  effort?: string;
  fast_service?: string;
  content?: string;
  thought_id?: string;
  context_window?: {
    totalTokens: number;
    modelContextWindow: number;
  };
  token_usage?: TokenUsage;
  timestamp?: string;
  toolCall?: ToolCall;
  todoUpdate?: TodoUpdate;
  planUpdate?: PlanUpdate;
  compactNotice?: CompactNotice;
  pending_ack?: boolean;
};

type ExchangeAuxMapLike = Record<string, ExchangeAux[]>;

export type TimelineItem =
  | {
      id: string;
      type: "user_text" | "assistant_text";
      content: string;
      timestamp?: string;
      agent?: string;
      model?: string;
      modelDisplayName?: string;
      effort?: string;
      fastService?: string;
      pendingAck?: boolean;
      seq?: number;
      contextWindow?: {
        totalTokens: number;
        modelContextWindow: number;
      };
      tokenUsage?: TokenUsage;
    }
  | { id: string; type: "thought"; content: string }
  | { id: string; type: "tool"; toolCall: ToolCall }
  // 连续同类工具卡的折叠组（2026-10-07）：agent 一个回合里连开 100+ 个 edit/read/
  // execute 时，逐个渲染 ToolCallCard 是「打开会话卡」的主要渲染成本（实测单会话
  // 389 张卡、单 seq 189 张）。折叠成一张「N 个编辑」的组卡后 DOM 从几百张降到几张，
  // 展开组卡时才逐个渲染。ask_user 不参与分组（它要交互、要答题）。
  | { id: string; type: "tool_group"; kind: string; toolCalls: ToolCall[] }
  | { id: string; type: "todo"; todoUpdate: TodoUpdate; timestamp?: string }
  | { id: string; type: "plan"; planUpdate: PlanUpdate; timestamp?: string }
  | { id: string; type: "compact"; compactNotice: CompactNotice; timestamp?: string };

type UseSessionStreamResult = {
  timeline: TimelineItem[];
  isStreaming: boolean;
  streamVersion: number;
  streamStatusText: string;
};

type ContextWindowLike = {
  totalTokens: number;
  modelContextWindow: number;
};

function hashText(input: string): string {
  let hash = 2166136261;
  for (let i = 0; i < input.length; i += 1) {
    hash ^= input.charCodeAt(i);
    hash = Math.imul(hash, 16777619);
  }
  return (hash >>> 0).toString(36);
}

function stableTimelineID(
  prefix: string,
  index: number,
  content: string,
  timestamp?: string,
  agent?: string,
): string {
  return `${prefix}:${index}:${timestamp || ""}:${agent || ""}:${hashText(content)}`;
}

function normalizeRole(role?: string): string {
  return (role || "").toLowerCase();
}

function normalizeToolCallStatus(status?: string): string {
  const value = (status || "").toLowerCase();
  if (value === "completed") return "complete";
  if (value === "pending") return "running";
  return value || "running";
}

function normalizeToolCall(input: ToolCall): ToolCall {
  const raw = input as ToolCall & {
    toolCallId?: string;
    tool_call_id?: string;
  };
  const callId = raw.callId || raw.toolCallId || raw.tool_call_id || "";
  return {
    ...input,
    callId,
    status: normalizeToolCallStatus(raw.status),
  };
}

// 同一 callId 只允许渲染一张工具卡。窗口侧（exchange_aux[seq].toolcall）与 overlay 侧
// （缓存里的 role=tool 瞬时条目）是两条独立来源，两边都会给出同一个 callId；一旦同时命中，
// 列表里就出现两个 id 相同的 item —— 既重复渲染，又制造 React 重复 key，后者会让卡片被
// 摆到错误的位置（实测 2026-09-13：ask 卡出现在窗口内真实位置之外的地方）。
// composedExchanges 是「窗口在前、overlay 在后」，所以保留首个 = 保留已持久化那份，
// 符合「窗口是唯一持久化源」的既有约定。
function dedupeToolCards(items: TimelineItem[]): TimelineItem[] {
  const seen = new Set<string>();
  const out: TimelineItem[] = [];
  for (const item of items) {
    if (item.type === "tool") {
      const callId = item.toolCall.callId;
      if (callId) {
        if (seen.has(callId)) continue;
        seen.add(callId);
      }
    }
    out.push(item);
  }
  return out;
}

function settleRunningTools(items: TimelineItem[]): TimelineItem[] {
  return items.map((item) => {
    if (item.type !== "tool") return item;
    const kind = (item.toolCall.kind || "").toLowerCase();
    if (kind === "ask_user" || kind === "task") return item;
    const status = (item.toolCall.status || "").toLowerCase();
    if (
      status === "running" ||
      status === "in_progress" ||
      status === "pending"
    ) {
      return {
        ...item,
        toolCall: {
          ...item.toolCall,
          status: "complete",
        },
      };
    }
    return item;
  });
}

function assistantSegmentItem(
  index: number,
  ex: ExchangeLike,
  content: string,
  segmentIndex: number,
  includeContextWindow: boolean,
): TimelineItem | null {
  if (!content) {
    return null;
  }
  return {
    // Content and timestamps change during streaming. Identity belongs to the
    // exchange/segment, so React and virtual measurements survive each chunk.
    id: `assistant:${index}:${segmentIndex}`,
    type: "assistant_text",
    content,
    timestamp: ex.timestamp,
    agent: ex.agent,
    model: ex.model,
    modelDisplayName: ex.model_display_name,
    effort: ex.effort,
    fastService: ex.fast_service,
    seq: ex.seq,
    contextWindow: includeContextWindow ? ex.context_window : undefined,
    tokenUsage: includeContextWindow ? ex.token_usage : undefined,
  };
}

function buildAssistantTimeline(
  ex: ExchangeLike,
  index: number,
  auxList: ExchangeAux[],
): TimelineItem[] {
  const content = ex.content || "";
  if (!auxList.length) {
    const single = assistantSegmentItem(index, ex, content, 0, true);
    return single ? [single] : [];
  }

  const lines = content === "" ? [] : content.split("\n");
  const totalLines = lines.length;
  const out: TimelineItem[] = [];
  const normalizedAux = auxList.map((aux, auxIndex) => ({
    ...aux,
    auxIndex,
    line: Math.max(0, Math.min(totalLines, Number(aux.line || 0))),
  }));
  normalizedAux.sort((left, right) => {
    if (left.line !== right.line) {
      return left.line - right.line;
    }
    return left.auxIndex - right.auxIndex;
  });

  let emittedLines = 0;
  let segmentIndex = 0;
  for (const aux of normalizedAux) {
    if (aux.line > emittedLines) {
      const segment = assistantSegmentItem(
        index,
        ex,
        lines.slice(emittedLines, aux.line).join("\n"),
        segmentIndex,
        false,
      );
      if (segment) {
        out.push(segment);
        segmentIndex += 1;
      }
      emittedLines = aux.line;
    }
    if (aux.thought) {
      out.push({
        id:
          aux.thought_id ||
          stableTimelineID(
            "thought",
            index * 1000 + segmentIndex,
            aux.thought,
            ex.timestamp,
            ex.agent,
          ),
        type: "thought",
        content: aux.thought,
      });
      segmentIndex += 1;
    } else if (aux.plan) {
      out.push({
        id:
          aux.plan.id ||
          stableTimelineID(
            "plan",
            index * 1000 + segmentIndex,
            aux.plan.content || "",
            ex.timestamp,
            ex.agent,
          ),
        type: "plan",
        planUpdate: aux.plan,
        timestamp: ex.timestamp,
      });
      segmentIndex += 1;
    } else if (aux.todo) {
      out.push({
        id: stableTimelineID(
          "todo",
          index * 1000 + segmentIndex,
          JSON.stringify(aux.todo),
          ex.timestamp,
          ex.agent,
        ),
        type: "todo",
        todoUpdate: aux.todo,
        timestamp: ex.timestamp,
      });
      segmentIndex += 1;
    } else if (aux.compact) {
      out.push({
        id:
          aux.compact.id ||
          stableTimelineID(
            "compact",
            index * 1000 + segmentIndex,
            JSON.stringify(aux.compact),
            ex.timestamp,
            ex.agent,
          ),
        type: "compact",
        compactNotice: aux.compact,
        timestamp: ex.timestamp,
      });
      segmentIndex += 1;
    } else if (aux.toolcall) {
      const normalizedTool = normalizeToolCall(aux.toolcall);
      out.push({
        id:
          normalizedTool.callId ||
          stableTimelineID(
            "tool",
            index * 1000 + segmentIndex,
            JSON.stringify(normalizedTool),
            ex.timestamp,
            ex.agent,
          ),
        type: "tool",
        toolCall: normalizedTool,
      });
      segmentIndex += 1;
    }
  }

  if (emittedLines < totalLines) {
    const segment = assistantSegmentItem(
      index,
      ex,
      lines.slice(emittedLines).join("\n"),
      segmentIndex,
      true,
    );
    if (segment) {
      out.push(segment);
      segmentIndex += 1;
    }
  } else {
    for (let i = out.length - 1; i >= 0; i -= 1) {
      const item = out[i];
      if (item.type === "assistant_text") {
        out[i] = {
          ...item,
          contextWindow: ex.context_window,
          tokenUsage: ex.token_usage,
        };
        break;
      }
    }
  }

  return out;
}

/**
 * 这一行在持久化序列里的位置。**没有就返回 0，绝不编造。**
 *
 * 曾经这里按「第几条 user/agent 行」推一个位置，好让没有 seq 的
 * 瞬时行也能在时间线上排个序。它造成了两个可观测的错误：
 *
 *  ① 瞬时行凭空获得一个**已经属于别人**的 seq。服务端的 exchange_aux 是**按 seq 索引**的
 *     （工具卡、token 用量都挂在 aux[seq] 下），于是这一行会把某条真实持久行的 aux
 *     一并认领过来 —— 用户看到的正是「正文下方多出一整套工具卡」。
 *  ② `data-session-seq` 与 fork 按钮都以 `seq > 0` 为「这是持久消息」的判据，
 *     编造出来的 seq 让**还没落盘的瞬时行**看起来可以 fork。
 *
 * 排序本来就不需要它：瞬时行永远排在持久行之后（`composeLoadedExchanges` 的契约），
 * 数组顺序即时间顺序，`seq` 只是「服务端窗口里的身份」，本就不该由位置反推。
 */
function persistedSeq(ex: ExchangeLike): number {
  const raw = Number(ex.seq || 0);
  return Number.isFinite(raw) && raw > 0 ? raw : 0;
}

function buildBaseTimeline(
  exchanges: ExchangeLike[],
  exchangeAux: ExchangeAuxMapLike,
): TimelineItem[] {
  const out: TimelineItem[] = [];
  for (let index = 0; index < exchanges.length; index += 1) {
    const ex = exchanges[index];
    const role = normalizeRole(ex.role);
    const content = ex.content || "";
    if (role === "user") {
      const seq = persistedSeq(ex);
      if (!content) continue;
      out.push({
        id: stableTimelineID("user", index, content, ex.timestamp, ex.agent),
        type: "user_text",
        content,
        timestamp: ex.timestamp,
        agent: ex.agent,
        pendingAck: ex.pending_ack === true,
        seq: seq || undefined,
      });
      continue;
    }
    if (role === "agent" || role === "assistant") {
      const seq = persistedSeq(ex);
      const auxList = seq ? exchangeAux[String(seq)] || [] : [];
      out.push(...buildAssistantTimeline({ ...ex, seq }, index, auxList));
      continue;
    }
    if (role === "thought") {
      if (!content) continue;
      out.push({
        id:
          ex.thought_id ||
          stableTimelineID("thought", index, content, ex.timestamp, ex.agent),
        type: "thought",
        content,
      });
      continue;
    }
    if (role === "tool") {
      if (!ex.toolCall) continue;
      const normalizedTool = normalizeToolCall(ex.toolCall);
      out.push({
        id:
          normalizedTool.callId ||
          stableTimelineID(
            "tool",
            index,
            JSON.stringify(normalizedTool),
            ex.timestamp,
            ex.agent,
          ),
        type: "tool",
        toolCall: normalizedTool,
      });
      continue;
    }
    if (role === "todo") {
      if (!ex.todoUpdate) continue;
      out.push({
        id: stableTimelineID(
          "todo",
          index,
          JSON.stringify(ex.todoUpdate),
          ex.timestamp,
          ex.agent,
        ),
        type: "todo",
        todoUpdate: ex.todoUpdate,
        timestamp: ex.timestamp,
      });
      continue;
    }
    if (role === "plan") {
      if (!ex.planUpdate) continue;
      out.push({
        id: ex.planUpdate.id || stableTimelineID("plan", index, ex.planUpdate.content || "", ex.timestamp, ex.agent),
        type: "plan",
        planUpdate: ex.planUpdate,
        timestamp: ex.timestamp,
      });
      continue;
    }
    if (role === "compact") {
      if (!ex.compactNotice) continue;
      out.push({
        id: ex.compactNotice.id || stableTimelineID("compact", index, JSON.stringify(ex.compactNotice), ex.timestamp, ex.agent),
        type: "compact",
        compactNotice: ex.compactNotice,
        timestamp: ex.timestamp,
      });
    }
  }
  return groupConsecutiveToolCalls(out);
}

/**
 * 连续同类工具卡的折叠（2026-10-07）。
 *
 * 为什么：一个回合里 agent 连开 100+ 个 edit/read/execute 是常态，逐个渲染
 * ToolCallCard 是「打开会话卡」的主要渲染成本（实测单会话 389 张、单 seq 189 张）。
 * 折叠成一张组卡后首屏 DOM 从几百张降到几张；展开组卡才逐个渲染。
 *
 * 只折「详情在展开时才需要」的 kind（edit/read/execute）：它们的折叠卡片本来就
 * 只有标题+状态，折成一组不丢信息。ask_user 必须保持独立（要答题），
 * todo/plan/compact 有各自卡片，都不参与。
 *
 * 阈值 5：2–4 张卡直接显示更直观，为它们套一层「展开/收起」反而多一次点击。
 */
const GROUPABLE_TOOL_KINDS = new Set(["edit", "read", "execute"]);
const TOOL_GROUP_MIN = 5;

export function groupConsecutiveToolCalls(items: TimelineItem[]): TimelineItem[] {
  const out: TimelineItem[] = [];
  let i = 0;
  while (i < items.length) {
    const item = items[i];
    if (item.type !== "tool") {
      out.push(item);
      i += 1;
      continue;
    }
    const kind = `${item.toolCall?.kind || ""}`.toLowerCase();
    if (!GROUPABLE_TOOL_KINDS.has(kind)) {
      out.push(item);
      i += 1;
      continue;
    }
    let j = i + 1;
    while (j < items.length) {
      const next = items[j];
      if (next.type !== "tool") break;
      if (`${next.toolCall?.kind || ""}`.toLowerCase() !== kind) break;
      j += 1;
    }
    const run = items.slice(i, j) as Array<Extract<TimelineItem, { type: "tool" }>>;
    if (run.length < TOOL_GROUP_MIN) {
      out.push(...run);
      i = j;
      continue;
    }
    out.push({
      id: `tool-group:${kind}:${run[0]?.id || i}`,
      type: "tool_group",
      kind,
      toolCalls: run.map((entry) => entry.toolCall),
    });
    i = j;
  }
  return out;
}

function applySessionContextWindow(
  items: TimelineItem[],
  contextWindow?: ContextWindowLike,
): TimelineItem[] {
  const totalTokens = Math.max(0, Number(contextWindow?.totalTokens || 0));
  const modelContextWindow = Math.max(
    0,
    Number(contextWindow?.modelContextWindow || 0),
  );
  if (!totalTokens || !modelContextWindow) {
    return items;
  }
  for (let i = items.length - 1; i >= 0; i -= 1) {
    const item = items[i];
    if (item.type !== "assistant_text") {
      continue;
    }
    if (
      item.contextWindow?.totalTokens &&
      item.contextWindow?.modelContextWindow
    ) {
      return items;
    }
    const next = [...items];
    next[i] = {
      ...item,
      contextWindow: {
        totalTokens,
        modelContextWindow,
      },
    };
    return next;
  }
  return items;
}

export function useSessionStream(
  sessionKey: string | null,
  exchanges: ExchangeLike[] = [],
  exchangeAux: ExchangeAuxMapLike = {},
  sessionContextWindow?: ContextWindowLike,
  sessionPending = false,
): UseSessionStreamResult {
  const [isStreaming, setIsStreaming] = useState(false);
  const [streamVersion, setStreamVersion] = useState(0);
  const [streamStatusText, setStreamStatusText] = useState("");
  // 流式 chunk（message_chunk/thought_chunk）高频到达时合并 streamVersion 更新，
  // 避免每 chunk 一次 setState → SessionViewer 重渲染风暴（与 App.tsx 的
  // bumpCacheVersionDebounced 同思路）。滚动跟随用 30ms 粒度视觉无差异。
  const chunkVersionTimerRef = useRef<number | null>(null);
  const bumpStreamVersion = () => {
    if (chunkVersionTimerRef.current !== null) return;
    chunkVersionTimerRef.current = window.setTimeout(() => {
      chunkVersionTimerRef.current = null;
      setStreamVersion((value) => value + 1);
    }, 30);
  };

  const baseTimeline = useMemo(
    () =>
      applySessionContextWindow(
        buildBaseTimeline(exchanges, exchangeAux),
        sessionContextWindow,
      ),
    [exchanges, exchangeAux, sessionContextWindow],
  );

  useEffect(() => {
    setStreamVersion(0);
    setStreamStatusText("");
    if (!sessionKey) {
      setIsStreaming(false);
      return;
    }
    setIsStreaming(
      sessionPending && sessionService.isSessionStreaming(sessionKey),
    );

    const unsubscribe = sessionService.subscribe(sessionKey, {
      onStream: (event) => {
        if (event.type === "message_chunk" || event.type === "thought_chunk") {
          bumpStreamVersion();
        } else {
          setStreamVersion((value) => value + 1);
        }
        if (event.type === "recovery") {
          setStreamStatusText(event.data?.message || translateNow("session.recovering"));
          setIsStreaming(true);
          return;
        }
        if (event.type === "message_chunk") {
          setStreamStatusText("");
        }
        if (event.type === "message_done") {
          // 一轮消息已完成：立即清除流式标记，避免"正在生成"卡到下个事件。
          setStreamStatusText("");
          setIsStreaming(false);
          return;
        }
        if (event.type === "error") {
          setStreamStatusText("");
          setIsStreaming(false);
        } else {
          setIsStreaming(true);
        }
      },
      onDone: () => {
        setStreamStatusText("");
        setIsStreaming(false);
      },
      onError: () => {
        setStreamStatusText("");
        setIsStreaming(false);
      },
    });

    return () => {
      unsubscribe();
      if (chunkVersionTimerRef.current !== null) {
        window.clearTimeout(chunkVersionTimerRef.current);
        chunkVersionTimerRef.current = null;
      }
    };
  }, [sessionKey, sessionPending]);

  // Viewport-only rerenders must not look like new session content to the
  // viewer's tail-following effect. dedupeToolCards 在 settleRunningTools 之后：
  // 先把 running 收成终态，再折叠重复 tool 卡片，顺序反了会漏掉仍在跑的那张。
  const settledTimeline = useMemo(
    () => dedupeToolCards(settleRunningTools(baseTimeline)),
    [baseTimeline],
  );

  return {
    timeline: settledTimeline,
    isStreaming,
    streamVersion,
    streamStatusText,
  };
}
