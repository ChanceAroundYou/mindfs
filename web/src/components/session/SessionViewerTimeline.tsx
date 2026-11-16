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
import type { ToolCallViewCache } from "../stream/useToolCallCard";
import { renderAuxiliaryTimelineItem } from "./sessionViewerAuxiliaryTimelineHelpers";
import { SessionViewerUserMessage } from "./SessionViewerUserMessage";
import { SessionViewerAssistantMessage } from "./SessionViewerAssistantMessage";
import { SessionViewerTurnMeta } from "./SessionViewerTurnMeta";
import {
  extractUploadAttachments,
  formatAssistantExchangeMeta,
  formatTime,
  isAssistantFlowEnd,
  stripImageAttachmentTokens,
  stripUploadAttachmentTokens,
  timelineItemSpacing,
  turnFooterMarginTop,
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
  /**
   * 工具卡展开态与已拉详情（CUSTOM(G-AT)）。按会话存活，虚拟化卸载/重挂时恢复原状。
   */
  toolViewCache?: ToolCallViewCache;
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
  userMetaButtonStyle, virtual = null, toolViewCache,
}: SessionViewerTimelineProps) {
  const reportError = useReportError();
  const { savePrompt } = useSessionViewerTimelineActions();
  const makePromptKey = (item: Extract<TimelineItem, { type: "user_text" | "assistant_text" }>) =>
    `${item.id}\n${item.timestamp || ""}\n${item.content || ""}`;

  const renderTimelineItem = (item: TimelineItem, idx: number, spacing = "0") => {
    const timelineItemKey = item.id || `${item.type}-${idx}`;
    const isUser = item.type === "user_text";
    const itemMeta = timelineItemMeta[idx];
    // 页脚属于**整轮**：只在该轮最后一行之后画。工具/思考收尾、纯 reasoning 收尾
    // （正文被过滤）都能拿到页脚；一轮还在流式输出时不画（与旧行为一致）。
    const next = idx + 1 < timeline.length ? timeline[idx + 1] : null;
    const endsAssistantFlow = isAssistantFlowEnd(item, next);
    const turnMeta = item.turnMeta;
    const showFooter =
      endsAssistantFlow &&
      !!turnMeta &&
      !(isStreaming && idx === timeline.length - 1);
    const footer = showFooter && turnMeta ? renderTurnFooter(item, turnMeta, itemMeta.prevUserTs) : null;

    if (item.type === "thought") {
      return <>{<SessionViewerThoughtItem item={item} timelineItemKey={timelineItemKey} spacing={spacing} />}{footer}</>;
    }
    if (item.type === "tool") {
      return <>{renderToolTimelineItem({ item, timelineItemKey, spacing, rootId, rootPath, sessionKey, sessionAgent, activeAskUserCallId, onAskUserAnswer, toolViewCache })}{footer}</>;
    }
    if (item.type === "tool_group" || item.type === "todo" || item.type === "plan" || item.type === "compact") {
      return <>{renderAuxiliaryTimelineItem({ item, timelineItemKey, spacing, rootId, rootPath, sessionKey, toolViewCache })}{footer}</>;
    }
    if (item.type === "turn_meta") {
      // 整轮没有任何可渲染内容时的纯页脚行（例如只有纯标点 reasoning）。
      if (!footer) return null;
      return (
        <div style={{ marginTop: spacing, alignSelf: "flex-start", display: "flex", flexDirection: "column", width: "100%" }}>
          {footer}
        </div>
      );
    }
    const userMessageIndex = isUser ? itemMeta.userIndex : undefined;
    const time = formatTime(item.timestamp, locale);
    const uploadAttachments = isUser ? extractUploadAttachments(item.content || "") : [];
    const imageAttachments = uploadAttachments.filter((attachment) => attachment.isImage);
    const displayContent = isUser ? stripImageAttachmentTokens(item.content || "") : item.content || "";
    const promptSaveContent = isUser ? stripUploadAttachmentTokens(item.content || "") : "";
    const promptKey = makePromptKey(item);
    const userMessageWidth = imageAttachments.length > 0 ? "min(320px, 100%)" : "auto";
    return (
      <div key={timelineItemKey} data-session-seq={item.seq || undefined} data-user-message-index={userMessageIndex} style={{ marginTop: spacing, alignSelf: isUser ? "flex-end" : "flex-start", width: isUser ? userMessageWidth : "100%", maxWidth: isUser ? "80%" : "100%", minWidth: 0, position: "relative", display: "flex", flexDirection: "column" }}>
        {isUser ? <SessionViewerUserMessage {...{ item, time, spacing, timelineItemKey, rootId, rootPath, displayContent, promptSaveContent, promptKey, promptSaved: !!savedPromptKeys[promptKey], copySucceeded: !!copiedMessageKeys[promptKey], imageAttachments, userMessageWidth, hasRichUserAttachments: imageAttachments.length > 0, onFileClickRef, onEditUserMessage, savePrompt, copyText, reportError, setSavedPromptKeys, setCopiedMessageKeys, copyResetTimersRef, pendingAck: item.pendingAck, t, themeColor, userMetaButtonStyle }} /> : <SessionViewerAssistantMessage {...{ item, rootId, onFileClickRef }} />}
        {footer}
      </div>
    );
  };

  /**
   * 一轮的页脚。复制 / fork 的身份用「条目 id + 时间戳」，不再依赖 assistant_text
   * 的 promptKey —— 页脚行本身可能没有正文。
   */
  const renderTurnFooter = (item: TimelineItem, meta: NonNullable<TimelineItem["turnMeta"]>, prevUserTs: string | undefined) => {
    const footerKey = `${item.id}\n${meta.timestamp || ""}`;
    const copied = !!copiedMessageKeys[footerKey];
    const onCopy = () => {
      if (!meta.content) {
        reportError("clipboard.write_failed", t("session.emptyMessageCannotCopy"));
        return;
      }
      void copyText(meta.content)
        .then(() => {
          setCopiedMessageKeys((prev: Record<string, true>) => ({ ...prev, [footerKey]: true }));
          if (copyResetTimersRef.current[footerKey]) {
            window.clearTimeout(copyResetTimersRef.current[footerKey]);
          }
          copyResetTimersRef.current[footerKey] = window.setTimeout(() => {
            setCopiedMessageKeys((prev: Record<string, true>) => {
              const next = { ...prev };
              delete next[footerKey];
              return next;
            });
            delete copyResetTimersRef.current[footerKey];
          }, 1000);
        })
        .catch((err: unknown) => {
          reportError(
            "clipboard.write_failed",
            String((err as Error)?.message || t("session.copyFailed")),
          );
        });
    };
    const seq = Number(meta.seq || 0);
    const onFork =
      seq > 0 && onForkAgentMessage
        ? () => {
            void onForkAgentMessage(seq);
          }
        : undefined;
    return (
      <SessionViewerTurnMeta
        meta={meta}
        time={formatTime(meta.timestamp, locale)}
        durationLabel={formatSessionDuration(prevUserTs, meta.timestamp)}
        exchangeMeta={formatAssistantExchangeMeta(item, agents)}
        copied={copied}
        t={t}
        themeColor={themeColor}
        userMetaButtonStyle={userMetaButtonStyle}
        marginTop={turnFooterMarginTop(item)}
        onCopy={onCopy}
        onFork={onFork}
      />
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
            data-index={row.index}
            data-timeline-key={String(row.key)}
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
