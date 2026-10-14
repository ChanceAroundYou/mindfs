import type { Dispatch, MutableRefObject, SetStateAction } from "react";
import type { TimelineItem } from "../../hooks/useSessionStream";
import type { AgentStatus } from "../../shared/agentTypes";
import type { RelatedFile } from "../../shared/sessionTypes";
import type { Locale, MessageKey } from "../../i18n/index";
import { isPersistedSeq } from "../../services/session";
import { savePrompt } from "../../services/prompts";
import { reportError } from "../../services/net/error";
import { copyText } from "../../platform/clipboard";
import { formatSessionDuration } from "../../services/session/sessionDuration";
import { SessionViewerThoughtItem } from "./SessionViewerThoughtItem";
import { renderToolTimelineItem } from "./SessionViewerToolItem";
import { renderAuxiliaryTimelineItem } from "./SessionViewerAuxiliaryTimeline";
import { SessionViewerUserMessage } from "./SessionViewerUserMessage";
import { SessionViewerAssistantMessage } from "./SessionViewerAssistantMessage";
import {
  extractUploadAttachments,
  formatAssistantExchangeMeta,
  formatTime,
  stripImageAttachmentTokens,
  stripUploadAttachmentTokens,
  timelineItemSpacing,
} from "./sessionViewerFormat";

export type SessionViewerTimelineProps = {
  timeline: TimelineItem[];
  timelineItemMeta: Array<{ userIndex: number; prevUserTs: string | undefined }>;
  locale: Locale;
  isStreaming: boolean;
  rootId?: string | null;
  rootPath?: string | null;
  sessionKey: string | null;
  sessionAgent?: string;
  activeAskUserCallId: string;
  onAskUserAnswer?: (input: any) => void | Promise<void>;
  agents?: AgentStatus[];
  onFileClickRef: MutableRefObject<((file: RelatedFile & { name?: string }) => void) | undefined>;
  onEditUserMessage?: (content: string) => void;
  onForkAgentMessage?: (seq: number) => void | Promise<void>;
  savedPromptKeys: Record<string, true>;
  copiedMessageKeys: Record<string, true>;
  copyResetTimersRef: MutableRefObject<Record<string, number>>;
  setSavedPromptKeys: Dispatch<SetStateAction<Record<string, true>>>;
  setCopiedMessageKeys: Dispatch<SetStateAction<Record<string, true>>>;
  t: (key: MessageKey) => string;
  themeColor: string;
  userMetaButtonStyle: React.CSSProperties;
};

export function SessionViewerTimeline({
  timeline, timelineItemMeta, locale, isStreaming, rootId, rootPath, sessionKey,
  sessionAgent, activeAskUserCallId, onAskUserAnswer, agents, onFileClickRef,
  onEditUserMessage, onForkAgentMessage, savedPromptKeys, copiedMessageKeys,
  copyResetTimersRef, setSavedPromptKeys, setCopiedMessageKeys, t, themeColor,
  userMetaButtonStyle,
}: SessionViewerTimelineProps) {
  const makePromptKey = (item: Extract<TimelineItem, { type: "user_text" | "assistant_text" }>) =>
    `${item.id}\n${item.timestamp || ""}\n${item.content || ""}`;

  const renderTimelineItem = (item: TimelineItem, idx: number, spacing = "0") => {
    const timelineItemKey = item.id || `${item.type}-${idx}`;
    if (item.type === "thought") {
      return <SessionViewerThoughtItem item={item} timelineItemKey={timelineItemKey} spacing={spacing} />;
    }
    if (item.type === "tool") {
      return renderToolTimelineItem({ item, timelineItemKey, spacing, rootId, rootPath, sessionKey, sessionAgent, activeAskUserCallId, onAskUserAnswer });
    }
    if (item.type === "tool_group" || item.type === "todo" || item.type === "plan" || item.type === "compact") {
      return renderAuxiliaryTimelineItem({ item, timelineItemKey, spacing, rootId, rootPath, sessionKey });
    }
    const isUser = item.type === "user_text";
    const itemMeta = timelineItemMeta[idx];
    const userMessageIndex = isUser ? itemMeta.userIndex : undefined;
    const next = idx + 1 < timeline.length ? timeline[idx + 1] : null;
    const hasFollowingAssistantFlow = !isUser && !!next && next.type !== "user_text";
    const hideAssistantMeta = !isUser && (hasFollowingAssistantFlow || (isStreaming && idx === timeline.length - 1));
    const time = formatTime(item.timestamp, locale);
    const uploadAttachments = isUser ? extractUploadAttachments(item.content || "") : [];
    const imageAttachments = uploadAttachments.filter((attachment) => attachment.isImage);
    const displayContent = isUser ? stripImageAttachmentTokens(item.content || "") : item.content || "";
    const promptSaveContent = isUser ? stripUploadAttachmentTokens(item.content || "") : "";
    const promptKey = makePromptKey(item);
    const userMessageWidth = imageAttachments.length > 0 ? "min(320px, 100%)" : "auto";
    const assistantMarkdownContent = !isUser ? (item.content || "").trim() : "";
    const assistantExchangeMeta = !isUser ? formatAssistantExchangeMeta(item, agents) : "";
    const assistantDurationLabel = !isUser ? formatSessionDuration(itemMeta.prevUserTs, item.timestamp) : "";
    const canForkAgentMessage = !isUser && isPersistedSeq(item.seq) && !!onForkAgentMessage;
    return (
      <div key={timelineItemKey} data-session-seq={item.seq || undefined} data-user-message-index={userMessageIndex} style={{ marginTop: spacing, alignSelf: isUser ? "flex-end" : "flex-start", width: isUser ? userMessageWidth : "100%", maxWidth: isUser ? "80%" : "100%", minWidth: 0, position: "relative", display: "flex", flexDirection: "column" }}>
        {isUser ? <SessionViewerUserMessage {...{ item, time, spacing, timelineItemKey, rootId, rootPath, displayContent, promptSaveContent, promptKey, promptSaved: !!savedPromptKeys[promptKey], copySucceeded: !!copiedMessageKeys[promptKey], imageAttachments, userMessageWidth, hasRichUserAttachments: imageAttachments.length > 0, onFileClickRef, onEditUserMessage, savePrompt, copyText, reportError, setSavedPromptKeys, setCopiedMessageKeys, copyResetTimersRef, pendingAck: item.pendingAck, t, themeColor, userMetaButtonStyle }} /> : <SessionViewerAssistantMessage {...{ item, time, spacing, timelineItemKey, rootId, rootPath, onFileClickRef, t, themeColor, userMetaButtonStyle, hideAssistantMeta, assistantMarkdownContent, canForkAgentMessage, onForkAgentMessage, assistantExchangeMeta, assistantDurationLabel, tokenUsage: item.tokenUsage, contextWindow: item.contextWindow }} />}
      </div>
    );
  };

  return <>{timeline.map((item, idx) => renderTimelineItem(item, idx, timelineItemSpacing(idx > 0 ? timeline[idx - 1] : null, item)))}</>;
}
