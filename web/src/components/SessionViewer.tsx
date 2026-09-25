import React, { memo, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useSessionStream, type TimelineItem } from "../hooks/useSessionStream";
import type { TodoUpdate } from "../services/session";
import { ThinkingBlock } from "./stream/ThinkingBlock";
import { ToolCallCard, renderToolIcon } from "./stream/ToolCallCard";
import { AgentIcon } from "./AgentIcon";
import { InlineTokenText } from "./InlineTokenText";
import { MarkdownViewer } from "./MarkdownViewer";
import { fetchProofProtectedBlob } from "../services/file";
import { getRootNodeId } from "../services/rootNode";
import {
  SESSION_WINDOW_SIZE,
  clearWindowedView,
  getSessionWindow,
  type TokenUsage,
  setWindowedView,
  type ExchangeAux,
  type RelatedFile,
  type SessionWindowMeta,
  type ToolCall,
} from "../services/session";
import { savePrompt } from "../services/prompts";
import { reportError } from "../services/error";
import { rootBadgeButtonStyle } from "./rootBadgeStyle";
import { copyText } from "../services/clipboard";
import type { AgentStatus } from "../services/agents";
import { useI18n, type Locale } from "../i18n";
import { formatSessionDuration } from "../services/sessionDuration";
import {
  relatedFileStatKey,
  useRelatedFileStats,
} from "../hooks/useRelatedFileStats";

type SessionItem = {
  key?: string;
  session_key?: string;
  type?: string;
  name?: string;
  agent?: string;
  context_window?: {
    totalTokens: number;
    modelContextWindow: number;
  };
  search_seq?: number;
  scope?: string;
  purpose?: string;
  exchanges?: Array<{
    seq?: number;
    role?: string;
    agent?: string;
    model?: string;
    model_display_name?: string;
    effort?: string;
    fast_service?: string;
    content?: string;
    timestamp?: string;
    context_window?: {
      totalTokens: number;
      modelContextWindow: number;
    };
    token_usage?: TokenUsage;
  }>;
  closed_at?: string;
  source?: string;
  related_files?: RelatedFile[];
  exchange_aux?: Record<string, ExchangeAux[]>;
};

type ExchangeArray = NonNullable<SessionItem["exchanges"]>;

type SessionViewerProps = {
  session: SessionItem | null;
  loading?: boolean;
  slashCommandResult?: {
    sessionKey?: string;
    command: string;
    content: string;
    status: "running" | "complete" | "failed";
    error?: string;
    loginNotice?: {
      status?: string;
      loginId?: string;
      verificationUrl?: string;
      userCode?: string;
      error?: string;
      authMode?: string;
      planType?: string;
    };
  } | null;
  rootId?: string | null;
  rootDisplayName?: string | null;
  rootPath?: string | null;
  rootColor?: string | null;
  interactionMode?: "main" | "drawer";
  targetSeq?: number;
  gitFileStatsByPath?: Record<
    string,
    { status: string; additions: number; deletions: number }
  >;
  onFileClick?: (file: RelatedFile & { name?: string }) => void;
  onRootClick?: (rootId: string) => void;
  onRemoveRelatedFile?: (path: string, head?: string, repoPath?: string, repoKind?: string) => void;
  onAskUserAnswer?: (input: {
    rootId: string;
    sessionKey: string;
    agent?: string;
    toolUseId: string;
    answers: Record<string, string>;
  }) => void | Promise<void>;
  onEditUserMessage?: (content: string) => void;
  onForkAgentMessage?: (seq: number) => void | Promise<void>;
  targetSeqRequestKey?: string | number;
  agents?: AgentStatus[];
  composerOverlayInset?: number;
  scrollContainerRef?: React.RefObject<HTMLDivElement | null>;
};

type AskUserQuestionOption = {
  label?: string;
  description?: string;
};

type AskUserQuestionItem = {
  question?: string;
  header?: string;
  options?: AskUserQuestionOption[];
  multiSelect?: boolean;
};

type UploadAttachment = {
  path: string;
  name: string;
  isImage: boolean;
};

function AttachmentImage({
  rootId,
  path,
  name,
}: {
  rootId: string;
  path: string;
  name: string;
}) {
  const [url, setURL] = useState("");

  useEffect(() => {
    let cancelled = false;
    let objectURL = "";
    async function run() {
      try {
        const blob = await fetchProofProtectedBlob({ rootId, path, nodeId: String(getRootNodeId(rootId) || "").trim() || undefined });
        if (cancelled) return;
        objectURL = URL.createObjectURL(blob);
        setURL(objectURL);
      } catch {
        if (!cancelled) {
          setURL("");
        }
      }
    }
    if (rootId && path) {
      void run();
    }
    return () => {
      cancelled = true;
      if (objectURL) {
        URL.revokeObjectURL(objectURL);
      }
    };
  }, [rootId, path]);

  return (
    <img
      src={url}
      alt={name}
      style={{
        display: "block",
        width: "100%",
        maxHeight: "220px",
        objectFit: "cover",
        background: "rgba(15,23,42,0.06)",
      }}
    />
  );
}

const uploadTokenPattern = /\[(?:read file|file):\s*([^\]]+)\]/g;

function basename(path: string): string {
  const normalized = (path || "").replace(/\\/g, "/");
  const parts = normalized.split("/");
  return parts[parts.length - 1] || path;
}

function isImagePath(path: string): boolean {
  return /\.(png|jpe?g|gif|webp|bmp|svg)$/i.test(path);
}

