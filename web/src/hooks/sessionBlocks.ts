// 会话正文块 → 时间线（CUSTOM(G-BH)，方案 §3.2 / §7）。
//
// 为什么单独成模块：这里的判定（块序、类型映射、context_window 归属）是**行为**，
// 需要能被 `node --test` 直接 import 跑（见 `tests/ts-module-hook.mjs` 的说明：
// `.tsx` 导入不了，判定逻辑必须住在 `.ts` 里）。上游把这段逻辑内联在
// `useSessionStream.ts` 里，那只能靠源码文本断言。
//
// 为什么需要块流这条路：旧渲染器把「一轮正文」当成 `content` 字符串 + aux 按 `line`
// 插入，于是**只产出 reasoning、没有 text 的轮次渲染成空**（实测 os 模型约 15% 的轮次）。
// 流式时 thought_chunk 实时推所以看得见，收尾换成持久化数据后正文瞬间消失 ——
// 用户看到的就是「回复完内容就没了」。块流里 reasoning 是一等公民，结构上不可能丢。
import type {
  CompactNotice,
  PlanUpdate,
  SessionBlock,
  TodoUpdate,
  ToolCall,
  TokenUsage,
} from "../services/session";

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
  | { id: string; type: "tool_group"; kind: string; toolCalls: ToolCall[] }
  | { id: string; type: "todo"; todoUpdate: TodoUpdate; timestamp?: string }
  | { id: string; type: "plan"; planUpdate: PlanUpdate; timestamp?: string }
  | { id: string; type: "compact"; compactNotice: CompactNotice; timestamp?: string };

/**
 * 渲染块所需的那部分 exchange 字段。刻意不直接引 `Session` / `SessionItem`：
 * 调用方传进来的是「本地拼装过的 exchange」，字段名与序列化形状一致即可。
 */
export type BlockExchangeLike = {
  seq?: number;
  agent?: string;
  model?: string;
  model_display_name?: string;
  effort?: string;
  fast_service?: string;
  timestamp?: string;
  context_window?: {
    totalTokens: number;
    modelContextWindow: number;
  };
  token_usage?: TokenUsage;
};

/** 正文块映射（`blocks[seq]`）。缺失 = 该轮还没有块流 → 回退旧格式。 */
export type SessionBlockMapLike = Record<string, SessionBlock[]>;

export function normalizeToolCallStatus(status?: string): string {
  const value = (status || "").toLowerCase();
  if (value === "completed") return "complete";
  if (value === "pending") return "running";
  return value || "running";
}

export function normalizeToolCall(input: ToolCall): ToolCall {
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

/**
 * 块的时间线 id：`block:<seq>:<pos>`。
 *
 * 直接用服务端的权威身份，不再哈希内容 —— `(seq,pos)` 在一个会话里唯一，且**跨 revision
 * 稳定**：同一块内容被补正（revision+1）时 id 不变，React 与虚拟列表的测量都不会重建。
 * 旧写法的 id 含内容哈希，流式期每来一个 chunk 就换一次。
 */
export function blockItemID(seq: number | undefined, pos: number): string {
  return `block:${seq && seq > 0 ? seq : 0}:${pos}`;
}

/**
 * 该 seq 的正文块；没有就返回 undefined（历史会话 / 本方案落地前落盘的轮次）。
 * 判据是「**非空**」而不是「键存在」：后端对没有块流的会话下发空 map，
 * 空数组当作没有，才能走到旧格式回退。
 */
export function blocksForSeq(
  blocks: SessionBlockMapLike,
  seq: number,
): SessionBlock[] | undefined {
  if (!seq) return undefined;
  const list = blocks[String(seq)];
  return list && list.length ? list : undefined;
}

/**
 * 从块流里取该轮的纯文本正文（用于 user 行、以及任何只需要文本的场合）。
 * 按 `pos` 顺序把 text 块拼起来；reasoning **不算**正文。
 */
export function blocksTextContent(blocks: SessionBlock[]): string {
  return blocks
    .slice()
    .sort((left, right) => Number(left.pos || 0) - Number(right.pos || 0))
    .filter((block) => block.type === "text")
    .map((block) => block.text || "")
    .join("\n");
}

/**
 * 按 `type` 渲染块流。
 *
 * 顺序：**只按 `pos` 遍历**。后端 `BuildTurnBlocks` 与旧渲染器逐条同序（aux 按
 * (line,到达序) 稳定排序后插进正文段之间、空正文段跳过），所以这里不需要
 * `lines` / `totalLines` / `emittedLines` / clamp —— 那些全部消失。
 */
export function buildAssistantTimelineFromBlocks(
  ex: BlockExchangeLike,
  blocks: SessionBlock[],
): TimelineItem[] {
  const out: TimelineItem[] = [];
  const ordered = blocks
    .slice()
    .sort((left, right) => Number(left.pos || 0) - Number(right.pos || 0));
  for (const block of ordered) {
    const pos = Number(block.pos || 0);
    const id = blockItemID(ex.seq, pos);
    switch (block.type) {
      case "reasoning": {
        // 灰显 / 可折叠 —— 复用既有 thought 渲染，超大块由它自己折叠。
        const content = block.text || "";
        if (!content) break;
        out.push({ id, type: "thought", content });
        break;
      }
      case "text": {
        const content = block.text || "";
        if (!content) break;
        out.push({
          id,
          type: "assistant_text",
          content,
          timestamp: ex.timestamp,
          agent: ex.agent,
          model: ex.model,
          modelDisplayName: ex.model_display_name,
          effort: ex.effort,
          fastService: ex.fast_service,
          seq: ex.seq,
        });
        break;
      }
      case "tool_call": {
        if (!block.toolcall) break;
        out.push({ id, type: "tool", toolCall: normalizeToolCall(block.toolcall) });
        break;
      }
      case "todo": {
        if (!block.todo) break;
        out.push({ id, type: "todo", todoUpdate: block.todo, timestamp: ex.timestamp });
        break;
      }
      case "plan": {
        if (!block.plan) break;
        out.push({ id, type: "plan", planUpdate: block.plan, timestamp: ex.timestamp });
        break;
      }
      case "compact": {
        if (!block.compact) break;
        out.push({ id, type: "compact", compactNotice: block.compact, timestamp: ex.timestamp });
        break;
      }
      default:
        // 未知类型跳过：agent 加新类型时旧前端不能炸（与后端 CompactBlockLight 同约定）。
        break;
    }
  }
  // 每轮真值的 context_window / token_usage 贴到**最后一个** assistant_text 上。
  // 与旧渲染器同一取舍（`message-mechanism-merges` 的 ⑮d 钉住了这个字面量）。
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
  return out;
}
