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
function dedupeToolCards(
  items: TimelineItem[],
  auxiliaryCallIds: Set<string>,
  onDrop?: (info: {
    callId: string;
    kind: string;
    from: string;
    keptIndex: number;
    droppedIndex: number;
    total: number;
  }) => void,
): TimelineItem[] {
  const seen = new Map<string, number>();
  const out: TimelineItem[] = [];
  for (let index = 0; index < items.length; index += 1) {
    const item = items[index];
    if (item.type === "tool") {
      const callId = item.toolCall.callId;
      if (callId) {
        const kept = seen.get(callId);
        if (kept !== undefined) {
          onDrop?.({
            callId,
            kind: `${item.toolCall.kind || ""}`,
            from: auxiliaryCallIds.has(callId) ? "aux" : "exchange",
            keptIndex: kept,
            droppedIndex: index,
            total: items.length,
          });
          continue;
        }
        seen.set(callId, index);
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
    id: stableTimelineID(
      "assistant",
      index * 1000 + segmentIndex,
      content,
      ex.timestamp,
      ex.agent,
    ),
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

function buildBaseTimeline(
  exchanges: ExchangeLike[],
  exchangeAux: ExchangeAuxMapLike,
): TimelineItem[] {
  const out: TimelineItem[] = [];
  let inferredSeq = 0;
  for (let index = 0; index < exchanges.length; index += 1) {
    const ex = exchanges[index];
    const role = normalizeRole(ex.role);
    const content = ex.content || "";
    if (role === "user") {
      inferredSeq += 1;
      const seq = Number(ex.seq || 0) > 0 ? Number(ex.seq || 0) : inferredSeq;
      if (!content) continue;
      out.push({
        id: stableTimelineID("user", index, content, ex.timestamp, ex.agent),
        type: "user_text",
        content,
        timestamp: ex.timestamp,
        agent: ex.agent,
        pendingAck: ex.pending_ack === true,
        seq,
      });
      continue;
    }
    if (role === "agent" || role === "assistant") {
      inferredSeq += 1;
      const seq = Number(ex.seq || 0) > 0 ? Number(ex.seq || 0) : inferredSeq;
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

  // 诊断锚点（ask 重复）—「渲染源」对账。同一 callId 可能同时躺在
  //   ① 窗口的 exchange_aux（buildAssistantTimeline 按 aux.toolcall 渲染一张卡）
  //   ② 缓存的 role=tool 条目（buildBaseTimeline 的 role==="tool" 分支再渲染一张）
  // 两侧各渲染一次且互不去重，UI 上就是同一张 ask 出现两次。另有第三种：同一 callId
  // 在 aux 里跨多个 seq 重复出现（aux 不保证跨轮唯一），而 Set 化的 windowToolCallIds
  // 会把它并成一条、掩盖份数。只在重复特征变化时打一行，避免每次重渲染刷屏。
  const lastToolDupSigRef = useRef("");
  useEffect(() => {
    if (!sessionKey) return;
    const auxSites = new Map<
      string,
      { n: number; kind: string; seqs: number[]; lines: number[] }
    >();
    for (const [seqKey, items] of Object.entries(exchangeAux || {})) {
      for (const aux of items || []) {
        const tool = (aux as any)?.toolcall;
        const callId = typeof tool?.callId === "string" ? tool.callId : "";
        if (!callId) continue;
        const entry =
          auxSites.get(callId) ||
          { n: 0, kind: `${tool?.kind || ""}`, seqs: [], lines: [] };
        entry.n += 1;
        entry.seqs.push(Number(seqKey));
        entry.lines.push(Number((aux as any)?.line || 0));
        auxSites.set(callId, entry);
      }
    }
    const exchangeSites = new Map<string, number>();
    for (const ex of exchanges) {
      if (String((ex as any)?.role || "").toLowerCase() !== "tool") continue;
      const callId = String((ex as any)?.toolCall?.callId || "");
      if (callId) {
        exchangeSites.set(callId, (exchangeSites.get(callId) || 0) + 1);
      }
    }
    const duplicatedInAux = [...auxSites.entries()].filter(([, v]) => v.n > 1);
    const inBothSources = [...auxSites.keys()].filter((id) =>
      exchangeSites.has(id),
    );
    if (!duplicatedInAux.length && !inBothSources.length) {
      lastToolDupSigRef.current = "";
      return;
    }
    const signature = JSON.stringify([
      duplicatedInAux.map(([id, v]) => [id, v.n, v.seqs]),
      inBothSources.map((id) => [id, exchangeSites.get(id)]),
    ]);
    if (signature === lastToolDupSigRef.current) return;
    lastToolDupSigRef.current = signature;
    console.warn("[ask/probe] tool-source-dup", {
      sessionKey,
      auxSeqs: Object.keys(exchangeAux || {}).length,
      duplicatedInAux: duplicatedInAux.map(([callId, v]) => ({
        callId,
        kind: v.kind,
        copies: v.n,
        seqs: v.seqs,
        lines: v.lines,
      })),
      inBothSources: inBothSources.map((callId) => ({
        callId,
        kind: auxSites.get(callId)?.kind || "",
        exchangeCopies: exchangeSites.get(callId) || 0,
      })),
    });
  }, [sessionKey, exchangeAux, exchanges]);

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

  const settledTimeline = useMemo(() => {
    const auxiliaryCallIds = new Set<string>();
    for (const items of Object.values(exchangeAux || {})) {
      for (const aux of items || []) {
        const callId = (aux as any)?.toolcall?.callId;
        if (typeof callId === "string" && callId) auxiliaryCallIds.add(callId);
      }
    }
    return dedupeToolCards(settleRunningTools(baseTimeline), auxiliaryCallIds, (info) => {
      console.warn("[ask/probe] dedupe-tool-card", { sessionKey, ...info });
    });
  }, [baseTimeline, exchangeAux, sessionKey]);

  return {
    timeline: settledTimeline,
    isStreaming,
    streamVersion,
    streamStatusText,
  };
}