function extractUploadAttachments(content: string): UploadAttachment[] {
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

function stripImageAttachmentTokens(content: string): string {
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

function stripUploadAttachmentTokens(content: string): string {
  if (!content) {
    return "";
  }
  return content
    .replace(uploadTokenPattern, "")
    .replace(/\n{3,}/g, "\n\n")
    .replace(/^[\n\s]+|[\n\s]+$/g, "");
}

function formatContextWindowPercent(contextWindow?: {
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

function formatCompactTokenCount(value: number) {
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

function formatTurnTokenCount(value: number) {
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

function TurnTokenUsage({ usage }: { usage?: TokenUsage }) {
  if (!usage) {
    return null;
  }
  const inputTokens = Math.max(0, Number(usage.inputTokens || 0));
  const outputTokens = Math.max(0, Number(usage.outputTokens || 0));
  if (!inputTokens && !outputTokens) {
    return null;
  }
  const cacheReadReported = Number.isFinite(usage.cacheReadTokens);
  const cacheReadTokens = Math.max(0, Number(usage.cacheReadTokens || 0));
  const hitPercent = inputTokens > 0 && cacheReadReported
    ? Math.round(Math.min(1, cacheReadTokens / inputTokens) * 100)
    : null;
  const cacheLabel = hitPercent === null ? "—" : `${hitPercent}%`;
  return (
    <span
      title={`${inputTokens}(♻ ${cacheLabel})→${outputTokens}`}
      style={{
        display: "inline-flex",
        alignItems: "baseline",
        flexWrap: "wrap",
        minWidth: 0,
        fontVariantNumeric: "tabular-nums",
      }}
    >
      <span>{formatTurnTokenCount(inputTokens)}</span>
      <span>{`(♻ ${cacheLabel})`}</span>
      <span>→</span>
      <span>{formatTurnTokenCount(outputTokens)}</span>
    </span>
  );
}

function modelDisplayName(
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

function formatAssistantExchangeMeta(
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

function ContextWindowBadge({
  contextWindow,
}: {
  contextWindow?: { totalTokens: number; modelContextWindow: number };
}) {
  const metrics = formatContextWindowPercent(contextWindow);
  if (!metrics) {
    return null;
  }
  const hue =
    metrics.percent >= 90
      ? "#dc2626"
      : metrics.percent >= 75
        ? "#ea580c"
        : "#0f766e";
  return (
    <span
      title={`Context Window ${metrics.percent}% used (${metrics.usedTokens}/${contextWindow?.modelContextWindow} used)`}
      style={{
        display: "inline-flex",
        alignItems: "center",
        justifyContent: "center",
        flexWrap: "wrap",
        minWidth: 0,
        color: hue,
        lineHeight: 1.1,
        fontSize: "10px",
        fontWeight: 700,
        letterSpacing: "0.01em",
        fontVariantNumeric: "tabular-nums",
        overflowWrap: "anywhere",
      }}
    >
      <span>{metrics.percent}%</span>
      <span>&middot;used</span>
      <span>
        {`(${formatCompactTokenCount(metrics.usedTokens)}/${formatCompactTokenCount(
          contextWindow?.modelContextWindow || 0,
        )})`}
      </span>
    </span>
  );
}

function previousUserTimestamp(timeline: TimelineItem[], index: number): string {
  for (let i = index - 1; i >= 0; i -= 1) {
    const item = timeline[i];
    if (item.type === "user_text") {
      return item.timestamp || "";
    }
  }
  return "";
}

const formatTime = (isoString: string | undefined, locale: Locale) => {
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

const formatToolCallFallbackResult = (toolCall: Partial<ToolCall>): string => {
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

const formatTodoToolCallInput = (rawInput: string): string => {
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

function isAuxiliaryTimelineItem(item: TimelineItem | null): boolean {
  return (
    item?.type === "tool" ||
    item?.type === "thought" ||
    item?.type === "todo" ||
    item?.type === "plan" ||
    item?.type === "compact"
  );
}

// overlay 对账用的归一化：只去空白。服务端落盘时会在相邻文本块之间补 "\n\n"
// （usecase.appendResponseChunk），而流式 message_chunk 不带，所以同一轮的
// 「缓存瞬时拷贝」与「落盘正文」只差空白 —— 逐字比较认不出来，去空白才认得出。
function normalizeOverlayText(value: string): string {
  return value.replace(/\s+/g, "");
}

// 只在窗口最新这几条持久化行里找陈旧拷贝：它必然是「刚落盘那一轮」的副本。
// 跟更早的历史比会误伤（同一句工程套话在不同轮次重复出现是正常的）。
const OVERLAY_TAIL_ROWS = 3;
// ponytail: 短文本不参与让位判定。长度地板挡掉「好的」「继续」这类合法重复；
// 代价是落盘后残留的短片段（<32 字）仍会多显示一次，等窗口重锚定自愈。
const OVERLAY_DUP_MIN_CHARS = 32;

function PlanUpdateCard({ content, rootId }: { content: string; rootId?: string | null }) {
  return (
    <div
      style={{
        width: "100%",
        minWidth: 0,
        borderRadius: "10px",
        border: "1px solid color-mix(in srgb, var(--accent-color) 24%, transparent)",
        background: "linear-gradient(180deg, color-mix(in srgb, var(--accent-color) 8%, transparent), color-mix(in srgb, var(--accent-color) 3%, transparent))",
        overflow: "hidden",
      }}
    >
      <div
        style={{
          padding: "6px 8px",
          fontSize: "12px",
          fontWeight: 600,
          color: "var(--text-primary)",
          borderBottom: "1px solid var(--border-color)",
        }}
      >
        Plan
      </div>
      <div style={{ padding: "10px" }}>
        <MarkdownViewer content={content || ""} root={rootId || undefined} />
      </div>
    </div>
  );
}

function CompactNoticeCard({
  status,
  summary,
}: {
  status?: string;
  summary?: string;
}) {
  const normalizedStatus = `${status || ""}`.toLowerCase();
  const label =
    normalizedStatus === "running"
      ? "Compacting context"
      : normalizedStatus === "error"
        ? "Context compaction failed"
        : "Context compacted";
  return (
    <div
      style={{
        width: "100%",
        minWidth: 0,
        borderRadius: "10px",
        border: "1px solid rgba(148, 163, 184, 0.28)",
        background: "rgba(148, 163, 184, 0.08)",
        padding: "10px",
        color: "var(--text-secondary)",
        fontSize: "13px",
      }}
    >
      <div style={{ fontWeight: 600, color: "var(--text-primary)" }}>{label}</div>
      {summary ? <div style={{ marginTop: "6px" }}>{summary}</div> : null}
    </div>
  );
}

function TodoUpdateCard({ todoUpdate }: { todoUpdate: TodoUpdate }) {
  const items = Array.isArray(todoUpdate?.items) ? todoUpdate.items : [];
  return (
    <div
      style={{
        width: "100%",
        minWidth: 0,
        borderRadius: "10px",
        border: "1px solid rgba(16, 185, 129, 0.22)",
        background:
          "linear-gradient(180deg, rgba(16, 185, 129, 0.08), rgba(16, 185, 129, 0.03))",
        overflow: "hidden",
      }}
    >
      <div
        style={{
          padding: "6px 8px",
          fontSize: "12px",
          fontWeight: 600,
          color: "var(--text-primary)",
          borderBottom: "1px solid var(--border-color)",
        }}
      >
        ✅ todos
      </div>
      <div
        style={{
          padding: "10px",
          display: "flex",
          flexDirection: "column",
          gap: "6px",
        }}
      >
        {items.map((item, index) => {
          const status = `${item?.status || ""}`.trim().toLowerCase();
          const content = `${item?.content || ""}`.trim();
          const activeForm = `${item?.activeForm || ""}`.trim();
          const label =
            status === "in_progress" && activeForm
              ? activeForm
              : content || activeForm;
          const checked = status === "completed";
          return (
            <div
              key={`${index}-${label}`}
              style={{
                fontSize: "13px",
                color: "var(--text-primary)",
                opacity: checked ? 0.78 : 1,
                textDecoration: checked ? "line-through" : "none",
              }}
            >
              {checked ? "☑" : "☐"} {label}
              {status === "in_progress" ? " (in progress)" : ""}
            </div>
          );
        })}
      </div>
    </div>
  );
}

function getAskUserQuestions(toolCall: Partial<ToolCall>): AskUserQuestionItem[] {
  const raw = toolCall.meta?.questions;
  if (Array.isArray(raw)) {
    return raw as AskUserQuestionItem[];
  }
  const input = toolCall.meta?.input;
  if (typeof input !== "string" || input.trim() === "") {
    return [];
  }
  try {
    const parsed = JSON.parse(input) as { questions?: AskUserQuestionItem[] };
    return Array.isArray(parsed.questions) ? parsed.questions : [];
  } catch {
    return [];
  }
}

function getAskUserAnswers(toolCall: Partial<ToolCall>): Record<string, string> {
  const raw = toolCall.meta?.answers;
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) {
    return {};
  }
  const answers: Record<string, string> = {};
  for (const [key, value] of Object.entries(raw as Record<string, unknown>)) {
    const cleanKey = `${key || ""}`.trim();
    const cleanValue = `${value || ""}`.trim();
    if (cleanKey && cleanValue) {
      answers[cleanKey] = cleanValue;
    }
  }
  return answers;
}

function askUserQuestionTitle(question?: AskUserQuestionItem): string {
  const questionText = `${question?.question || ""}`.trim();
  const header = `${question?.header || ""}`.trim();
  if (header && questionText) {
    return `${header}：${questionText}`;
  }
  return questionText || header;
}

function AskUserIcon() {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      width="16"
      height="16"
      viewBox="0 0 16 16"
      aria-hidden="true"
      style={{ color: "#ef4444", flexShrink: 0 }}
    >
      <g fill="currentColor">
        <path d="M8 11a.75.75 0 1 1 0 1.5a.75.75 0 0 1 0-1.5m0-7c1.262 0 2.25.988 2.25 2.25c0 1.083-.566 1.648-1.021 2.104c-.408.407-.729.728-.729 1.396a.5.5 0 0 1-1 0c0-1.083.566-1.648 1.021-2.104c.408-.407.729-.728.729-1.396C9.25 5.538 8.712 5 8 5s-1.25.538-1.25 1.25a.5.5 0 0 1-1 0C5.75 4.988 6.738 4 8 4" />
        <path
          fillRule="evenodd"
          d="M8 1a7 7 0 0 1 6.999 7.001a7 7 0 0 1-10.504 6.06l-2.728.91a.582.582 0 0 1-.744-.714l.83-2.906A7 7 0 0 1 8 1m.001 1.001c-3.308 0-6 2.692-6 6c0 1.003.252 1.996.73 2.871l.196.36l-.726 2.54l1.978-.659l.428-.143l.39.226A6 6 0 0 0 8 14l.001.001c3.308 0 6-2.692 6-6s-2.692-6-6-6"
          clipRule="evenodd"
        />
      </g>
    </svg>
  );
}

function ForkIcon() {
  return (
    <svg
      width="14"
      height="14"
      viewBox="0 0 24 24"
      xmlns="http://www.w3.org/2000/svg"
      aria-hidden="true"
      style={{ transform: "rotate(180deg) scaleX(-1)" }}
    >
      <path d="M0 0h24v24H0z" fill="none" />
      <path
        fill="none"
        stroke="currentColor"
        strokeLinecap="round"
        strokeLinejoin="round"
        strokeWidth="1.5"
        d="M17 7a2 2 0 1 0 0-4a2 2 0 0 0 0 4M7 7a2 2 0 1 0 0-4a2 2 0 0 0 0 4m0 14a2 2 0 1 0 0-4a2 2 0 0 0 0 4M7 7v10M17 7v1c0 2.5-2 3-2 3l-6 2s-2 .5-2 3v1"
      />
      <circle cx="17" cy="5" r="2" fill="currentColor" />
    </svg>
  );
}

function AskUserQuestionCard({
  toolCall,
  rootId,
  sessionKey,
  agent,
  active,
  onAnswer,
}: {
  toolCall: ToolCall;
  rootId?: string | null;
  sessionKey?: string | null;
  agent?: string;
  active: boolean;
  onAnswer?: SessionViewerProps["onAskUserAnswer"];
}) {
  const { t } = useI18n();
  const questions = getAskUserQuestions(toolCall);
  const [focusedCustomAnswerKey, setFocusedCustomAnswerKey] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const status = `${toolCall.status || ""}`.toLowerCase();
  const isCurrent =
    status === "running" || status === "pending" || status === "in_progress";
  const [expanded, setExpanded] = useState(isCurrent);
  const toolUseId =
    toolCall.callId ||
    (typeof toolCall.meta?.toolUseId === "string" ? toolCall.meta.toolUseId : "");
  const persistedAnswers = getAskUserAnswers(toolCall);
  const persistedAnswersKey = JSON.stringify(persistedAnswers);
  const hasPersistedAnswers = Object.keys(persistedAnswers).length > 0;
  const [answers, setAnswers] = useState<Record<string, string>>(persistedAnswers);
  const [submitted, setSubmitted] = useState(hasPersistedAnswers);
  const firstQuestionTitle = askUserQuestionTitle(questions[0]);
  const title =
    firstQuestionTitle ||
    `${toolCall.title || ""}`.trim() ||
    (typeof toolCall.meta?.title === "string" ? toolCall.meta.title : "") ||
    "ask user";
  const canSubmit =
    !!rootId &&
    !!sessionKey &&
    !!toolUseId &&
    !!onAnswer &&
    questions.length > 0 &&
    questions.every((_, index) => (answers[`q_${index}`] || "").trim() !== "") &&
    !submitting &&
    !submitted;

  useEffect(() => {
    if (hasPersistedAnswers) {
      setAnswers(persistedAnswers);
      setSubmitted(true);
      setExpanded(false);
      return;
    }
    setAnswers({});
    setSubmitted(false);
  }, [toolUseId, persistedAnswersKey, hasPersistedAnswers]);

  useEffect(() => {
    if (submitted) {
      setExpanded(false);
      return;
    }
    setExpanded(active && isCurrent);
  }, [active, isCurrent, submitted, toolUseId]);

  const setAnswer = (index: number, value: string) => {
    setAnswers((prev) => ({ ...prev, [`q_${index}`]: value }));
  };
  const toggleMultiAnswer = (index: number, label: string, validLabels: string[]) => {
    const key = `q_${index}`;
    setAnswers((prev) => {
      const current = (prev[key] || "")
        .split(",")
        .map((item) => item.trim())
        .filter((item) => item && validLabels.includes(item));
      const next = current.includes(label)
        ? current.filter((item) => item !== label)
        : [...current, label];
      return { ...prev, [key]: next.join(", ") };
    });
  };

  return (
    <div
      style={{
        width: "100%",
        minWidth: 0,
        borderRadius: "10px",
        border: "1px solid rgba(239, 68, 68, 0.28)",
        background:
          "linear-gradient(180deg, rgba(239, 68, 68, 0.08), rgba(239, 68, 68, 0.03))",
        overflow: "hidden",
      }}
    >
      <button
        type="button"
        onClick={() => setExpanded((value) => !value)}
        style={{
          width: "100%",
          display: "flex",
          alignItems: "center",
          justifyContent: "flex-start",
          padding: "6px 8px",
          background: "rgba(239, 68, 68, 0.04)",
          border: "none",
          borderBottom: expanded ? "1px solid var(--border-color)" : "none",
          cursor: "pointer",
          fontSize: "12px",
          gap: "6px",
          minWidth: 0,
        }}
        title={title}
      >
        <AskUserIcon />
        <span
          style={{
            minWidth: 0,
            flex: 1,
            fontWeight: 500,
            color: "var(--text-primary)",
            whiteSpace: expanded ? "normal" : "nowrap",
            overflow: expanded ? "visible" : "hidden",
            textOverflow: expanded ? "clip" : "ellipsis",
            textAlign: "left",
            overflowWrap: "anywhere",
            lineHeight: 1.35,
          }}
        >
          {title}
        </span>
        <span
          style={{
            flexShrink: 0,
            transform: expanded ? "rotate(90deg)" : "rotate(0deg)",
            transition: "transform 0.2s",
            color: "var(--text-secondary)",
            display: "inline-flex",
            alignItems: "center",
          }}
        >
          <svg
            width="12"
            height="12"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2.25"
            strokeLinecap="round"
            strokeLinejoin="round"
          >
            <polyline points="9 18 15 12 9 6" />
          </svg>
        </span>
      </button>
      {expanded ? (
        <div style={{ padding: "10px", display: "flex", flexDirection: "column", gap: "12px" }}>
          {questions.map((question, index) => {
            const key = `q_${index}`;
            const options = Array.isArray(question.options) ? question.options : [];
            const selected = answers[key] || "";
            const optionLabels = options
              .map((option) => `${option.label || ""}`.trim())
              .filter(Boolean);
            const selectedSet = new Set(
              selected
                .split(",")
                .map((item) => item.trim())
                .filter(Boolean),
            );
            const hasOptionSelection = question.multiSelect
              ? optionLabels.some((label) => selectedSet.has(label))
              : optionLabels.includes(selected);
            const customAnswer = hasOptionSelection ? "" : selected;
            const customAnswerActive =
              customAnswer.trim() !== "" || focusedCustomAnswerKey === key;
            const itemTitle = askUserQuestionTitle(question);
            return (
              <div key={key} style={{ display: "flex", flexDirection: "column", gap: "8px" }}>
                {itemTitle ? (
                  <div
                    style={{
                      fontSize: "13px",
                      fontWeight: 700,
                      color: "var(--text-primary)",
                      lineHeight: 1.45,
                      overflowWrap: "anywhere",
                    }}
                  >
                    {itemTitle}
                  </div>
                ) : null}
                {options.length > 0 ? (
                  <div style={{ display: "flex", flexDirection: "column", gap: "6px" }}>
                    {options.map((option) => {
                      const label = `${option.label || ""}`.trim();
                      if (!label) return null;
                      const checked = question.multiSelect
                        ? selectedSet.has(label)
                        : selected === label;
                      return (
                        <label
                          key={label}
                          style={{
                            display: "flex",
                            gap: "8px",
                            alignItems: "flex-start",
                            padding: "7px 8px",
                            borderRadius: "8px",
                            border: checked
                              ? "1px solid rgba(239, 68, 68, 0.42)"
                              : "1px solid var(--border-color)",
                            background: checked ? "rgba(239, 68, 68, 0.10)" : "var(--content-bg)",
                            cursor: submitted ? "default" : "pointer",
                          }}
                        >
                          <input
                            type={question.multiSelect ? "checkbox" : "radio"}
                            name={`${toolUseId}-${key}`}
                            checked={checked}
                            disabled={submitted}
                            onChange={() =>
                              question.multiSelect
                                ? toggleMultiAnswer(index, label, optionLabels)
                                : setAnswer(index, label)
                            }
                            style={{ marginTop: "2px" }}
                          />
                          <span style={{ display: "flex", flexDirection: "column", gap: "2px" }}>
                            <span style={{ fontSize: "13px", color: "var(--text-primary)" }}>{label}</span>
                            {option.description ? (
                              <span style={{ fontSize: "12px", color: "var(--text-secondary)" }}>
                                {option.description}
                              </span>
                            ) : null}
                          </span>
                        </label>
                      );
                    })}
                    <textarea
                      value={customAnswer}
                      disabled={submitted}
                      onFocus={() => setFocusedCustomAnswerKey(key)}
                      onBlur={() => setFocusedCustomAnswerKey((current) => (current === key ? "" : current))}
                      onChange={(event) => setAnswer(index, event.target.value)}
                      placeholder={t("session.askCustomAnswer")}
                      rows={2}
                      style={{
                        width: "100%",
                        resize: "vertical",
                        borderRadius: "8px",
                        border: customAnswerActive
                          ? "1px solid rgba(239, 68, 68, 0.42)"
                          : "1px solid var(--border-color)",
                        outline: "none",
                        background: "var(--content-bg)",
                        color: "var(--text-primary)",
                        padding: "8px",
                        fontSize: "13px",
                        boxSizing: "border-box",
                      }}
                    />
                  </div>
                ) : (
                  <textarea
                    value={selected}
                    disabled={submitted}
                    onChange={(event) => setAnswer(index, event.target.value)}
                    placeholder={t("session.askAnswer")}
                    rows={3}
                    style={{
                      width: "100%",
                      resize: "vertical",
                      borderRadius: "8px",
                      border: "1px solid var(--border-color)",
                      background: "var(--content-bg)",
                      color: "var(--text-primary)",
                      padding: "8px",
                      fontSize: "13px",
                      boxSizing: "border-box",
                    }}
                  />
              )}
            </div>
          );
        })}
        <button
          type="button"
          disabled={!canSubmit}
          onClick={async () => {
            if (!canSubmit || !rootId || !sessionKey || !toolUseId || !onAnswer) return;
            setSubmitting(true);
            try {
              await onAnswer({ rootId, sessionKey, agent, toolUseId, answers });
              setSubmitted(true);
              setExpanded(false);
            } finally {
              setSubmitting(false);
            }
          }}
          style={{
            alignSelf: "flex-start",
            border: "none",
            borderRadius: "999px",
            padding: "6px 12px",
            background: canSubmit ? "#ef4444" : "var(--border-color)",
            color: canSubmit ? "#fff" : "var(--text-secondary)",
            fontSize: "12px",
            fontWeight: 700,
            cursor: canSubmit ? "pointer" : "default",
          }}
        >
          {submitted ? t("session.askSubmitted") : submitting ? t("session.askSubmitting") : t("session.askSubmit")}
        </button>
        </div>
      ) : null}
    </div>
  );
}

function timelineItemSpacing(
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

function shouldDefaultCollapseRelatedFiles(_isMobile?: boolean, _relatedFileCount?: number): boolean {
  return true;
}

const USER_MESSAGE_SUMMARY_LENGTH = 48;

function normalizeUserMessageSummary(content: string, emptyLabel: string): string {
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

function UserMessageListIcon() {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" width="1em" height="1em" viewBox="0 0 32 32" aria-hidden="true">
      <path d="M0 0h32v32H0z" fill="none" />
      <path fill="currentColor" d="M4.082 4.083v3h22.835v-3zm0 16.223h22.835v-3H4.082zm0-6.612h22.835v-3H4.082zm0 13.223h22.835v-3H4.082z" />
    </svg>
  );
}

function SessionViewerInner({
  session,
  loading = false,
  slashCommandResult = null,
  rootId,
  rootDisplayName,
  rootPath,
  rootColor,
  interactionMode = "main",
  targetSeq = 0,
  targetSeqRequestKey = "",
  gitFileStatsByPath = {},
  onFileClick,
  onRootClick,
  onRemoveRelatedFile,
  onAskUserAnswer,
  onEditUserMessage,
  onForkAgentMessage,
  agents,
  composerOverlayInset = 0,
  scrollContainerRef,
}: SessionViewerProps) {
  const { locale, t } = useI18n();
  const [relatedFilesCollapsed, setRelatedFilesCollapsed] = useState(true);
  const [isMobile, setIsMobile] = useState(() => {
    if (typeof window === "undefined") {
      return false;
    }
    return window.matchMedia("(max-width: 767px)").matches;
  });
  const [savedPromptKeys, setSavedPromptKeys] = useState<Record<string, true>>(
    {},
  );
  const [copiedMessageKeys, setCopiedMessageKeys] = useState<
    Record<string, true>
  >({});
  const scrollEndRef = useRef<HTMLDivElement>(null);
  const scrollRef = useRef<HTMLDivElement>(null);
  // ponytail: drawer now uses inner scroll container (upstream 6247756: BottomSheet overflow:hidden, inner overflow:auto)
  // outer drawerScrollRef is kept for BottomSheet sizing only, not for stick-to-bottom.
  const activeScrollRef = scrollRef;
  const onFileClickRef = useRef(onFileClick);
  const copyResetTimersRef = useRef<Record<string, number>>({});
  const relatedFilesDefaultStateRef = useRef<string>("");
  const userSummaryRootRef = useRef<HTMLDivElement | null>(null);
  const userSummaryListRef = useRef<HTMLDivElement | null>(null);
  const relatedFilesDividerRef = useRef<HTMLDivElement | null>(null);
  const sessionKey = session?.key || session?.session_key || null;
  // 会话归属节点：窗口拉取/图片等请求须路由到会话所在节点。同名根（如两台机器都有
  // root "mindfs"）仅凭裸 rootId 查裸键映射会串到另一节点（实测窗口 GET 404）。
  const sessionNodeId =
    String((session as any)?._nodeId || "").trim() || undefined;
  // 方案 B（超长会话窗口化）：可视数据以可见窗口为准，初始用全量/窗口做首帧，
  // 随后由 getSessionWindow({ latest: SESSION_WINDOW_SIZE }) 覆盖（见下方初始化 effect）。
  const [visibleExchanges, setVisibleExchanges] = useState<ExchangeArray>(
    () => (Array.isArray(session?.exchanges) ? session.exchanges : []),
  );
  const [visibleAux, setVisibleAux] = useState<Record<string, ExchangeAux[]>>(
    () => session?.exchange_aux || {},
  );
  const [windowMeta, setWindowMeta] = useState<SessionWindowMeta>({
    total: 0,
    hasMore: false,
    minSeq: 0,
    maxSeq: 0,
  });
  const [loadingMore, setLoadingMore] = useState(false);
  const topSentinelRef = useRef<HTMLDivElement>(null);
  const isPrependingRef = useRef(false);
  const isAwaiting = !!(session as any)?.pending;
  // 方案 C：overlay 尾巴 = App 缓存里「窗口尚未覆盖」的条目，只读派生、永不合并回窗口。
  // 窗口是唯一持久化源，只在 init 拉取 / 翻页前插 / 重锚定（_windowMeta）时整体替换。
  //
  // 判定刻意不依赖 windowMeta：loadMore 与 targetSeq 取窗都会用旧/中段窗口的 meta 覆盖
  // windowMeta，拿它当「会话最新 seq」会塌回旧值（曾导致按位次裁剪错算并把实时流式内容
  // 一起 slice 掉）。改为三个规则：
  //   - seq>0：由 latestSeq 界定。latestSeq 只增不减、按会话键绑定，且仅在 init/anchor 这类
  //     「最新窗口」经 applyWindow 更新（loadMore / targetSeq 不碰）。seq<=latestSeq → 窗口已含
  //     或即将含，丢弃；seq>latestSeq → 刚发出、窗口还没追上，保留（自己发的消息立即出现）。
  //   - seq==0 且 role=user：按 content 与窗口计数消抵——窗口里同内容出现 covered 次，就丢弃
  //     缓存中同内容的前 covered 个，其余保留。这是跨端丢事件（手机后台 / WS 断连）时的兜底，
  //     不依赖时间戳（客户端 ISO 毫秒 vs 服务端 RFC3339Nano 永不相等）。
  //   - seq==0 且 role=tool：callId 已出现在窗口 aux → 让位（见 windowToolCallIds）。
  //   - seq==0 的其它（流式文本 / 思考）：默认保留（流式内容要实时可见），但若窗口最新几条
  //     持久化行里已经包含它（去空白后判定），说明这一轮已落盘、这份是陈旧拷贝 → 让位。
  //     这是 2026-09-17 的缺口：以前这里「一律保留」，于是 ask 前后被渲染成两块一样的正文。
  // 只读派生不写回缓存，避免覆盖其它标签页可能正在流式写入的持久化缓存。
  const [latestSeqState, setLatestSeqState] = useState<{ key: string; max: number }>(
    { key: "", max: 0 },
  );
  const noteLatestSeq = useCallback(
    (key: string | null, meta: SessionWindowMeta | null | undefined) => {
      const max = Number(meta?.maxSeq || 0);
      if (!key || max <= 0) return;
      setLatestSeqState((prev) =>
        prev.key === key ? (max > prev.max ? { key, max } : prev) : { key, max },
      );
    },
    [],
  );
  const latestSeq = latestSeqState.key === sessionKey ? latestSeqState.max : 0;
  const visibleSeqSet = useMemo(() => {
    const set = new Set<number>();
    for (const ex of visibleExchanges) {
      const seq = Number((ex as any)?.seq || 0);
      if (seq > 0) set.add(seq);
    }
    return set;
  }, [visibleExchanges]);
  const windowUserCounts = useMemo(() => {
    const counts = new Map<string, number>();
    for (const ex of visibleExchanges) {
      if (String((ex as any)?.role || "").toLowerCase() !== "user") continue;
      const content = String((ex as any)?.content || "");
      counts.set(content, (counts.get(content) || 0) + 1);
    }
    return counts;
  }, [visibleExchanges]);
  // 窗口侧已渲染的 tool callId 集合：role=tool 的瞬时条目若其 callId 已存在于窗口的
  // exchange_aux 里，说明同一张卡会由 buildAssistantTimeline 从 aux 渲染一次 —— overlay
  // 必须让位，否则同一 callId 从两个数据源各渲染一份（实测 2026-09-12 症状 1：ask_user
  // 卡与推理文本各出现两份，且库里并没有重复数据）。
  const windowToolCallIds = useMemo(() => {
    const set = new Set<string>();
    for (const items of Object.values(visibleAux || {})) {
      for (const aux of items || []) {
        const callId = (aux as any)?.toolcall?.callId;
        if (typeof callId === "string" && callId) set.add(callId);
      }
    }
    return set;
  }, [visibleAux]);
  const windowTailTexts = useMemo(() => {
    const texts: string[] = [];
    const persisted = visibleExchanges.filter(
      (ex) => Number((ex as any)?.seq || 0) > 0,
    );
    for (const ex of persisted.slice(-OVERLAY_TAIL_ROWS)) {
      const text = normalizeOverlayText(String((ex as any)?.content || ""));
      if (text) texts.push(text);
    }
    return texts;
  }, [visibleExchanges]);
  const tailOverlay = useMemo(() => {
    const exs = Array.isArray(session?.exchanges)
      ? (session.exchanges as ExchangeArray)
      : ([] as ExchangeArray);
    // 「已经会被渲染的持久化正文」全集 = 窗口最新几行（windowTailTexts）+ 本函数自己
    // 要渲染的 seq>latestSeq 缓存条目。后者不能漏：重锚定还没把刚落盘的行拉进窗口时，
    // 那一行由 overlay 侧渲染（下面 seq>0 分支），若只跟窗口比，seq=0 的直播拷贝就找不到
    // 「已经显示过的那一份」，重复照旧（实测 2026-09-17 20:16，ask 上下各一整轮）。
    const renderedPersistedTexts = windowTailTexts.slice();
    for (const ex of exs) {
      const seq = Number((ex as any)?.seq || 0);
      if (seq <= 0) continue;
      if (visibleSeqSet.has(seq)) continue;
      if (latestSeq === 0 || seq <= latestSeq) continue;
      const text = normalizeOverlayText(String((ex as any)?.content || ""));
      if (text) renderedPersistedTexts.push(text);
    }
    const consumed = new Map<string, number>();
    const out: ExchangeArray = [];
    for (const ex of exs) {
      const seq = Number((ex as any)?.seq || 0);
      if (seq > 0) {
        // 已持久化条目：窗口已含（visibleSeqSet）或已被最新窗口的 seq 范围覆盖 → 不重复渲染。
        if (visibleSeqSet.has(seq)) continue;
        if (latestSeq === 0 || seq <= latestSeq) {
          // 已入库但落在窗口之外的持久化条目由窗口侧负责渲染，这里不再重复渲染。
          continue;
        }
        out.push(ex);
        continue;
      }
      // seq=0 的 tool 瞬时条目：窗口 aux 已含同 callId → 让位给窗口侧渲染。
      const transientCallId = (ex as any)?.toolCall?.callId;
      if (
        String((ex as any)?.role || "").toLowerCase() === "tool" &&
        typeof transientCallId === "string" &&
        transientCallId &&
        windowToolCallIds.has(transientCallId)
      ) {
        continue;
      }
      if (String((ex as any)?.role || "").toLowerCase() === "user") {
        const content = String((ex as any)?.content || "");
        const covered = windowUserCounts.get(content) || 0;
        const used = consumed.get(content) || 0;
        // ponytail: 同内容重复发言时按出现次序消抵，若窗口只回填了后发的那条（跨窗口滚动
        // 只载尾巴），可能抵消错那一条（显示成"后发的在窗口、先发的在 overlay"——条数仍对，
        // 归属可能错位）。升到"按 seq 区间匹配"可消除，但需要窗口的 minSeq/maxSeq 参与判断，
        // 收益极小（需同内容 + 两条同时在途 + 跨窗滚动），暂留此天花板。
        if (used < covered) {
          consumed.set(content, used + 1);
          continue;
        }
      }
      // seq==0 的直播正文/思考：同一轮若已落盘（本次会被渲染的持久化正文里已有包含它的），
      // 缓存里这份就是陈旧拷贝 → 让位。若不让位，「窗口/overlay 渲染一份 + 直播再渲染一份」
      // 会把同一轮显示两次，ask 卡上下各一整轮（实测 2026-09-17，库里并无重复数据）。
      const transientText = normalizeOverlayText(
        String((ex as any)?.content || ""),
      );
      if (
        transientText.length >= OVERLAY_DUP_MIN_CHARS &&
        renderedPersistedTexts.some((text) => text.includes(transientText))
      ) {
        continue;
      }
      out.push(ex);
    }
    return out;
  }, [session?.exchanges, sessionKey, latestSeq, visibleSeqSet, windowUserCounts, windowToolCallIds, windowTailTexts]);
  // 窗口态与 overlay 是两条独立来源，同一个 exchange 对象可能两边都在（重锚定会把整份
  // 缓存装进窗口态时就发生过）。按对象同一性取差集，保证同一份 exchange 只渲染一次 ——
  // 与 dedupeToolCards 同一条约定，只是这里对所有类型生效（thought/正文没有 callId 可去重）。
  const composedExchanges = useMemo(() => {
    const inWindow = new Set(visibleExchanges as unknown[]);
    const extra = tailOverlay.filter((ex) => !inWindow.has(ex));
    return [...visibleExchanges, ...extra] as ExchangeArray;
  }, [visibleExchanges, tailOverlay]);
  const { timeline, isStreaming, streamVersion, streamStatusText } = useSessionStream(
    sessionKey,
    composedExchanges,
    visibleAux,
    session?.context_window,
    isAwaiting,
  );
  const shouldStickToBottomRef = useRef(true);
  const lastSessionKeyRef = useRef<string | null>(null);
  const targetSeqScrollKeyRef = useRef("");
  const targetSeqFrameRef = useRef<number | null>(null);
  const targetSeqTimerRefs = useRef<number[]>([]);
  const [showJumpToLatest, setShowJumpToLatest] = useState(false);
  const [userSummaryHoverOpen, setUserSummaryHoverOpen] = useState(false);
  const [userSummaryPinnedOpen, setUserSummaryPinnedOpen] = useState(false);
  const [currentUserMessageIndex, setCurrentUserMessageIndex] = useState(0);
  const viewportStickFrameRef = useRef<number | null>(null);

  const cancelTargetSeqScroll = () => {
    if (targetSeqFrameRef.current !== null) {
      window.cancelAnimationFrame(targetSeqFrameRef.current);
      targetSeqFrameRef.current = null;
    }
    targetSeqTimerRefs.current.forEach((timer) => window.clearTimeout(timer));
    targetSeqTimerRefs.current = [];
  };

  const stickSessionToBottom = (behavior: ScrollBehavior = "auto") => {
    const container = activeScrollRef?.current;
    if (!container) {
      return;
    }
    const maxTop = Math.max(0, container.scrollHeight - container.clientHeight);
    container.scrollTo({ top: maxTop, behavior });
  };

  useEffect(() => {
    onFileClickRef.current = onFileClick;
  }, [onFileClick]);

  useEffect(() => {
    if (typeof window === "undefined") {
      return;
    }
    const media = window.matchMedia("(max-width: 767px)");
    const handleChange = () => {
      setIsMobile(media.matches);
    };
    handleChange();
    media.addEventListener("change", handleChange);
    return () => {
      media.removeEventListener("change", handleChange);
    };
  }, []);

  useEffect(() => {
    setSavedPromptKeys({});
    setCopiedMessageKeys({});
    setUserSummaryHoverOpen(false);
    setUserSummaryPinnedOpen(false);
    setCurrentUserMessageIndex(0);
    relatedFilesDefaultStateRef.current = "";
    Object.values(copyResetTimersRef.current).forEach((timer) =>
      window.clearTimeout(timer),
    );
    copyResetTimersRef.current = {};
  }, [sessionKey]);

  // 方案 B：从窗口响应中提取 exchanges/aux/meta（后端 window_meta 透传 total/hasMore/minSeq/maxSeq）。
  const applyWindow = useCallback(
    (
      res: { session: any; meta: SessionWindowMeta } | null,
      opts?: { keepOlder?: boolean },
    ) => {
      if (!res) return false;
      const winSession = res.session as any;
      const winExchanges = Array.isArray(winSession?.exchanges)
        ? (winSession.exchanges as ExchangeArray)
        : [];
      const winAux =
        (winSession?.exchange_aux as Record<string, ExchangeAux[]>) || {};
      setVisibleExchanges((prev) => {
        if (!opts?.keepOlder) return winExchanges;
        // 重锚定换窗时保住用户已翻上去的历史：窗口只覆盖 [minSeq, maxSeq]，
        // 比 minSeq 更老的条目是 loadMore 一页页取回来的，整体替换会把它们扔掉
        // —— 表现为「翻上去加载出来了、闪一下又回到 20 条」，而且顶部哨兵再次可见
        // 又触发 loadMore，形成 40↔20 的无限抖动。
        const winMinSeq = Number(res.meta?.minSeq || 0);
        if (!winMinSeq) return winExchanges;
        const older = prev.filter((ex) => {
          const seq = Number((ex as any)?.seq || 0);
          return seq > 0 && seq < winMinSeq;
        });
        return older.length ? [...older, ...winExchanges] : winExchanges;
      });
      setVisibleAux((prev) =>
        opts?.keepOlder ? { ...prev, ...winAux } : winAux,
      );
      setWindowMeta(res.meta);
      // applyWindow 只服务「最新窗口」（init / anchor）——loadMore 与 targetSeq 取窗直接
      // setWindowMeta，不经过这里，因此 latestSeq 不会被旧/中段窗口的 maxSeq 污染。
      noteLatestSeq(sessionKey, res.meta);
      return true;
    },
    [noteLatestSeq, sessionKey],
  );

  // 初始化：会话切换时拉取尾部窗口，复位置底；拉取失败回退到 props 传入的全量/窗口。
  useEffect(() => {
    let cancelled = false;
    setLoadingMore(false);
    if (!sessionKey) {
      setVisibleExchanges([]);
      setVisibleAux({});
      setWindowMeta({ total: 0, hasMore: false, minSeq: 0, maxSeq: 0 });
      return;
    }
    // 进入窗口化视图，隔离 syncSession 的 truncated 全量回补（见 session.ts windowedView 标记）。
    setWindowedView(sessionKey, true);
    // 方案 B 首帧按需：避免先用全量做首帧导致长对话从头刷到尾。
    // 若外层传入的是全量（可能来自 App 旧缓存或网络全量兜底），此处仅取尾部
    // SESSION_WINDOW_SIZE 条作首帧，随后 getSessionWindow({ latest: SESSION_WINDOW_SIZE })
    // 会以服务端窗口覆盖，保持首帧 O(SESSION_WINDOW_SIZE)。
    // 避免种子与 overlay 重复显示同一轮次。
    const incomingExs = Array.isArray(session?.exchanges) ? (session.exchanges as ExchangeArray) : ([] as ExchangeArray);
    const persistedSeed = incomingExs.filter((e) => Number((e as any)?.seq || 0) > 0);
    const seedExs = (persistedSeed.length > SESSION_WINDOW_SIZE
      ? persistedSeed.slice(-SESSION_WINDOW_SIZE)
      : persistedSeed) as ExchangeArray;
    const seedAux: Record<string, ExchangeAux[]> = {};
    const seedSeqs = new Set(seedExs.map((e) => Number((e as any)?.seq || 0)));
    for (const [k, v] of Object.entries((session?.exchange_aux || {}) as Record<string, ExchangeAux[]>)) {
      if (seedSeqs.has(Number(k))) seedAux[k] = v;
    }
    setVisibleExchanges(seedExs);
    setVisibleAux(seedAux);
    getSessionWindow(rootId || "", sessionKey, { latest: SESSION_WINDOW_SIZE, nodeId: sessionNodeId })
      .then((res) => {
        if (cancelled) return;
        applyWindow(res);
        if (res) {
          shouldStickToBottomRef.current = true;
          setShowJumpToLatest(false);
          window.requestAnimationFrame(() => stickSessionToBottom("auto"));
        }
      })
      .catch(() => {});
    return () => {
      cancelled = true;
      clearWindowedView(sessionKey);
    };
  }, [sessionKey, rootId, applyWindow]);

  // 向上翻页：锚定 scrollHeight-scrollTop，请求 beforeSeq=minSeq 的历史段，
  // 函数式前置合并并 RAF 回补滚动位置，避免视口跳动。
  const loadMore = useCallback(() => {
    const container = activeScrollRef?.current;
    if (!container || loadingMore || !windowMeta.hasMore || !sessionKey) {
      return;
    }
    const anchor = container.scrollHeight - container.scrollTop;
    setLoadingMore(true);
    isPrependingRef.current = true;
    getSessionWindow(rootId || "", sessionKey, {
        beforeSeq: windowMeta.minSeq,
        limit: SESSION_WINDOW_SIZE,
        nodeId: sessionNodeId,
      })
      .then((res) => {
        if (!res) {
          setLoadingMore(false);
          return;
        }
        const winSession = res.session as any;
        const winExchanges = Array.isArray(winSession?.exchanges)
          ? (winSession.exchanges as ExchangeArray)
          : [];
        const winAux =
          (winSession?.exchange_aux as Record<string, ExchangeAux[]>) || {};
        setVisibleExchanges((prev) => [...winExchanges, ...prev]);
        setVisibleAux((prev) => ({ ...prev, ...winAux }));
        setWindowMeta(res.meta);
        setLoadingMore(false);
        window.requestAnimationFrame(() => {
          const el = activeScrollRef?.current;
          if (el) {
            el.scrollTop = el.scrollHeight - anchor;
          }
          isPrependingRef.current = false;
        });
      })
      .catch(() => setLoadingMore(false));
  }, [loadingMore, windowMeta, sessionKey, rootId]);

  // 顶部哨兵：进入视口（rootMargin 200px）触发 loadMore，hasMore 耗尽后自动不观察。
  useEffect(() => {
    const sentinel = topSentinelRef.current;
    const root = scrollRef.current;
    if (!sentinel || !root || !windowMeta.hasMore) {
      return;
    }
    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((entry) => entry.isIntersecting)) {
          loadMore();
        }
      },
      { root, rootMargin: "200px 0px 0px 0px", threshold: 0 },
    );
    observer.observe(sentinel);
    return () => observer.disconnect();
  }, [windowMeta.hasMore, loadMore]);

  // 方案 C 重锚定（done/重连）：App 侧 restoreActiveSession 替换缓存并附 _windowMeta/_anchoredAt；
  // 此处按锚点一次性原子换窗——窗口整体替换，overlay 尾巴自然清空（缓存尾巴已被 App 清掉）。
  // 旧锚点不重复应用（App 的 spread 会把 _windowMeta 带到后续 cache 对象上）。
  const lastAppliedAnchorRef = useRef<{ key: string | null; at: number }>({
    key: null,
    at: -1,
  });
  useEffect(() => {
    const anchorAt = Number((session as any)?._anchoredAt || 0);
    const anchorMeta = (session as any)?._windowMeta as
      | SessionWindowMeta
      | undefined;
    if (!anchorAt || !anchorMeta || !sessionKey) {
      return;
    }
    const last = lastAppliedAnchorRef.current;
    if (last.key === sessionKey && last.at >= anchorAt) {
      return;
    }
    lastAppliedAnchorRef.current = { key: sessionKey, at: anchorAt };
    // 锚点载荷是 App 的整份缓存：持久化行 + 直播瞬时行（seq=0，见 App.loadSession 的
    // [...incoming.filter(seq>0), ...localTransient]）。窗口态只能装持久化行 ——
    // 瞬时行归 overlay 管，混进窗口后 overlay 会再输出一遍，同一段正文渲染两次
    // （实测 2026-09-17：ask 触发后 ask 上下各一块同样的分析文本，重开会话必现）。
    const anchorExchanges = (Array.isArray((session as any)?.exchanges)
      ? ((session as any).exchanges as ExchangeArray)
      : ([] as ExchangeArray)
    ).filter((ex) => Number((ex as any)?.seq || 0) > 0);
    applyWindow(
      {
        session: { ...(session as any), exchanges: anchorExchanges },
        meta: anchorMeta,
      },
      { keepOlder: true },
    );
  }, [session, sessionKey, applyWindow]);

  const userMessageSummaries = useMemo(
    () =>
      timeline
        .filter((item): item is TimelineItem & { type: "user_text"; id: string; content: string } => item.type === "user_text")
        .map((item, index) => ({
          id: item.id || `user-${index}`,
          index: index + 1,
          summary: normalizeUserMessageSummary(item.content || "", t("session.emptyMessage")),
        })),
    [timeline, t],
  );
  const userSummaryOpen = userSummaryHoverOpen || userSummaryPinnedOpen;

  const readCurrentUserMessageIndex = useCallback(() => {
    const container = activeScrollRef?.current;
    if (!container) {
      return 0;
    }
    const nodes = Array.from(
      container.querySelectorAll<HTMLElement>("[data-user-message-index]"),
    );
    if (nodes.length === 0) {
      return 0;
    }
    const containerRect = container.getBoundingClientRect();
    const viewportTop = containerRect.top;
    const viewportBottom = containerRect.bottom;
    let firstVisible = 0;
    let firstFullyEntered = 0;
    let lastBeforeViewport = 0;
    for (const node of nodes) {
      const index = Number(node.dataset.userMessageIndex || 0);
      if (!index) {
        continue;
      }
      const rect = node.getBoundingClientRect();
      if (rect.bottom < viewportTop) {
        lastBeforeViewport = index;
        continue;
      }
      if (rect.top > viewportBottom) {
        break;
      }
      const isVisible = rect.bottom >= viewportTop && rect.top <= viewportBottom;
      if (isVisible && !firstVisible) {
        firstVisible = index;
      }
      if (rect.top >= viewportTop && !firstFullyEntered) {
        firstFullyEntered = index;
      }
    }
    return (
      firstFullyEntered ||
      firstVisible ||
      lastBeforeViewport ||
      Number(nodes[0]?.dataset.userMessageIndex || 0)
    );
  }, []);

  const refreshCurrentUserMessageIndex = useCallback(() => {
    setCurrentUserMessageIndex(readCurrentUserMessageIndex());
  }, [readCurrentUserMessageIndex]);

  const scrollToUserMessageSummary = (index: number) => {
    const container = activeScrollRef?.current;
    if (!container) {
      return;
    }
    const node = container.querySelector<HTMLElement>(
      `[data-user-message-index="${index}"]`,
    );
    if (!node) {
      return;
    }
    shouldStickToBottomRef.current = false;
    cancelTargetSeqScroll();
    const containerRect = container.getBoundingClientRect();
    const nodeRect = node.getBoundingClientRect();
    const maxTop = Math.max(0, container.scrollHeight - container.clientHeight);
    const nextTop = Math.max(
      0,
      Math.min(
        maxTop,
        container.scrollTop +
          nodeRect.top -
          containerRect.top -
          container.clientHeight / 2 +
          nodeRect.height / 2,
      ),
    );
    container.scrollTop = nextTop;
    setShowJumpToLatest(nextTop < maxTop - 40);
    setUserSummaryPinnedOpen(false);
    setUserSummaryHoverOpen(false);
    setCurrentUserMessageIndex(index);
  };

  useEffect(() => {
    if (!userSummaryOpen) {
      return;
    }
    const nextIndex = readCurrentUserMessageIndex();
    setCurrentUserMessageIndex(nextIndex);
    if (!nextIndex) {
      return;
    }
    const frame = window.requestAnimationFrame(() => {
      const list = userSummaryListRef.current;
      const item = list?.querySelector<HTMLElement>(
        `[data-user-summary-index="${nextIndex}"]`,
      );
      item?.scrollIntoView({ block: "center" });
    });
    return () => window.cancelAnimationFrame(frame);
  }, [readCurrentUserMessageIndex, userSummaryOpen, userMessageSummaries.length]);

  useEffect(() => {
    if (!userSummaryOpen) {
      return;
    }
    const handlePointerDown = (event: PointerEvent) => {
      const root = userSummaryRootRef.current;
      if (!root || root.contains(event.target as Node)) {
        return;
      }
      setUserSummaryPinnedOpen(false);
      setUserSummaryHoverOpen(false);
    };
    document.addEventListener("pointerdown", handlePointerDown, true);
    return () => document.removeEventListener("pointerdown", handlePointerDown, true);
  }, [userSummaryOpen]);

  useEffect(() => {
    return () => {
      Object.values(copyResetTimersRef.current).forEach((timer) =>
        window.clearTimeout(timer),
      );
      copyResetTimersRef.current = {};
      if (targetSeqFrameRef.current !== null) {
        window.cancelAnimationFrame(targetSeqFrameRef.current);
        targetSeqFrameRef.current = null;
      }
      if (viewportStickFrameRef.current !== null) {
        window.cancelAnimationFrame(viewportStickFrameRef.current);
        viewportStickFrameRef.current = null;
      }
      targetSeqTimerRefs.current.forEach((timer) => window.clearTimeout(timer));
      targetSeqTimerRefs.current = [];
    };
  }, []);

  useEffect(() => {
    if (isPrependingRef.current) {
      // 顶部插入（loadMore 历史段）不触发底部跟随，视口由 loadMore 的 rAF 回补。
      return;
    }
    const container = activeScrollRef?.current;
    if (!container) {
      if (interactionMode === "drawer" && shouldStickToBottomRef.current) {
        const frame = window.requestAnimationFrame(() => {
          const retry = activeScrollRef?.current;
          if (retry && shouldStickToBottomRef.current) scrollToRelatedFilesDivider("auto");
        });
        return () => window.cancelAnimationFrame(frame);
      }
      return;
    }
    if (!scrollEndRef.current) {
      return;
    }
    const nextKey = sessionKey;
    const isSessionChanged = lastSessionKeyRef.current !== nextKey;
    if (isSessionChanged) {
      lastSessionKeyRef.current = nextKey;
      shouldStickToBottomRef.current = true;
    }
    if (shouldStickToBottomRef.current) {
      scrollToRelatedFilesDivider("auto");
    }
  }, [sessionKey, timeline, isStreaming, streamVersion, slashCommandResult]);

  useEffect(() => {
    const container = activeScrollRef?.current;
    if (!container || typeof window === "undefined") {
      return;
    }
    const queueStickToBottom = () => {
      if (!shouldStickToBottomRef.current) {
        return;
      }
      if (viewportStickFrameRef.current !== null) {
        window.cancelAnimationFrame(viewportStickFrameRef.current);
      }
      viewportStickFrameRef.current = window.requestAnimationFrame(() => {
        viewportStickFrameRef.current = null;
        if (!shouldStickToBottomRef.current) {
          return;
        }
        stickSessionToBottom("auto");
      });
    };
    window.visualViewport?.addEventListener("resize", queueStickToBottom);
    window.visualViewport?.addEventListener("scroll", queueStickToBottom);
    window.addEventListener("mindfs:safe-area-updated", queueStickToBottom as EventListener);
    return () => {
      window.visualViewport?.removeEventListener("resize", queueStickToBottom);
      window.visualViewport?.removeEventListener("scroll", queueStickToBottom);
      window.removeEventListener("mindfs:safe-area-updated", queueStickToBottom as EventListener);
      if (viewportStickFrameRef.current !== null) {
        window.cancelAnimationFrame(viewportStickFrameRef.current);
        viewportStickFrameRef.current = null;
      }
    };
  }, [sessionKey]);

  useEffect(() => {
    const el = activeScrollRef?.current;
    if (!el) {
      shouldStickToBottomRef.current = true;
      setShowJumpToLatest(false);
      return;
    }
    let lastScrollTop = el.scrollTop;
    const updateStickiness = () => {
      const viewportGap = window.visualViewport
        ? window.innerHeight - window.visualViewport.height - window.visualViewport.offsetTop
        : 0;
      const rawDistanceFromBottom = el.scrollHeight - el.clientHeight - el.scrollTop;
      const distanceFromBottom = Math.max(0, rawDistanceFromBottom - viewportGap);
      const isNearBottom = distanceFromBottom < 40;
      const movedUp = el.scrollTop < lastScrollTop;
      const movedDown = el.scrollTop > lastScrollTop;
      if (isNearBottom) {
        shouldStickToBottomRef.current = true;
      } else if (movedUp) {
        shouldStickToBottomRef.current = false;
      } else if (movedDown && distanceFromBottom < 200) {
        shouldStickToBottomRef.current = true;
      }
      setShowJumpToLatest(!shouldStickToBottomRef.current);
      refreshCurrentUserMessageIndex();
      lastScrollTop = el.scrollTop;
    };
    updateStickiness();
    el.addEventListener("scroll", updateStickiness, { passive: true });
    return () => {
      el.removeEventListener("scroll", updateStickiness);
    };
  }, [refreshCurrentUserMessageIndex, sessionKey]);

  useEffect(() => {
    if (!targetSeq) {
      targetSeqScrollKeyRef.current = "";
      return;
    }
    if (!sessionKey) {
      return;
    }
    const scrollKey = `${sessionKey || ""}:${targetSeq}:${targetSeqRequestKey}`;
    if (targetSeqScrollKeyRef.current === scrollKey) {
      return;
    }
    const container = activeScrollRef?.current;
    if (!container || !timeline.length) {
      return;
    }
    const node = container.querySelector<HTMLElement>(
      `[data-session-seq="${targetSeq}"]`,
    );
    const inRange =
      windowMeta.minSeq > 0 &&
      targetSeq >= windowMeta.minSeq &&
      targetSeq <= windowMeta.maxSeq;
    if (node && inRange) {
      targetSeqScrollKeyRef.current = scrollKey;
      shouldStickToBottomRef.current = false;
      cancelTargetSeqScroll();
      const scrollToNode = () => {
        const latestContainer = activeScrollRef?.current;
        const latestNode = latestContainer?.querySelector<HTMLElement>(
          `[data-session-seq="${targetSeq}"]`,
        );
        if (!latestContainer || !latestNode) {
          return;
        }
        shouldStickToBottomRef.current = false;
        const nextTop = Math.max(
          0,
          latestNode.offsetTop -
            latestContainer.clientHeight / 2 +
            latestNode.offsetHeight / 2,
        );
        latestContainer.scrollTo({ top: nextTop, behavior: "auto" });
      };
      targetSeqFrameRef.current = window.requestAnimationFrame(() => {
        targetSeqFrameRef.current = window.requestAnimationFrame(() => {
          targetSeqFrameRef.current = null;
          scrollToNode();
        });
      });
      [80, 220, 480].forEach((delay) => {
        const timer = window.setTimeout(() => {
          targetSeqTimerRefs.current = targetSeqTimerRefs.current.filter(
            (item) => item !== timer,
          );
          if (targetSeqScrollKeyRef.current !== scrollKey) {
            return;
          }
          scrollToNode();
        }, delay);
        targetSeqTimerRefs.current.push(timer);
      });
      return;
    }
    // 跨窗口：targetSeq 不在当前可见窗口内，拉取以 targetSeq 为中心的窗口再滚动。
    if (targetSeq > 0 && !inRange) {
      targetSeqScrollKeyRef.current = scrollKey;
      shouldStickToBottomRef.current = false;
      cancelTargetSeqScroll();
      getSessionWindow(rootId || "", sessionKey, {
          beforeSeq: targetSeq + Math.floor(SESSION_WINDOW_SIZE / 2),
          limit: SESSION_WINDOW_SIZE,
          nodeId: sessionNodeId,
        })
        .then((res) => {
          if (!res) return;
          const winSession = res.session as any;
          const winExchanges = Array.isArray(winSession?.exchanges)
            ? (winSession.exchanges as ExchangeArray)
            : [];
          const winAux =
            (winSession?.exchange_aux as Record<string, ExchangeAux[]>) || {};
          setVisibleExchanges(winExchanges);
          setVisibleAux(winAux);
          setWindowMeta(res.meta);
          window.requestAnimationFrame(() => {
            const el = activeScrollRef?.current;
            const targetNode = el?.querySelector<HTMLElement>(
              `[data-session-seq="${targetSeq}"]`,
            );
            if (el && targetNode) {
              shouldStickToBottomRef.current = false;
              el.scrollTo({
                top: Math.max(
                  0,
                  targetNode.offsetTop -
                    el.clientHeight / 2 +
                    targetNode.offsetHeight / 2,
                ),
                behavior: "auto",
              });
            }
          });
        })
        .catch(() => {});
    }
  }, [sessionKey, targetSeq, targetSeqRequestKey, timeline, windowMeta]);

  const rawRelated = session?.related_files || (session as any)?.outputs || [];
  const relatedFiles = (Array.isArray(rawRelated) ? rawRelated : [])
    .map((f: any) => {
      const path =
        typeof f === "string" ? f : typeof f?.path === "string" ? f.path : "";
      const name =
        typeof f?.name === "string" ? f.name : path.split("/").pop() || path;
      const head =
        typeof f !== "string" && typeof f?.head === "string" ? f.head : "";
      const repoPath =
        typeof f !== "string" && typeof f?.repo_path === "string"
          ? f.repo_path
          : "";
      const repoName =
        typeof f !== "string" && typeof f?.repo_name === "string"
          ? f.repo_name
          : repoPath.split(/[\\/]/).filter(Boolean).pop() || "";
      const repoKind =
        typeof f !== "string" && typeof f?.repo_kind === "string"
          ? f.repo_kind
          : "";
      const rootID =
        typeof f !== "string" && typeof f?.root_id === "string"
          ? f.root_id
          : "";
      return { path, name, head, repo_path: repoPath, repo_name: repoName, repo_kind: repoKind, root_id: rootID };
    })
    .filter((f) => f.path);
  const gitStatsRefreshKey = Object.entries(gitFileStatsByPath)
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([path, stats]) => `${path}:${stats.status}:${stats.additions}:${stats.deletions}`)
    .join("|");
  const relatedFileStatsByKey = useRelatedFileStats(
    rootId,
    relatedFiles,
    gitStatsRefreshKey,
    sessionNodeId,
  );
  const scrollToRelatedFilesDivider = (behavior: ScrollBehavior = "auto") => {
    const container = activeScrollRef?.current;
    if (!container) {
      return;
    }
    const maxTop = Math.max(0, container.scrollHeight - container.clientHeight);
    let top = maxTop;
    const divider =
      interactionMode === "drawer" ? relatedFilesDividerRef.current : null;
    if (divider && relatedFiles.length > 0 && !relatedFilesCollapsed) {
      const dividerTop =
        divider.getBoundingClientRect().top -
        container.getBoundingClientRect().top +
        container.scrollTop;
      const target = dividerTop - container.clientHeight * 0.08;
      top = Math.max(0, Math.min(maxTop, target));
    }
    container.scrollTo({ top, behavior });
  };
  const activeAskUserCallId = (() => {
    if (!isAwaiting) {
      return "";
    }
    for (let i = timeline.length - 1; i >= 0; i -= 1) {
      const item = timeline[i];
      if (item.type === "user_text" || item.type === "assistant_text") {
        return "";
      }
      if (item.type !== "tool") continue;
      const toolCall = item.toolCall || {};
      const kind = `${toolCall.kind || ""}`.toLowerCase();
      const status = `${toolCall.status || ""}`.toLowerCase();
      if (kind !== "ask_user") continue;
      if (
        status !== "running" &&
        status !== "pending" &&
        status !== "in_progress"
      ) {
        continue;
      }
      if (getAskUserQuestions(toolCall).length === 0) continue;
      return (
        toolCall.callId ||
        (typeof toolCall.meta?.toolUseId === "string"
          ? toolCall.meta.toolUseId
          : "")
      );
    }
    return "";
  })();
  const defaultRelatedFilesCollapsed = shouldDefaultCollapseRelatedFiles(
    isMobile,
    relatedFiles.length,
  );
  const relatedFilesDefaultStateKey = `${sessionKey || ""}:${isMobile ? "mobile" : "desktop"}:${defaultRelatedFilesCollapsed ? "collapsed" : "expanded"}`;

  useEffect(() => {
    if (relatedFilesDefaultStateRef.current === relatedFilesDefaultStateKey) {
      return;
    }
    relatedFilesDefaultStateRef.current = relatedFilesDefaultStateKey;
    setRelatedFilesCollapsed(defaultRelatedFilesCollapsed);
  }, [defaultRelatedFilesCollapsed, relatedFilesDefaultStateKey]);

  if (!session) {
    return (
      <div
        style={{
          padding: "40px",
          textAlign: "center",
          color: "var(--text-secondary)",
        }}
      >
        {t("session.empty")}
      </div>
    );
  }

  const displayFiles = relatedFiles;
  const displayFileGroups = (() => {
    const currentRootPath = String(rootPath || "").replace(/[\\/]+$/, "");
    const repoGroups = displayFiles.reduce<
      Array<{
        key: string;
        repoPath: string;
        repoName: string;
        repoKind: string;
        headGroups: Array<{ key: string; head: string; files: typeof displayFiles }>;
      }>
    >((groups, file) => {
    const head = file.head || "";
    const rawRepoPath = file.repo_path || "";
    const normalizedRepoPath = String(rawRepoPath || "").replace(/[\\/]+$/, "");
    const isCurrentRepoRecord =
      !rawRepoPath ||
      file.repo_name === t("session.currentProject") ||
      (!!currentRootPath && normalizedRepoPath === currentRootPath);
    const repoPath = isCurrentRepoRecord ? "" : rawRepoPath;
    const rawRepoKind = file.repo_kind || "";
    const repoKind = isCurrentRepoRecord && rawRepoKind !== "plain" ? "" : rawRepoKind;
    const repoKey = `${repoKind}\0${repoPath}`;
    let repoGroup = groups.find((group) => group.key === repoKey);
    if (!repoGroup) {
      repoGroup = {
        key: repoKey,
        repoPath,
        repoName: isCurrentRepoRecord
          ? t("session.currentProject")
          : file.repo_name || repoPath.split(/[\\/]/).filter(Boolean).pop() || t("session.currentProject"),
        repoKind,
        headGroups: [],
      };
      groups.push(repoGroup);
    }
    const headKey = `${repoKey}\0${head}`;
    const existing = repoGroup.headGroups.find((group) => group.key === headKey);
    if (existing) {
      existing.files.push(file);
    } else {
      repoGroup.headGroups.push({
        key: headKey,
        head,
        files: [file],
      });
    }
    return groups;
    }, []);
    return repoGroups.flatMap((repoGroup) =>
      repoGroup.headGroups.map((headGroup) => ({
        key: headGroup.key,
        head: headGroup.head,
        repoPath: repoGroup.repoPath,
        repoName: repoGroup.repoName,
        repoKind: repoGroup.repoKind,
        files: headGroup.files,
      })),
    );
  })();
  const displayName =
    session.name ||
    session.purpose ||
    session.key ||
    session.session_key ||
    "Session";
  const hasVisibleTimeline = timeline.length > 0;
  const themeColor = String(rootColor || "").trim() || "var(--accent-color)";
  // 四横主按钮：浅且暗的 muted 变体（同色相、混灰降明度）；徽标保持纯主题色
  const themeMuted = `color-mix(in srgb, ${themeColor} 56%, #94a3b8)`;
  const themeMutedBorder = `color-mix(in srgb, ${themeColor} 20%, transparent)`;
  const userMetaButtonStyle: React.CSSProperties = {
    width: "18px",
    height: "18px",
    border: "none",
    background: "transparent",
    padding: 0,
    margin: 0,
    color: themeColor,
    cursor: "pointer",
    fontSize: "14px",
    fontWeight: 800,
    lineHeight: 1,
    opacity: 1,
    display: "inline-flex",
    alignItems: "center",
    justifyContent: "center",
    borderRadius: "6px",
    flexShrink: 0,
  };

  const makePromptKey = (
    item: Extract<TimelineItem, { type: "user_text" | "assistant_text" }>,
  ): string =>
    `${item.id}\n${item.timestamp || ""}\n${item.content || ""}`;

  const renderTimelineItem = (
    item: TimelineItem,
    idx: number,
    spacing: string = "0",
  ) => {
    const timelineItemKey = item.id || `${item.type}-${idx}`;
    if (item.type === "thought") {
      return (
        <div key={timelineItemKey} style={{ marginTop: spacing }}>
          <ThinkingBlock content={item.content || ""} defaultExpanded={false} />
        </div>
      );
    }
    if (item.type === "tool") {
      const tc = item.toolCall || {};
      const isAskUser =
        `${tc.kind || ""}`.toLowerCase() === "ask_user" &&
        getAskUserQuestions(tc).length > 0;
      const isUserShell =
        `${tc.kind || ""}`.toLowerCase() === "execute" &&
        tc.meta?.source === "userShell";
      const toolUseId =
        tc.callId ||
        (typeof tc.meta?.toolUseId === "string" ? tc.meta.toolUseId : "");
      return (
        <div key={timelineItemKey} style={{ marginTop: spacing }}>
          {isAskUser ? (
            <AskUserQuestionCard
              toolCall={tc}
              rootId={rootId}
              sessionKey={sessionKey}
              agent={session?.agent}
              active={!!toolUseId && toolUseId === activeAskUserCallId}
              onAnswer={onAskUserAnswer}
            />
          ) : (
            <ToolCallCard
              kind={tc.kind}
              title={
                (tc as any).title ||
                (tc.meta && typeof tc.meta.title === "string"
                  ? (tc.meta.title as string)
                  : "")
              }
              callId={tc.callId || ""}
              status={tc.status || "running"}
              content={tc.content}
              result={formatToolCallFallbackResult(tc)}
              locations={tc.locations}
              meta={tc.meta}
              rootPath={rootPath || undefined}
              rootId={rootId}
              sessionKey={sessionKey}
              defaultExpanded={isUserShell}
            />
          )}
        </div>
      );
    }
    if (item.type === "todo") {
      return (
        <div key={timelineItemKey} style={{ marginTop: spacing }}>
          <TodoUpdateCard todoUpdate={item.todoUpdate} />
        </div>
      );
    }
    if (item.type === "plan") {
      return (
        <div key={timelineItemKey} style={{ marginTop: spacing }}>
          <PlanUpdateCard content={item.planUpdate?.content || ""} rootId={rootId} />
        </div>
      );
    }
    if (item.type === "compact") {
      return (
        <div key={timelineItemKey} style={{ marginTop: spacing }}>
          <CompactNoticeCard
            status={item.compactNotice?.status}
            summary={item.compactNotice?.summary}
          />
        </div>
      );
    }
    const isUser = item.type === "user_text";
    const userMessageIndex = isUser
      ? timeline.slice(0, idx + 1).filter((timelineItem) => timelineItem.type === "user_text").length
      : undefined;
    const next = idx + 1 < timeline.length ? timeline[idx + 1] : null;
    const hasFollowingAssistantFlow =
      !isUser && !!next && next.type !== "user_text";
    const hideAssistantMeta =
      !isUser &&
      (hasFollowingAssistantFlow ||
        (isStreaming && idx === timeline.length - 1));
    const time = formatTime(item.timestamp, locale);
    const uploadAttachments = isUser
      ? extractUploadAttachments(item.content || "")
      : [];
    const imageAttachments = uploadAttachments.filter(
      (attachment) => attachment.isImage,
    );
    const displayContent = isUser
      ? stripImageAttachmentTokens(item.content || "")
      : item.content || "";
    const promptSaveContent = isUser
      ? stripUploadAttachmentTokens(item.content || "")
      : "";
    const promptKey = makePromptKey(item);
    const promptSaved = !!savedPromptKeys[promptKey];
    const copySucceeded = !!copiedMessageKeys[promptKey];
    const userMessageWidth =
      imageAttachments.length > 0 ? "min(320px, 100%)" : "auto";
    const hasRichUserAttachments = imageAttachments.length > 0;
    const assistantMarkdownContent = !isUser ? (item.content || "").trim() : "";
    const assistantExchangeMeta = !isUser
      ? formatAssistantExchangeMeta(item, agents)
      : "";
    const assistantDurationLabel = !isUser
      ? formatSessionDuration(previousUserTimestamp(timeline, idx), item.timestamp)
      : "";
    const canForkAgentMessage = !isUser && Number(item.seq || 0) > 0 && !!onForkAgentMessage;
    return (
      <div
        key={timelineItemKey}
        data-session-seq={item.seq || undefined}
        data-user-message-index={userMessageIndex}
        style={{
          marginTop: spacing,
          alignSelf: isUser ? "flex-end" : "flex-start",
          width: isUser ? userMessageWidth : "100%",
          maxWidth: isUser ? "80%" : "100%",
          minWidth: 0,
          position: "relative",
          display: "flex",
          flexDirection: "column",
        }}
      >
        {isUser ? (
          <div
            style={{
              display: "flex",
              flexDirection: "column",
              alignItems: "stretch",
              gap: "6px",
              width: userMessageWidth,
              maxWidth: "100%",
              minWidth: 0,
            }}
          >
            {hasRichUserAttachments ? (
              <div
                style={{
                  width: "100%",
                  maxWidth: "100%",
                  minWidth: 0,
                  padding: "8px",
                  borderRadius: "18px 18px 4px 18px",
                  background: "rgba(148,163,184,0.14)",
                  display: "flex",
                  flexDirection: "column",
                  gap: "8px",
                  boxSizing: "border-box",
                }}
              >
                {imageAttachments.length > 0 ? (
                  <div
                    style={{
                      display: "grid",
                      gridTemplateColumns:
                        imageAttachments.length > 1
                          ? "repeat(2, minmax(0, 1fr))"
                          : "minmax(0, 1fr)",
                      gap: "8px",
                      width: "100%",
                    }}
                  >
                    {imageAttachments.map((attachment) => (
                      <button
                        key={attachment.path}
                        type="button"
                        onClick={() =>
                          onFileClickRef.current?.({ path: attachment.path })
                        }
                        style={{
                          border: "none",
                          padding: 0,
                          background: "transparent",
                          cursor: "pointer",
                          borderRadius: "12px",
                          overflow: "hidden",
                        }}
                        title={attachment.name}
                      >
                        <AttachmentImage
                          rootId={rootId || ""}
                          path={attachment.path}
                          name={attachment.name}
                        />
                      </button>
                    ))}
                  </div>
                ) : null}
                {displayContent ? (
                  <div
                    style={{
                      padding:
                        imageAttachments.length > 0 ? "2px 6px 0" : "6px 8px",
                      color: "var(--text-primary)",
                      fontSize: "14px",
                      lineHeight: "1.5",
                      whiteSpace: "pre-wrap",
                      overflowWrap: "anywhere",
                      wordBreak: "break-word",
                    }}
                  >
                    <InlineTokenText
                      content={displayContent}
                      isDark={false}
                      variant="inverse"
                    />
                  </div>
                ) : null}
              </div>
            ) : null}
            {!hasRichUserAttachments && displayContent ? (
              <div
                style={{
                  padding: "10px 16px",
                  borderRadius: "18px 18px 4px 18px",
                  background: "rgba(148,163,184,0.14)",
                  color: "var(--text-primary)",
                  fontSize: "14px",
                  lineHeight: "1.5",
                  boxShadow: "none",
                  whiteSpace: "pre-wrap",
                  overflowWrap: "anywhere",
                  wordBreak: "break-word",
                  alignSelf: "flex-end",
                  maxWidth: "100%",
                  minWidth: 0,
                }}
              >
                <InlineTokenText
                  content={displayContent}
                  isDark={false}
                  variant="inverse"
                />
              </div>
            ) : null}
            <span
              style={{
                fontSize: "10px",
                color: "var(--text-secondary)",
                opacity: 0.5,
                alignSelf: "flex-end",
                display: "inline-flex",
                alignItems: "center",
                gap: "4px",
              }}
            >
              {item.pendingAck ? (
                <span
                  aria-label={t("session.sending")}
                  style={{
                    width: "8px",
                    height: "8px",
                    border: "1px solid var(--text-secondary)",
                    borderTopColor: "transparent",
                    borderRadius: "50%",
                    display: "inline-block",
                    animation: "spin 0.8s linear infinite",
                  }}
                />
              ) : null}
              <span>{time}</span>
              <button
                type="button"
                onClick={() => {
                  onEditUserMessage?.(item.content || "");
                }}
                style={userMetaButtonStyle}
                aria-label={t("session.editMessage")}
                title={t("session.editMessage")}
              >
                {renderToolIcon("edit", themeColor)}
              </button>
              {promptSaved ? (
                <span
                  aria-label={t("session.promptSaved")}
                  title={t("session.promptSaved")}
                  style={{
                    ...userMetaButtonStyle,
                    color: themeColor,
                    fontSize: "13px",
                  }}
                >
                  ✓
                </span>
              ) : (
                <button
                  type="button"
                  onClick={() => {
                    if (!promptSaveContent) {
                      reportError(
                        "file.write_failed",
                        t("session.emptyPromptCannotSave"),
                      );
                      return;
                    }
                    void savePrompt(promptSaveContent)
                      .then(() => {
                        setSavedPromptKeys((prev) => ({
                          ...prev,
                          [promptKey]: true,
                        }));
                      })
                      .catch((err) => {
                        reportError(
                          "file.write_failed",
                          String((err as Error)?.message || t("session.savePromptFailed")),
                        );
                      });
                  }}
                  style={userMetaButtonStyle}
                  aria-label={t("session.addPrompt")}
                  title={t("session.addPrompt")}
                >
                  <svg
                    xmlns="http://www.w3.org/2000/svg"
                    width="14"
                    height="14"
                    viewBox="0 0 16 16"
                    aria-hidden="true"
                  >
                    <path
                      fill="currentColor"
                      stroke="currentColor"
                      strokeWidth="0.45"
                      strokeLinejoin="round"
                      d="M1.086 5.183A2.5 2.5 0 0 1 2.854 2.12l3.863-1.035A2.5 2.5 0 0 1 9.78 2.854L10.354 5H9.32l-.506-1.888a1.5 1.5 0 0 0-1.837-1.06L3.112 3.087a1.5 1.5 0 0 0-1.06 1.837l1.035 3.864a1.5 1.5 0 0 0 1.837 1.06L5 9.828v1.028a2.5 2.5 0 0 1-2.879-1.81zM8 6a2 2 0 0 0-2 2v5a2 2 0 0 0 2 2h5a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2zM7 8a1 1 0 0 1 1-1h5a1 1 0 0 1 1 1v5a1 1 0 0 1-1 1H8a1 1 0 0 1-1-1zm4 .5a.5.5 0 0 0-1 0V10H8.5a.5.5 0 0 0 0 1H10v1.5a.5.5 0 0 0 1 0V11h1.5a.5.5 0 0 0 0-1H11z"
                    />
                  </svg>
                </button>
              )}
              <button
                type="button"
                onClick={() => {
                  if (!promptSaveContent) {
                    reportError("clipboard.write_failed", t("session.emptyMessageCannotCopy"));
                    return;
                  }
                  const markCopied = () => {
                    setCopiedMessageKeys((prev) => ({
                      ...prev,
                      [promptKey]: true,
                    }));
                    if (copyResetTimersRef.current[promptKey]) {
                      window.clearTimeout(copyResetTimersRef.current[promptKey]);
                    }
                    copyResetTimersRef.current[promptKey] = window.setTimeout(() => {
                      setCopiedMessageKeys((prev) => {
                        const next = { ...prev };
                        delete next[promptKey];
                        return next;
                      });
                      delete copyResetTimersRef.current[promptKey];
                    }, 1000);
                  };
                  void copyText(promptSaveContent)
                    .then(markCopied)
                    .catch((err) => {
                      reportError(
                        "clipboard.write_failed",
                        String((err as Error)?.message || t("session.copyFailed")),
                      );
                    });
                }}
                style={userMetaButtonStyle}
                aria-label={t("session.copyMessage")}
                title={t("session.copyMessage")}
              >
                {copySucceeded ? (
                  <span
                    aria-hidden="true"
                    style={{ fontSize: "13px", fontWeight: 800, lineHeight: 1, color: themeColor }}
                  >
                    ✓
                  </span>
                ) : (
                  <svg
                    xmlns="http://www.w3.org/2000/svg"
                    width="14"
                    height="14"
                    viewBox="0 0 24 24"
                    aria-hidden="true"
                  >
                    <path
                      fill="currentColor"
                      d="M20 2H10c-1.1 0-2 .9-2 2v10c0 1.1.9 2 2 2h10c1.1 0 2-.9 2-2V4c0-1.1-.9-2-2-2m0 12H10V4h10z"
                    />
                    <path
                      fill="currentColor"
                      d="M14 20H4V10h2V8H4c-1.1 0-2 .9-2 2v10c0 1.1.9 2 2 2h10c1.1 0 2-.9 2-2v-2h-2z"
                    />
                  </svg>
                )}
              </button>
            </span>
          </div>
        ) : (
          <div
            style={{
              width: "100%",
              minWidth: 0,
              display: "flex",
              flexDirection: "column",
              alignItems: "flex-start",
            }}
          >
            <div
              style={{
                color: "var(--text-primary)",
                fontSize: "15px",
                lineHeight: "1.7",
                width: "100%",
                minWidth: 0,
              }}
            >
              <MarkdownViewer
                content={item.content || ""}
                root={rootId || undefined}
                onFileClick={(path) => onFileClickRef.current?.({ path })}
              />
            </div>
            {!hideAssistantMeta && (
              <span
                style={{
                  alignSelf: "flex-start",
                  display: "inline-flex",
                  flexWrap: "nowrap",
                  alignItems: "center",
                  gap: "8px",
                  maxWidth: "100%",
                  minWidth: 0,
                  fontSize: "10px",
                  color: "var(--text-secondary)",
                  opacity: 0.5,
                  marginTop: "-10px",
                  marginBottom: "4px",
                }}
              >
                <span
                  style={{
                    display: "inline-flex",
                    alignItems: "center",
                    gap: "3px",
                    flexShrink: 0,
                  }}
                >
                  <button
                    type="button"
                    onClick={() => {
                      if (!assistantMarkdownContent) {
                        reportError(
                          "clipboard.write_failed",
                          t("session.emptyMessageCannotCopy"),
                        );
                        return;
                      }
                      void copyText(assistantMarkdownContent)
                        .then(() => {
                          setCopiedMessageKeys((prev) => ({
                            ...prev,
                            [promptKey]: true,
                          }));
                          if (copyResetTimersRef.current[promptKey]) {
                            window.clearTimeout(
                              copyResetTimersRef.current[promptKey],
                            );
                          }
                          copyResetTimersRef.current[promptKey] =
                            window.setTimeout(() => {
                              setCopiedMessageKeys((prev) => {
                                const next = { ...prev };
                                delete next[promptKey];
                                return next;
                              });
                              delete copyResetTimersRef.current[promptKey];
                            }, 1000);
                        })
                        .catch((err) => {
                          reportError(
                            "clipboard.write_failed",
                            String((err as Error)?.message || t("session.copyFailed")),
                          );
                        });
                    }}
                    style={userMetaButtonStyle}
                    aria-label={t("session.copyMarkdown")}
                    title={t("session.copyMarkdown")}
                  >
                    {copySucceeded ? (
                      <span
                        aria-hidden="true"
                        style={{
                          fontSize: "13px",
                          fontWeight: 800,
                          lineHeight: 1,
                          color: themeColor,
                        }}
                      >
                        ✓
                      </span>
                    ) : (
                      <svg
                        xmlns="http://www.w3.org/2000/svg"
                        width="14"
                        height="14"
                        viewBox="0 0 24 24"
                        aria-hidden="true"
                      >
                        <path
                          fill="currentColor"
                          d="M20 2H10c-1.1 0-2 .9-2 2v10c0 1.1.9 2 2 2h10c1.1 0 2-.9 2-2V4c0-1.1-.9-2-2-2m0 12H10V4h10z"
                        />
                        <path
                          fill="currentColor"
                          d="M14 20H4V10h2V8H4c-1.1 0-2 .9-2 2v10c0 1.1.9 2 2 2h10c1.1 0 2-.9 2-2v-2h-2z"
                        />
                      </svg>
                    )}
                  </button>
                  {canForkAgentMessage ? (
                    <button
                      type="button"
                      onClick={() => {
                        const seq = Number(item.seq || 0);
                        if (seq > 0) {
                          void onForkAgentMessage?.(seq);
                        }
                      }}
                      style={userMetaButtonStyle}
                      aria-label={t("session.forkFromMessage")}
                      title={t("session.forkFromMessage")}
                    >
                      <ForkIcon />
                    </button>
                  ) : null}
                  <AgentIcon
                    agentName={item.agent || ""}
                    style={{ width: "12px", height: "12px", flexShrink: 0 }}
                  />
                </span>
                <span
                  style={{
                    display: "inline-flex",
                    flex: "1 1 auto",
                    flexWrap: "wrap",
                    alignItems: "center",
                    gap: "0 8px",
                    minWidth: 0,
                    lineHeight: "16px",
                  }}
                >
                  {assistantExchangeMeta ? (
                    <span
                      style={{
                        display: "inline-flex",
                        alignItems: "center",
                        minHeight: "16px",
                        minWidth: 0,
                        maxWidth: "100%",
                        overflowWrap: "anywhere",
                      }}
                    >
                      {assistantExchangeMeta}
                    </span>
                  ) : null}
                  <span
                    style={{
                      display: "inline-flex",
                      alignItems: "center",
                      minHeight: "16px",
                      minWidth: 0,
                      maxWidth: "100%",
                      overflowWrap: "anywhere",
                    }}
                  >
                    <TurnTokenUsage usage={item.tokenUsage} />
                  </span>
                  <span
                    style={{
                      display: "inline-flex",
                      alignItems: "center",
                      minHeight: "16px",
                      minWidth: 0,
                      maxWidth: "100%",
                      fontVariantNumeric: "tabular-nums",
                      overflowWrap: "anywhere",
                    }}
                  >
                    {time}{assistantDurationLabel ? ` ${assistantDurationLabel}` : ""}
                  </span>
                  <span
                    style={{
                      display: "inline-flex",
                      alignItems: "center",
                      minHeight: "16px",
                      minWidth: 0,
                      maxWidth: "100%",
                      overflowWrap: "anywhere",
                    }}
                  >
                    <ContextWindowBadge contextWindow={item.contextWindow} />
                  </span>
                </span>
              </span>
            )}
          </div>
        )}
      </div>
    );
  };

  const renderSlashCommandResult = () => {
    if (!slashCommandResult) {
      return null;
    }
    const commandLabel = `/${slashCommandResult.command || "status"}`;
    const loginNotice = slashCommandResult.loginNotice;
    const isLogin = (slashCommandResult.command || "") === "login";
    const content =
      slashCommandResult.error ||
      loginNotice?.error ||
      slashCommandResult.content ||
      (slashCommandResult.status === "running"
        ? isLogin
          ? t("session.loginWaiting")
          : t("session.statusFetching")
        : "");
    const loginCodeCopyKey = loginNotice?.loginId
      ? `login-code:${loginNotice.loginId}`
      : `login-code:${slashCommandResult.sessionKey}`;
    const loginCodeCopied = !!copiedMessageKeys[loginCodeCopyKey];
    return (
      <div
        style={{
          marginTop: hasVisibleTimeline ? "18px" : "0",
          width: "100%",
          boxSizing: "border-box",
          border: "1px solid rgba(148,163,184,0.36)",
          background: "rgba(148,163,184,0.10)",
          borderRadius: "8px",
          padding: "10px 12px",
          color: "var(--text-primary)",
        }}
      >
        <div
          style={{
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
            gap: "8px",
            marginBottom: content ? "6px" : 0,
            fontSize: "11px",
            lineHeight: 1.4,
            color: "var(--text-secondary)",
          }}
        >
          <span style={{ fontFamily: "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace" }}>
            {commandLabel}
          </span>
          <span>
            {slashCommandResult.status === "running"
              ? t("session.statusRunning")
              : slashCommandResult.status === "failed"
                ? t("session.statusFailed")
                : t("session.statusComplete")}
          </span>
        </div>
        {isLogin && loginNotice?.userCode ? (
          <div
            style={{
              display: "flex",
              flexDirection: "column",
              gap: "8px",
              fontSize: "13px",
              lineHeight: 1.5,
            }}
          >
            {loginNotice.verificationUrl ? (
              <a
                href={loginNotice.verificationUrl}
                target="_blank"
                rel="noreferrer"
                style={{ color: "var(--accent)", overflowWrap: "anywhere" }}
              >
                {loginNotice.verificationUrl}
              </a>
            ) : null}
            <div
              style={{
                display: "flex",
                alignItems: "center",
                gap: "8px",
                flexWrap: "wrap",
              }}
            >
              <span
                style={{
                  fontFamily: "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace",
                  fontSize: "20px",
                  letterSpacing: "0",
                  color: "var(--text-primary)",
                }}
              >
                {loginNotice.userCode}
              </span>
              <button
                type="button"
                onClick={() => {
                  const userCode = loginNotice.userCode || "";
                  if (!userCode) {
                    reportError("clipboard.write_failed", t("session.emptyCodeCannotCopy"));
                    return;
                  }
                  void copyText(userCode)
                    .then(() => {
                      setCopiedMessageKeys((prev) => ({
                        ...prev,
                        [loginCodeCopyKey]: true,
                      }));
                      if (copyResetTimersRef.current[loginCodeCopyKey]) {
                        window.clearTimeout(
                          copyResetTimersRef.current[loginCodeCopyKey],
                        );
                      }
                      copyResetTimersRef.current[loginCodeCopyKey] =
                        window.setTimeout(() => {
                          setCopiedMessageKeys((prev) => {
                            const next = { ...prev };
                            delete next[loginCodeCopyKey];
                            return next;
                          });
                          delete copyResetTimersRef.current[loginCodeCopyKey];
                        }, 1000);
                    })
                    .catch((err) => {
                      reportError(
                        "clipboard.write_failed",
                        String((err as Error)?.message || t("session.copyFailed")),
                      );
                    });
                }}
                style={{
                  display: "inline-flex",
                  alignItems: "center",
                  justifyContent: "center",
                  width: "22px",
                  height: "22px",
                  border: "none",
                  background: "transparent",
                  color: "var(--text-secondary)",
                  borderRadius: "6px",
                  padding: 0,
                  cursor: "pointer",
                }}
                aria-label={loginCodeCopied ? t("session.codeCopied") : t("session.copyCode")}
                title={loginCodeCopied ? t("session.copied") : t("session.copyCode")}
              >
                {loginCodeCopied ? (
                  <span
                    aria-hidden="true"
                    style={{ fontSize: "13px", fontWeight: 800, lineHeight: 1, color: themeColor }}
                  >
                    ✓
                  </span>
                ) : (
                  <svg
                    xmlns="http://www.w3.org/2000/svg"
                    width="14"
                    height="14"
                    viewBox="0 0 24 24"
                    aria-hidden="true"
                  >
                    <path
                      fill="currentColor"
                      d="M20 2H10c-1.1 0-2 .9-2 2v10c0 1.1.9 2 2 2h10c1.1 0 2-.9 2-2V4c0-1.1-.9-2-2-2m0 12H10V4h10z"
                    />
                    <path
                      fill="currentColor"
                      d="M14 20H4V10h2V8H4c-1.1 0-2 .9-2 2v10c0 1.1.9 2 2 2h10c1.1 0 2-.9 2-2v-2h-2z"
                    />
                  </svg>
                )}
              </button>
            </div>
            {slashCommandResult.status === "complete" ? (
              <div style={{ color: "var(--text-secondary)" }}>
                {t("session.loginComplete")}
                {loginNotice.planType ? ` · ${loginNotice.planType}` : ""}
              </div>
            ) : null}
          </div>
        ) : content ? (
          <div
            style={{
              fontSize: "13px",
              lineHeight: "1.6",
              minWidth: 0,
              overflowWrap: "anywhere",
            }}
          >
            <MarkdownViewer
              content={content}
              root={rootId || undefined}
              onFileClick={(path) => onFileClickRef.current?.({ path })}
            />
          </div>
        ) : null}
      </div>
    );
  };

  return (
    <div
      style={{
        flex: 1,
        minHeight: 0,
        minWidth: 0,
        display: "flex",
        flexDirection: "column",
        background: "transparent",
      }}
    >
      {interactionMode === "drawer" ? null : (
        <header
          style={{
            height: "36px",
            padding: "0 16px",
            borderBottom: "1px solid var(--border-color)",
            display: "flex",
            alignItems: "center",
            background: "var(--mindfs-topbar-bg, transparent)",
            boxSizing: "border-box",
            zIndex: 10,
            flexShrink: 0,
          }}
        >
          <h1
            style={{
              display: "flex",
              alignItems: "center",
              gap: "8px",
              fontSize: "14px",
              fontWeight: 600,
              margin: 0,
              minWidth: 0,
            }}
          >
            {rootId ? (
              <button
                type="button"
                onClick={() => onRootClick?.(rootId)}
                style={{
                  ...rootBadgeButtonStyle,
                  background: "var(--node-badge-bg)",
                  color: String(rootColor || "").trim() || "var(--text-primary)",
                  flexShrink: 0,
                  cursor: onRootClick ? "pointer" : "default",
                }}
              >
                {rootDisplayName || rootId}
              </button>
            ) : null}
            <span
              style={{
                minWidth: 0,
                overflow: "hidden",
                textOverflow: "ellipsis",
                whiteSpace: "nowrap",
              }}
            >
              {displayName}
            </span>
          </h1>
        </header>
      )}

      {/* 滚动容器 */}
      <div style={{ flex: 1, minHeight: 0, minWidth: 0, position: "relative" }}>
        <div ref={scrollRef} style={{ flex: 1, minHeight: 0, minWidth: 0, height: "100%", overflowY: "auto", overflowX: "hidden", position: "relative", WebkitOverflowScrolling: "touch" }}>
          <div style={{
            width: "100%",
            minWidth: 0,
            display: "block",
            padding: "24px 16px",
            boxSizing: "border-box",
            overflowX: "hidden",
          }} data-mindfs-session-content-width="1">
          <div style={{ width: "100%", minWidth: 0, margin: "0", display: "flex", flexDirection: "column" }}>
            {/* 方案 B：顶部哨兵，进入视口触发历史段加载（IntersectionObserver） */}
            <div ref={topSentinelRef} style={{ height: 1 }} aria-hidden="true" />
            {loadingMore ? (
              <div
                style={{
                  display: "flex",
                  justifyContent: "center",
                  padding: "8px 0",
                }}
              >
                <span
                  style={{
                    width: "14px",
                    height: "14px",
                    border: "2px solid var(--border-color)",
                    borderTopColor: "var(--accent-color)",
                    borderRadius: "50%",
                    animation: "spin 0.8s linear infinite",
                  }}
                />
              </div>
            ) : null}
            {loading && !hasVisibleTimeline ? (
              <div
                style={{
                  display: "flex",
                  flexDirection: "column",
                  gap: "10px",
                  padding: "6px 0 8px",
                }}
              >
                {[0, 1, 2].map((index) => (
                  <div
                    key={index}
                    style={{
                      width: index === 1 ? "78%" : index === 2 ? "64%" : "52%",
                      height: index === 0 ? "18px" : "72px",
                      alignSelf:
                        index === 0
                          ? "flex-start"
                          : index === 1
                            ? "flex-end"
                            : "flex-start",
                      borderRadius: index === 0 ? "8px" : "18px",
                      background:
                        "linear-gradient(90deg, rgba(148,163,184,0.12) 0%, rgba(148,163,184,0.24) 50%, rgba(148,163,184,0.12) 100%)",
                      backgroundSize: "200% 100%",
                      animation:
                        "sessionLoadingPulse 1.1s ease-in-out infinite",
                    }}
                  />
                ))}
              </div>
            ) : null}
            {timeline.map((item, idx) =>
              renderTimelineItem(
                item,
                idx,
                timelineItemSpacing(idx > 0 ? timeline[idx - 1] : null, item),
              ),
            )}
            {renderSlashCommandResult()}
            {(isAwaiting || isStreaming) && (() => {
              const awaitingColor = String(rootColor || "").trim() || "var(--accent-color)";
              return (
                <div
                  style={{
                    marginTop: "16px",
                    display: "flex",
                    alignItems: "center",
                    gap: "6px",
                    fontSize: "12px",
                    color: "var(--text-secondary)",
                  }}
                >
                  <span
                    style={{
                      width: "8px",
                      height: "8px",
                      borderRadius: "50%",
                      background: awaitingColor,
                      animation: "pulse 1s infinite",
                    }}
                  />
                {isStreaming
                  ? streamStatusText || t("session.generating")
                  : t("session.sentWaiting")}
              </div>
              );
            })()}

            {relatedFiles.length > 0 && interactionMode !== "drawer" && (
              <div
                ref={relatedFilesDividerRef}
                style={{
                  marginTop: "18px",
                  paddingTop: "14px",
                  borderTop: "1px solid var(--border-color)",
                  width: "100%",
                  boxSizing: "border-box",
                }}
              >
                <div
                  role="button"
                  tabIndex={0}
                  onClick={() => setRelatedFilesCollapsed((value) => !value)}
                  onKeyDown={(event) => {
                    if (event.key === "Enter" || event.key === " ") {
                      event.preventDefault();
                      setRelatedFilesCollapsed((value) => !value);
                    }
                  }}
                  style={{
                    fontSize: "12px",
                    fontWeight: 500,
                    color: "var(--text-secondary)",
                    marginBottom: "6px",
                    display: "flex",
                    alignItems: "center",
                    justifyContent: "space-between",
                    cursor: "pointer",
                    borderRadius: "6px",
                    padding: "2px 0",
                    outline: "none",
                  }}
                >
                  <span>{t("session.relatedFiles", { count: relatedFiles.length })}</span>
                  <div
                    style={{
                      display: "inline-flex",
                      alignItems: "center",
                      gap: "10px",
                    }}
                  >
                    <button
                      type="button"
                      onClick={(event) => {
                        event.stopPropagation();
                        setRelatedFilesCollapsed((value) => !value)
                      }}
                      aria-label={
                        relatedFilesCollapsed ? t("session.expandRelatedFiles") : t("session.collapseRelatedFiles")
                      }
                      title={
                        relatedFilesCollapsed ? t("session.expandRelatedFiles") : t("session.collapseRelatedFiles")
                      }
                      style={{
                        border: "none",
                        background: "transparent",
                        padding: 0,
                        margin: 0,
                        cursor: "pointer",
                        color: "var(--text-secondary)",
                        width: "16px",
                        height: "16px",
                        display: "inline-flex",
                        alignItems: "center",
                        justifyContent: "center",
                        flexShrink: 0,
                      }}
                    >
                      <span
                        aria-hidden="true"
                        style={{
                          flexShrink: 0,
                          transform: relatedFilesCollapsed
                            ? "rotate(0deg)"
                            : "rotate(90deg)",
                          transition: "transform 0.2s",
                          color: "var(--text-secondary)",
                          display: "inline-flex",
                          alignItems: "center",
                        }}
                      >
                        <svg
                          width="12"
                          height="12"
                          viewBox="0 0 24 24"
                          fill="none"
                          stroke="currentColor"
                          strokeWidth="2.25"
                          strokeLinecap="round"
                          strokeLinejoin="round"
                        >
                          <polyline points="9 18 15 12 9 6" />
                        </svg>
                      </span>
                    </button>
                  </div>
                </div>
                {!relatedFilesCollapsed ? (
                  <div
                    style={{
                      display: "flex",
                      flexDirection: "column",
                      gap: "4px",
                    }}
                  >
                    {displayFileGroups.map((group) => {
                      const normalizedRepoPath = String(group.repoPath || "").replace(/[\\/]+$/, "");
                      const normalizedRootPath = String(rootPath || "").replace(/[\\/]+$/, "");
                      const isCurrentRepo =
                        !group.repoPath ||
                        group.repoName === t("session.currentProject") ||
                        normalizedRepoPath === normalizedRootPath;
                      const showGroupHeader = group.repoKind === "plain" || group.head || !isCurrentRepo;
                      return (
                        <div
                          key={group.key}
                          style={{
                            display: "flex",
                            flexDirection: "column",
                            gap: "4px",
                          }}
                        >
                          {showGroupHeader ? (
                            <div
                              title={[group.repoPath, group.head].filter(Boolean).join(" · ") || group.repoName || t("session.currentProject")}
                              style={{
                                padding: "2px 6px 0",
                                fontSize: "11px",
                                color: "var(--text-secondary)",
                                fontFamily: group.head
                                  ? "var(--mono-font, monospace)"
                                  : undefined,
                              }}
                            >
                              {group.repoKind === "plain"
                                ? `${group.repoName || t("session.currentProject")} · ${t("session.nonGit")}`
                                : group.head
                                  ? isCurrentRepo
                                    ? `HEAD ${group.head.slice(0, 8)}`
                                    : `${group.repoName || t("session.currentProject")} · HEAD ${group.head.slice(0, 8)}`
                                  : group.repoName || t("session.currentProject")}
                            </div>
                          ) : null}
                          {group.files.map((file) => {
                            const stats =
                              relatedFileStatsByKey[relatedFileStatKey(file)] ||
                              gitFileStatsByPath[file.path];
                            return (
                          <div
                            key={`${file.head || "legacy"}:${file.path}`}
                            style={{
                              display: "flex",
                              alignItems: "center",
                              gap: "6px",
                            }}
                          >
                            <div
                              onClick={() => onFileClickRef.current?.(file)}
                              style={{
                                display: "flex",
                                alignItems: "center",
                                gap: "8px",
                                flex: 1,
                                minWidth: 0,
                                padding: "3px 6px",
                                borderRadius: "6px",
                                cursor: "pointer",
                                transition: "background 0.15s",
                              }}
                              onMouseEnter={(e) => {
                                e.currentTarget.style.background =
                                  "rgba(0,0,0,0.04)";
                              }}
                              onMouseLeave={(e) => {
                                e.currentTarget.style.background =
                                  "transparent";
                              }}
                            >
                              <svg
                                xmlns="http://www.w3.org/2000/svg"
                                width="13"
                                height="13"
                                viewBox="0 0 24 24"
                                fill="none"
                                stroke="#94a3b8"
                                strokeWidth="2"
                                strokeLinecap="round"
                                strokeLinejoin="round"
                                aria-hidden="true"
                                style={{ flexShrink: 0 }}
                              >
                                <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
                                <polyline points="14 2 14 8 20 8" />
                                <line x1="16" x2="8" y1="13" y2="13" />
                                <line x1="16" x2="8" y1="17" y2="17" />
                                <line x1="10" x2="8" y1="9" y2="9" />
                              </svg>
                              <div
                                style={{
                                  flex: 1,
                                  minWidth: 0,
                                  fontSize: "12px",
                                  color: "var(--text-primary)",
                                  overflow: "hidden",
                                  textOverflow: "ellipsis",
                                  whiteSpace: "nowrap",
                                }}
                              >
                                {file.name}
                              </div>
                              {stats ? (
                                <div
                                  style={{
                                    display: "inline-flex",
                                    alignItems: "center",
                                    gap: "8px",
                                    fontSize: "11px",
                                    color: "var(--text-secondary)",
                                    flexShrink: 0,
                                  }}
                                >
                                  <span
                                    style={{
                                      color: "#15803d",
                                      fontVariantNumeric: "tabular-nums",
                                    }}
                                  >
                                    +{stats.additions}
                                  </span>
                                  <span
                                    style={{
                                      color: "#b91c1c",
                                      fontVariantNumeric: "tabular-nums",
                                    }}
                                  >
                                    -{stats.deletions}
                                  </span>
                                </div>
                              ) : null}
                            </div>
                            <button
                              type="button"
                              aria-label={t("session.removeRelatedFile", { name: file.name || file.path })}
                              onClick={(event) => {
                                event.stopPropagation();
                                onRemoveRelatedFile?.(
                                  file.path,
                                  file.head,
                                  file.repo_path,
                                  file.repo_kind,
                                );
                              }}
                              style={{
                                border: "none",
                                background: "transparent",
                                color: "#dc2626",
                                cursor: "pointer",
                                fontSize: "14px",
                                lineHeight: 1,
                                padding: "2px 4px",
                                borderRadius: "4px",
                                flexShrink: 0,
                              }}
                            >
                              x
                            </button>
                          </div>
                            );
                          })}
                        </div>
                      );
                    })}
                  </div>
                ) : null}
              </div>
            )}
            <div ref={scrollEndRef} style={{ height: "1px" }} />
          </div>
          </div>
        </div>
        {userMessageSummaries.length > 0 || showJumpToLatest ? (
          <div
            style={{
              position: "absolute",
              right: "16px",
              bottom: `${16 + composerOverlayInset}px`,
              zIndex: 4,
              display: "flex",
              alignItems: "center",
              gap: "6px",
            }}
          >
            {showJumpToLatest ? (
              <button
                type="button"
                onClick={() => {
                  cancelTargetSeqScroll();
                  if (targetSeq) {
                    targetSeqScrollKeyRef.current = `${sessionKey || ""}:${targetSeq}:${targetSeqRequestKey}`;
                  }
                  shouldStickToBottomRef.current = true;
                  setShowJumpToLatest(false);
                  stickSessionToBottom("smooth");
                }}
                aria-label={t("session.jumpLatest")}
                title={t("session.jumpLatest")}
                style={{
                  display: "inline-flex",
                  alignItems: "center",
                  gap: "6px",
                  height: "34px",
                  border: `1px solid color-mix(in srgb, ${themeColor} 35%, transparent)`,
                  background: themeColor,
                  color: "#ffffff",
                  borderRadius: "999px",
                  padding: "0 12px",
                  boxShadow: "0 8px 24px rgba(15, 23, 42, 0.14)",
                  cursor: "pointer",
                  fontSize: "12px",
                  whiteSpace: "nowrap",
                }}
              >
                <span>{t("session.jumpBottom")}</span>
              </button>
            ) : null}
            {userMessageSummaries.length > 0 ? (
              <div
                ref={userSummaryRootRef}
                onMouseEnter={() => {
                  refreshCurrentUserMessageIndex();
                  setUserSummaryHoverOpen(true);
                }}
                onMouseLeave={() => setUserSummaryHoverOpen(false)}
                style={{
                  position: "relative",
                  display: "inline-flex",
                }}
              >
                {userSummaryOpen ? (
                  <>
                    <div
                      aria-hidden="true"
                      style={{
                        position: "absolute",
                        right: 0,
                        bottom: "100%",
                        width: "min(320px, calc(100vw - 72px))",
                        height: "8px",
                      }}
                    />
                    <div
                      role="dialog"
                      aria-label={t("session.userSummary")}
                      style={{
                        position: "absolute",
                        right: 0,
                        bottom: "calc(100% + 8px)",
                        width: "min(320px, calc(100vw - 72px))",
                        maxHeight: "260px",
                        overflowY: "auto",
                        padding: "6px",
                        borderRadius: "8px",
                        border: "1px solid var(--menu-border)",
                        background: "var(--menu-bg)",
                        boxShadow: "0 16px 34px rgba(15, 23, 42, 0.18)",
                        color: "var(--text-primary)",
                        boxSizing: "border-box",
                      }}
                    >
                      {windowMeta.total > 0 ? (
                        <div
                          style={{
                            fontSize: "11px",
                            lineHeight: "16px",
                            color: "var(--text-secondary)",
                            padding: "2px 8px 6px",
                            borderBottom: "1px solid var(--menu-border)",
                          }}
                        >
                          已加载 {visibleExchanges.length} / 共 {windowMeta.total}
                        </div>
                      ) : null}
                      <div
                        ref={userSummaryListRef}
                        style={{ display: "flex", flexDirection: "column", gap: "2px" }}
                      >
                        {userMessageSummaries.map((item) => {
                          const isCurrent = item.index === currentUserMessageIndex;
                          return (
                            <button
                              key={item.id}
                              type="button"
                              data-user-summary-index={item.index}
                              onClick={() => scrollToUserMessageSummary(item.index)}
                              style={{
                                width: "100%",
                                border: "none",
                                background: isCurrent
                                  ? "rgba(148, 163, 184, 0.22)"
                                  : "transparent",
                                display: "block",
                                padding: "6px 8px",
                                borderRadius: "6px",
                                cursor: "pointer",
                                textAlign: "left",
                                color: "var(--text-primary)",
                              }}
                              onMouseEnter={(event) => {
                                event.currentTarget.style.background = "var(--menu-active-bg)";
                              }}
                              onMouseLeave={(event) => {
                                event.currentTarget.style.background = isCurrent
                                  ? "rgba(148, 163, 184, 0.22)"
                                  : "transparent";
                              }}
                            >
                              <span
                                title={item.summary}
                                style={{
                                  display: "block",
                                  minWidth: 0,
                                  fontSize: "12px",
                                  lineHeight: "18px",
                                  color: "var(--text-primary)",
                                  overflow: "hidden",
                                  textOverflow: "ellipsis",
                                  whiteSpace: "nowrap",
                                }}
                              >
                                {item.summary}
                              </span>
                            </button>
                          );
                        })}
                      </div>
                    </div>
                  </>
                ) : null}
                <button
                  type="button"
                  onClick={() => {
                    refreshCurrentUserMessageIndex();
                    setUserSummaryPinnedOpen((open) => {
                      const nextOpen = !open;
                      if (!nextOpen) {
                        setUserSummaryHoverOpen(false);
                      }
                      return nextOpen;
                    });
                  }}
                  aria-label={userSummaryOpen ? t("session.hideUserSummary") : t("session.showUserSummary")}
                  title={userSummaryOpen ? t("session.hideUserSummary") : t("session.showUserSummary")}
                  style={{
                    position: "relative",
                    width: "34px",
                    height: "34px",
                    border: userSummaryOpen ? `1px solid ${themeMuted}` : `1px solid ${themeMutedBorder}`,
                    borderRadius: "8px",
                    background: userSummaryOpen ? themeMuted : "var(--menu-bg)",
                    color: userSummaryOpen ? "#ffffff" : themeMuted,
                    boxShadow: "0 10px 24px rgba(15, 23, 42, 0.16)",
                    display: "inline-flex",
                    alignItems: "center",
                    justifyContent: "center",
                    cursor: "pointer",
                    fontSize: "18px",
                  }}
                >
                  <UserMessageListIcon />
                  <span
                    aria-hidden="true"
                    style={{
                      position: "absolute",
                      top: "-7px",
                      right: "-7px",
                      minWidth: "18px",
                      height: "18px",
                      padding: "0 5px",
                      borderRadius: "999px",
                      background: themeColor,
                      color: "#ffffff",
                      border: "2px solid var(--menu-bg)",
                      fontSize: "10px",
                      fontWeight: 800,
                      lineHeight: "14px",
                      display: "inline-flex",
                      alignItems: "center",
                      justifyContent: "center",
                      boxSizing: "border-box",
                      fontVariantNumeric: "tabular-nums",
                    }}
                  >
                    {userMessageSummaries.length > 99 ? "99+" : userMessageSummaries.length}
                  </span>
                </button>
              </div>
            ) : null}
          </div>
        ) : null}
      </div>
      <style>{`
        @keyframes pulse {
          0%, 100% { opacity: 1; }
          50% { opacity: 0.5; }
        }
        @keyframes sessionLoadingPulse {
          0% { background-position: 100% 0; opacity: 0.7; }
          50% { opacity: 1; }
          100% { background-position: -100% 0; opacity: 0.7; }
        }
        @keyframes spin {
          to { transform: rotate(360deg); }
        }
      `}</style>
    </div>
  );
}

export const SessionViewer = memo(
  SessionViewerInner,
  (prev, next) =>
    prev.session === next.session &&
    prev.loading === next.loading &&
    prev.rootId === next.rootId &&
    prev.rootPath === next.rootPath &&
    prev.interactionMode === next.interactionMode &&
    prev.targetSeq === next.targetSeq &&
    prev.targetSeqRequestKey === next.targetSeqRequestKey &&
    prev.composerOverlayInset === next.composerOverlayInset &&
    prev.gitFileStatsByPath === next.gitFileStatsByPath &&
    prev.onRootClick === next.onRootClick,
);
