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

export function formatTurnTokenCount(value: number) {
  const tokens = Math.max(0, Number(value || 0));
  const compact = (amount: number, suffix: string) => {
    const digits = amount >= 100 ? 0 : amount >= 10 ? 1 : 2;
    return `${Number(amount.toFixed(digits))}${suffix}`;
  };
  if (tokens >= 1_000_000) {
    return compact(tokens / 1_000_000, "m");
  }
  if (tokens >= 1_000) {
    return compact(tokens / 1_000, "k");
  }
  return String(Math.round(tokens));
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

export function formatAssistantExchangeMeta(
  item: TimelineItem,
  agents?: AgentStatus[],
): string {
  if (item.type !== "assistant_text") {
    return "";
  }
  const parts = [
    `${item.modelDisplayName || ""}`.trim() ||
      modelDisplayName(agents, item.agent, item.model),
    item.effort,
  ]
    .map((value) => `${value || ""}`.trim())
    .filter(Boolean);
  if (`${item.fastService || ""}`.trim().toLowerCase() === "on") {
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

export function shouldDefaultCollapseRelatedFiles(_isMobile?: boolean, _relatedFileCount?: number): boolean {
  return true;
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
