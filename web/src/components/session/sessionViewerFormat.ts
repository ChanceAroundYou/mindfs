import type { TimelineItem } from "../../hooks/useSessionStream";
import type { AgentStatus } from "../../services/agents";
import type { TokenUsage, ToolCall } from "../../services/session";
import type { Locale } from "../../i18n/index";
import type { UploadAttachment } from "./sessionViewerTypes";

export const uploadTokenPattern = /\[(?:read file|file):\s*([^\]]+)\]/g;

export function basename(path: string): string {
  const normalized = (path || "").replace(/\\/g, "/");
  const parts = normalized.split("/");
  return parts[parts.length - 1] || path;
}

export function isImagePath(path: string): boolean {
  return /\.(png|jpe?g|gif|webp|bmp|svg)$/i.test(path);
}

export function extractUploadAttachments(content: string): UploadAttachment[] {
  const attachments: UploadAttachment[] = [];
  uploadTokenPattern.lastIndex = 0;
  let match: RegExpExecArray | null;
  while ((match = uploadTokenPattern.exec(content || "")) !== null) {
    const path = match[1].trim();
    attachments.push({
      path,
      name: basename(path),
      isImage: isImagePath(path),
    });
  }
  return attachments;
}

export function stripImageAttachmentTokens(content: string): string {
  if (!content) {
    return "";
  }
  const stripped = content.replace(
    uploadTokenPattern,
    (fullMatch, rawPath: string) => {
      const path = String(rawPath || "").trim();
      if (!isImagePath(path)) {
        return fullMatch;
      }
      return "";
    },
  );
  return stripped.replace(/\n{3,}/g, "\n\n").replace(/^[\n\s]+|[\n\s]+$/g, "");
}

export function stripUploadAttachmentTokens(content: string): string {
  if (!content) {
    return "";
  }
  return content
    .replace(uploadTokenPattern, "")
    .replace(/\n{3,}/g, "\n\n")
    .replace(/^[\n\s]+|[\n\s]+$/g, "");
}

export function formatContextWindowPercent(contextWindow?: {
  totalTokens: number;
  modelContextWindow: number;
}) {
  const usedTokens = Math.max(0, Number(contextWindow?.totalTokens || 0));
  const modelContextWindow = Math.max(
    0,
    Number(contextWindow?.modelContextWindow || 0),
  );
  if (!usedTokens || !modelContextWindow) {
    return null;
  }
  const usedRatio = Math.max(0, Math.min(1, usedTokens / modelContextWindow));
  return {
    usedTokens,
    usedRatio,
    percent: Math.round(usedRatio * 100),
  };
}

export function formatCompactTokenCount(value: number) {
  if (!Number.isFinite(value) || value <= 0) {
    return "0";
  }
  if (value >= 1_000_000) {
    return `${Math.round(value / 1_000_000)}M`;
  }
  if (value >= 1_000) {
    return `${Math.round(value / 1_000)}K`;
  }
  return String(Math.round(value));
}

export function modelDisplayName(
  agents: AgentStatus[] | undefined,
  agentName?: string,
  modelID?: string,
): string {
  const model = `${modelID || ""}`.trim();
  if (!model) {
    return "";
  }
  const agent = (agents || []).find(
    (item) => item.name === `${agentName || ""}`.trim(),
  );
  const match = (agent?.models || []).find((item) => item.id === model);
  return `${match?.name || ""}`.trim() || model;
}

/** 模型信息的最小形状：轮次页脚与会话常驻状态条共用。 */
export type ModelMetaLike = {
  agent?: string;
  model?: string;
  modelDisplayName?: string;
  effort?: string;
  fastService?: string;
};

export function formatModelMeta(
  meta: ModelMetaLike | undefined,
  agents?: AgentStatus[],
): string {
  if (!meta) {
    return "";
  }
  const parts = [
    `${meta.modelDisplayName || ""}`.trim() ||
      modelDisplayName(agents, meta.agent, meta.model),
    meta.effort,
  ]
    .map((value) => `${value || ""}`.trim())
    .filter(Boolean);
  if (`${meta.fastService || ""}`.trim().toLowerCase() === "on") {
    parts.push("fast");
  }
  return parts.join(" · ");
}

