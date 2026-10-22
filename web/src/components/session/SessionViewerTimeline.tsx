import type { Dispatch, MutableRefObject, SetStateAction } from "react";
import type { TimelineItem } from "../../hooks/useSessionStream";
import type { AgentStatus } from "../../shared/agentTypes";
import type { RelatedFile } from "../../shared/sessionTypes";
import type { Locale, MessageKey } from "../../i18n/index";
import { useReportError } from "../../hooks/useReportError";
import { useSessionViewerTimelineActions } from "./useSessionViewerTimelineActions";
import { copyText } from "../../platform/clipboard";
import { formatSessionDuration } from "../../shared/sessionDuration";
import { SessionViewerThoughtItem } from "./SessionViewerThoughtItem";
import { renderToolTimelineItem } from "./sessionViewerToolTimelineHelpers";
import { renderAuxiliaryTimelineItem } from "./sessionViewerAuxiliaryTimelineHelpers";
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
  /**
   * 虚拟化回调（CUSTOM(G-JT)）。给出时只渲染视口内的条目，屏外条目由上下占位高
   * 代替；不给则全量渲染（少条目路径与既有的源码级测试都走这条）。
   */
  virtual?: SessionViewerTimelineVirtual | null;
};

export type SessionViewerTimelineVirtual = {
  items: Array<{ index: number; key: React.Key; start: number }>;
  totalSize: number;
  measureElement: (node: Element | null) => void;
};

export function SessionViewerTimeline({
  timeline, timelineItemMeta, locale, isStreaming, rootId, rootPath, sessionKey,
  sessionAgent, activeAskUserCallId, onAskUserAnswer, agents, onFileClickRef,
  onEditUserMessage, onForkAgentMessage, savedPromptKeys, copiedMessageKeys,
  copyResetTimersRef, setSavedPromptKeys, setCopiedMessageKeys, t, themeColor,
  userMetaButtonStyle, virtual = null,
}: SessionViewerTimelineProps) {
  const reportError = useReportError();
  const { isPersistedSeq, savePrompt } = useSessionViewerTimelineActions();
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
        {isUser ? <SessionViewerUserMessage {...{ item, time, spacing, timelineItemKey, rootId, rootPath, displayContent, promptSaveContent, promptKey, promptSaved: !!savedPromptKeys[promptKey], copySucceeded: !!copiedMessageKeys[promptKey], imageAttachments, userMessageWidth, hasRichUserAttachments: imageAttachments.length > 0, onFileClickRef, onEditUserMessage, savePrompt, copyText, reportError, setSavedPromptKeys, setCopiedMessageKeys, copyResetTimersRef, pendingAck: item.pendingAck, t, themeColor, userMetaButtonStyle }} /> : <SessionViewerAssistantMessage {...{ item, time, spacing, timelineItemKey, rootId, rootPath, onFileClickRef, t, themeColor, userMetaButtonStyle, hideAssistantMeta, assistantMarkdownContent, promptKey, promptSaved: !!savedPromptKeys[promptKey], copySucceeded: !!copiedMessageKeys[promptKey], canForkAgentMessage, onForkAgentMessage, assistantExchangeMeta, assistantDurationLabel, tokenUsage: item.tokenUsage, contextWindow: item.contextWindow, copyText, reportError, setCopiedMessageKeys, copyResetTimersRef }} />}
      </div>
    );
  };

  if (!virtual) {
    return <>{timeline.map((item, idx) => renderTimelineItem(item, idx, timelineItemSpacing(idx > 0 ? timeline[idx - 1] : null, item)))}</>;
  }

  // 虚拟化路径：容器撑到总高度，只挂载视口内的条目，按 start 绝对定位。
  // `overflowAnchor: none` 是必须的 —— 浏览器默认的滚动锚定会在条目高度被测量
  // 修正时反向补偿 scrollTop，与本库自身的 scrollAdjustments 叠加后表现为抖动。
  return (
    <div style={{ position: "relative", width: "100%", height: virtual.totalSize, overflowAnchor: "none" }}>
      {virtual.items.map((row) => {
        const item = timeline[row.index];
        if (!item) return null;
        return (
          <div
            key={row.key}
            data-timeline-index={row.index}
            ref={virtual.measureElement}
            style={{
              position: "absolute",
              top: 0,
              left: 0,
              width: "100%",
              transform: `translateY(${row.start}px)`,
              display: "flex",
              flexDirection: "column",
            }}
          >
            {renderTimelineItem(item, row.index, timelineItemSpacing(row.index > 0 ? timeline[row.index - 1] : null, item))}
          </div>
        );
      })}
    </div>
  );
}
