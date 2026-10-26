import type { CompactNotice, ExchangeAux, PlanUpdate, TodoUpdate, TokenUsage, ToolCall } from "../services/session";
import { buildAssistantTimeline } from "./sessionAssistantTimeline";
import { stableTimelineID } from "./stableTimelineID";
import {
  blocksForSeq,
  blocksTextContent,
  buildAssistantTimelineFromBlocks,
  isBlankText,
  normalizeToolCall,
  type BlockExchangeLike,
  type SessionBlockMapLike,
  type TimelineItem,
} from "./sessionBlocks";

export type { TimelineItem } from "./sessionBlocks";

// CUSTOM(G-AT): consecutive tool cards are grouped here so large sessions render in bounded card batches.

export type ExchangeLike = {
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

export type ExchangeAuxMapLike = Record<string, ExchangeAux[]>;

export type UseSessionStreamResult = {
  timeline: TimelineItem[];
  isStreaming: boolean;
  streamVersion: number;
  streamStatusText: string;
};

export type ContextWindowLike = {
  totalTokens: number;
  modelContextWindow: number;
};


function normalizeRole(role?: string): string {
  return (role || "").toLowerCase();
}

// 同一 callId 只允许渲染一张工具卡。窗口侧（exchange_aux[seq].toolcall）与 overlay 侧
// （缓存里的 role=tool 瞬时条目）是两条独立来源，两边都会给出同一个 callId；一旦同时命中，
// 列表里就出现两个 id 相同的 item —— 既重复渲染，又制造 React 重复 key，后者会让卡片被
// 摆到错误的位置（实测 2026-09-13：ask 卡出现在窗口内真实位置之外的地方）。
// composedExchanges 是「窗口在前、overlay 在后」，所以保留首个 = 保留已持久化那份，
// 符合「窗口是唯一持久化源」的既有约定。
export function dedupeToolCards(items: TimelineItem[]): TimelineItem[] {
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

export function settleRunningTools(items: TimelineItem[]): TimelineItem[] {
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

export function buildBaseTimeline(
  exchanges: ExchangeLike[],
  exchangeAux: ExchangeAuxMapLike = {},
  blocks: SessionBlockMapLike = {},
): TimelineItem[] {
  return groupConsecutiveToolCalls(buildTimelineItems(exchanges, exchangeAux, blocks));
}

/**
 * 未分组的条目生成（CUSTOM(G-KC) 缓存用）。分组由 buildBaseTimeline 在整体上跑一次，
 * 保证跨 exchange 的连续同类工具卡仍能合并。
 */
export function buildTimelineItems(
  exchanges: ExchangeLike[],
  exchangeAux: ExchangeAuxMapLike = {},
  blocks: SessionBlockMapLike = {},
  // CUSTOM(G-KC): 瞬时行（seq=0）没有跨会话唯一的身份，只能用位置当 token。
  // 分片缓存逐条调用时传的是**全数组位置**（单元素数组的局部下标恒为 0）。
  // 持久行不受影响 —— 它们用 `s<seq>`。
  startIndex = 0,
): TimelineItem[] {
  const out: TimelineItem[] = [];
  for (let index = 0; index < exchanges.length; index += 1) {
    const ex = exchanges[index];
    const role = normalizeRole(ex.role);
    const seq = persistedSeq(ex);
    // CUSTOM(G-KC): id 的身份 token 不能是数组下标。
    // 两个理由，都是实测踩出来的（2026-10-26）：
    // ① 上翻前插会让下标整体位移 → 全历史 id 换一遍 → 测量缓存全部失效；
    // ② 分片缓存/持久·瞬时两半都从 0 开始建 → 同一 token 撞键 → React key 与
    //    虚拟器 getItemKey 重复 → 所有行定位到同一个 start（文字大面积压叠）。
    // 持久行用 seq（全会话唯一且不随前插改变）；瞬时行没有 seq，用位置，
    // 但必须是全数组位置（startIndex 由分片调用方透传）。
    const idToken = seq > 0 ? `s${seq}` : String(startIndex + index);
    const blockList = blocksForSeq(blocks, seq);
    // 正文优先取块流；没有块流才回退 `content`。块流是正文的唯一载体（D1），
    // 一旦某轮有块，`content` 就不再是它的真相来源（双写期两者等价，S2d 删掉 content）。
    const content =
      blockList && (role === "user" || role === "agent" || role === "assistant")
        ? blocksTextContent(blockList)
        : ex.content || "";
    if (role === "user") {
      if (isBlankText(content)) continue;
      out.push({
        id: stableTimelineID("user", idToken, content, ex.timestamp, ex.agent),
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
      if (blockList) {
        out.push(
          ...buildAssistantTimelineFromBlocks(
            { ...(ex as BlockExchangeLike), seq },
            blockList,
          ),
        );
        continue;
      }
      const auxList = seq ? exchangeAux[String(seq)] || [] : [];
      out.push(...buildAssistantTimeline({ ...ex, seq }, idToken, auxList));
      continue;
    }
    if (role === "thought") {
      if (isBlankText(content)) continue;
      out.push({
        id:
          ex.thought_id ||
          stableTimelineID("thought", idToken, content, ex.timestamp, ex.agent),
        type: "thought",
        content,
      });
      continue;
    }
    if (role === "tool") {
      if (!ex.toolCall) continue;
      const normalizedTool = normalizeToolCall(ex.toolCall);
      const kind = String(normalizedTool.kind || "").toLowerCase();
      const hasTitle = Boolean(normalizedTool.title || (normalizedTool.meta && typeof normalizedTool.meta.title === "string" && normalizedTool.meta.title));
      const hasContent = Array.isArray(normalizedTool.content) && normalizedTool.content.length > 0;
      const hasLocations = Array.isArray(normalizedTool.locations) && normalizedTool.locations.length > 0;
      const hasCommand = typeof normalizedTool.meta?.command === "string" && String(normalizedTool.meta.command).trim() !== "" ||
        typeof normalizedTool.meta?.input === "string" && String(normalizedTool.meta.input).trim() !== "";
      const isEmptyBash = (kind === "bash" || kind === "execute") && !hasContent && !hasLocations && !hasCommand && (!hasTitle || /^bash$/i.test(String(normalizedTool.title || normalizedTool.meta?.title || "").trim()));
      if (isEmptyBash) continue;
      out.push({
        id:
          normalizedTool.callId ||
          stableTimelineID(
            "tool",
            idToken,
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
          idToken,
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
        id: ex.planUpdate.id || stableTimelineID("plan", idToken, ex.planUpdate.content || "", ex.timestamp, ex.agent),
        type: "plan",
        planUpdate: ex.planUpdate,
        timestamp: ex.timestamp,
      });
      continue;
    }
    if (role === "compact") {
      if (!ex.compactNotice) continue;
      out.push({
        id: ex.compactNotice.id || stableTimelineID("compact", idToken, JSON.stringify(ex.compactNotice), ex.timestamp, ex.agent),
        type: "compact",
        compactNotice: ex.compactNotice,
        timestamp: ex.timestamp,
      });
    }
  }
  return out;
}

/**
 * CUSTOM(G-AT): 上游没有工具卡折叠 —— 它把每个 toolcall 逐个渲染成 ToolCallCard，
 * 一个回合 100+ 张时首屏 DOM 与布局成本都压在打开会话上。合上游时这一整段
 * （含 SessionViewer 的 tool_group 分支与 stream/ToolCallGroupCard.tsx）要么全留要么全弃。
 *
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

export function applySessionContextWindow(
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