export const formatTime = (isoString: string | undefined, locale: Locale) => {
  if (!isoString) return "";
  try {
    const date = new Date(isoString);
    const now = new Date();
    const isToday = date.toDateString() === now.toDateString();
    const isThisYear = date.getFullYear() === now.getFullYear();
    const timeStr = date.toLocaleTimeString(locale, {
      hour: "2-digit",
      minute: "2-digit",
      hour12: false,
    });
    if (isToday) return timeStr;
    const month = (date.getMonth() + 1).toString().padStart(2, "0");
    const day = date.getDate().toString().padStart(2, "0");
    if (isThisYear) return `${month}-${day} ${timeStr}`;
    return `${date.getFullYear()}-${month}-${day} ${timeStr}`;
  } catch {
    return "";
  }
};

export const formatToolCallFallbackResult = (toolCall: Partial<ToolCall>): string => {
  const kind = (toolCall.kind || "").toLowerCase();
  if (kind === "read") return "";
  const rawInput = toolCall.meta?.input;
  if (typeof rawInput === "string" && rawInput.trim() !== "") {
    const todoMarkdown = formatTodoToolCallInput(rawInput);
    if (todoMarkdown) return todoMarkdown;
    return rawInput;
  }
  const rawOutput = toolCall.meta?.output;
  if (typeof rawOutput === "string" && rawOutput.trim() !== "")
    return rawOutput;
  return "";
};

export const formatTodoToolCallInput = (rawInput: string): string => {
  try {
    const parsed = JSON.parse(rawInput) as {
      todos?: Array<{
        content?: string;
        status?: string;
        activeForm?: string;
      }>;
    };
    if (!Array.isArray(parsed?.todos) || parsed.todos.length === 0) {
      return "";
    }
    const lines = parsed.todos
      .map((todo) => {
        const content = `${todo?.content || ""}`.trim();
        const activeForm = `${todo?.activeForm || ""}`.trim();
        const status = `${todo?.status || ""}`.trim().toLowerCase();
        if (status === "completed") {
          const label = content || activeForm;
          return label ? `- [x] ${label}` : "";
        }
        if (status === "in_progress") {
          const label = activeForm || content;
          return label ? `- [ ] ${label} _(in progress)_` : "";
        }
        const label = content || activeForm;
        return label ? `- [ ] ${label}` : "";
      })
      .filter(Boolean);
    return lines.join("\n");
  } catch {
    return "";
  }
};

export function isAuxiliaryTimelineItem(item: TimelineItem | null): boolean {
  return (
    item?.type === "tool" ||
    item?.type === "thought" ||
    item?.type === "todo" ||
    item?.type === "plan" ||
    item?.type === "compact"
  );
}

/**
 * 每个已完成轮次在时间线里的**唯一**收尾下标。
 *
 * - 与 `isStreaming` 无关 —— pending 轮询翻转不会让轮次边界闪；
 * - 与相邻项类型无关 —— 工具卡分组、瞬时行都不会算错边界；
 * - 按轮次身份去重 —— 同一轮后面的瞬时时/持久行不会各算一次。
 *
 * 底部控件槽只取其中最新的一项（见 findLatestCompletedTurnIndex）。
 */
export function collectCompletedTurnEndIndices(
  items: readonly TimelineItem[],
): Map<string, number> {
  const last = new Map<string, number>();
  items.forEach((item, index) => {
    const meta = item.turnMeta;
    if (meta?.completed) last.set(meta.turnKey, index);
  });
  return last;
}

/**
 * 最新一个**已完整结束**轮次的收尾下标；没有则 -1。
 *
 * 底部控件槽只显示一个轮次：运行中让位给等待提示，空闲时显示最新完成轮。
 * 仍以 `turnMeta.completed` 为唯一判据 —— 客户端流式/轮询状态不参与，收尾后不会闪。
 */
export function findLatestCompletedTurnIndex(
  items: readonly TimelineItem[],
): number {
  for (let i = items.length - 1; i >= 0; i -= 1) {
    if (items[i]?.turnMeta?.completed) return i;
  }
  return -1;
}

export function timelineItemSpacing(
  previous: TimelineItem | null,
  current: TimelineItem,
): string {
  if (!previous) {
    return "0";
  }
  if (isAuxiliaryTimelineItem(previous) && isAuxiliaryTimelineItem(current)) {
    return "6px";
  }
  return "16px";
}

export const USER_MESSAGE_SUMMARY_LENGTH = 48;

export function normalizeUserMessageSummary(content: string, emptyLabel: string): string {
  const text = stripUploadAttachmentTokens(content || "")
    .replace(/\s+/g, " ")
    .trim();
  if (!text) {
    return emptyLabel;
  }
  return text.length > USER_MESSAGE_SUMMARY_LENGTH
    ? `${text.slice(0, USER_MESSAGE_SUMMARY_LENGTH)}...`
    : text;
}
