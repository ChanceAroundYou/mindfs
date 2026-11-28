// 会话正文块 → 时间线（CUSTOM(G-BH)，方案 §3.2 / §7）。
//
// 为什么单独成模块：这里的判定（块序、类型映射、context_window 归属）是独立行为，
// 由 Vitest 直接覆盖。上游把这段逻辑内联在 `useSessionStream.ts` 里，那只能靠源码文本断言。
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
import {
  hasMeaningfulChar,
  isBlankText,
  isPlaceholderThought,
} from "../shared/thoughtText";

/**
 * 一轮 agent 回复的身份与页脚数据（时间、正文、完成判据）。
 *
 * 为什么挂在**每个条目**上而不是最后一个 assistant_text 上：一轮回复可能以工具卡或
 * 思考卡收尾（CLI 常见：最后一步先调工具，或只产出 reasoning）。只贴 assistant_text
 * 会让这些轮次**没有控件行**。时间线渲染时只在「该轮最后一行」画一次，所以数据必须
 * 在每行都取得到，且所有条目共用**同一个对象**（分组、缓存都靠它认轮）。
 *
 * `completed` 直接取「服务端已持久化」（seq > 0）而不是客户端的 isStreaming：
 * 轮次落盘就是回合结束的权威信号；流式期的瞬时行没有 seq，永远不会出现控件。
 * 这样控件行既不依赖 5s 轮询的 pending 翻转（闪、消失），也不会在运行中出现。
 */
export type TurnMeta = {
  /** 轮次身份：持久行 `s<seq>`，瞬时行 `t<idToken>`。分组/收尾判据都按它。 */
  turnKey: string;
  /** 服务端已持久化 = 这一轮已完整结束。 */
  completed: boolean;
  timestamp?: string;
  agent?: string;
  seq?: number;
  /** 该轮的正文（复制按钮用）。 */
  content: string;
};

export type TimelineItem =
  | {
      id: string;
      type: "user_text" | "assistant_text";
      content: string;
      timestamp?: string;
      agent?: string;
      pendingAck?: boolean;
      seq?: number;
      turnMeta?: TurnMeta;
    }
  | { id: string; type: "thought"; content: string; turnMeta?: TurnMeta }
  | { id: string; type: "tool"; toolCall: ToolCall; turnMeta?: TurnMeta }
  | { id: string; type: "tool_group"; kind: string; toolCalls: ToolCall[]; turnMeta?: TurnMeta }
  | { id: string; type: "todo"; todoUpdate: TodoUpdate; timestamp?: string; turnMeta?: TurnMeta }
  | { id: string; type: "plan"; planUpdate: PlanUpdate; timestamp?: string; turnMeta?: TurnMeta }
  | { id: string; type: "compact"; compactNotice: CompactNotice; timestamp?: string; turnMeta?: TurnMeta };

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

// 思考/正文的可见性判据住在 shared/thoughtText（三条思考路径与思考框共用）。
// 这里重新导出，保持既有 import 与测试不变。
export { hasMeaningfulChar, isBlankText, isPlaceholderThought };

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
    .filter((text) => !isBlankText(text))
    .join("\n");
}

/**
 * 把 exchange 的身份与页脚字段收成一份数据。
 *
 * 持久行（seq > 0）用 `s<seq>` 认轮 —— seq 全会话唯一，跨重渲染稳定；瞬时行用调用方
 * 传入的全数组位置 token，避免两轮流式内容撞成同一把 key。各条目共用同一个对象引用：
 * 一轮只有一份控件行，分组与渲染都靠这个身份。
 */
export function turnMetaFromExchange(
  ex: BlockExchangeLike,
  content: string,
  idToken?: string,
): TurnMeta {
  const seq = Number(ex.seq || 0);
  return {
    turnKey: seq > 0 ? `s${seq}` : `t${idToken || ex.timestamp || "transient"}`,
    completed: seq > 0,
    timestamp: ex.timestamp,
    agent: ex.agent,
    seq: ex.seq,
    content,
  };
}

/**
 * 给一轮的所有条目挂上轮次身份。
 *
 * 整轮没有任何可渲染条目时返回空数组：控件行没有锚点，模型/上下文由常驻状态条
 * 承载，不再为了「这一轮发生过」造一条只有页脚的假行（那正是同步后底部刷出一堆
 * 控件、以及 turn_meta 边界把上一轮页脚吞掉的来源）。
 */
export function attachTurnFooter(
  items: TimelineItem[],
  ex: BlockExchangeLike,
  content: string,
  idToken?: string,
): TimelineItem[] {
  if (items.length === 0) return items;
  const meta = turnMetaFromExchange(ex, content, idToken);
  return items.map((item) => ({ ...item, turnMeta: meta }));
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
  idToken?: string,
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
        // 空白/零宽或纯标点（如 "..."、"。。"）是无内容占位，不值得渲染成
        // 「思考过程 (N 字符)」；判据见 shared/thoughtText，三条思考路径共用一把尺。
        const content = block.text || "";
        if (isPlaceholderThought(content)) break;
        out.push({ id, type: "thought", content });
        break;
      }
      case "text": {
        const content = block.text || "";
        // 零宽字符不算正文：见 isBlankText。只判空串会让 `"\n\u200b"` 变成一个
        // 只有零宽字符的 markdown 段落，工具卡间距被段落 margin 撑开。
        if (isBlankText(content)) break;
        out.push({
          id,
          type: "assistant_text",
          content,
          timestamp: ex.timestamp,
          agent: ex.agent,
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
  // 每轮真值的 context_window / token_usage 由 turnMeta 承载，渲染时贴到该轮最后一行；
  // 没有可渲染条目（例如纯标点 reasoning 被过滤）时仍产出一行纯页脚，模型与用量不丢。
  return attachTurnFooter(out, ex, blocksTextContent(blocks), idToken);
}
