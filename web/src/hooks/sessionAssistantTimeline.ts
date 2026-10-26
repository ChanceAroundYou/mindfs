import type { ExchangeAux } from "../services/session";
import { isBlankText, normalizeToolCall, type TimelineItem } from "./sessionBlocks";
import type { ExchangeLike } from "./sessionTimeline";
import { stableTimelineID } from "./stableTimelineID";

function assistantSegmentItem(
  // CUSTOM(G-KC): 身份 token 不能是数组下标 —— 上翻前插会让下标整体位移，
  // 持久/瞬时两半又各自从 0 起建，撞键后虚拟行全挤到同一个 start。
  // 调用方传持久行的 `s<seq>`（或瞬时行在半区内的位置）。
  idToken: string,
  ex: ExchangeLike,
  content: string,
  segmentIndex: number,
  includeContextWindow: boolean,
): TimelineItem | null {
  // 零宽 / 全空白段不算正文：见 sessionBlocks 的 isBlankText（`"\n\u200b"` 这类
  // 增量会在两段正文之间渲染成一个只有零宽字符、却带段落 margin 的空段落）。
  if (isBlankText(content)) {
    return null;
  }
  return {
    // Content and timestamps change during streaming. Identity belongs to the
    // exchange/segment, so React and virtual measurements survive each chunk.
    id: `assistant:${idToken}:${segmentIndex}`,
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

export function buildAssistantTimeline(
  ex: ExchangeLike,
  idToken: string,
  auxList: ExchangeAux[],
): TimelineItem[] {
  const content = ex.content || "";
  if (!auxList.length) {
    const single = assistantSegmentItem(idToken, ex, content, 0, true);
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
        idToken,
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
            `${idToken}:${segmentIndex}`,
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
            `${idToken}:${segmentIndex}`,
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
          `${idToken}:${segmentIndex}`,
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
            `${idToken}:${segmentIndex}`,
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
            `${idToken}:${segmentIndex}`,
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
      idToken,
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
