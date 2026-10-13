import React from "react";
import { AttachmentImage } from "./SessionViewerAttachments";
import { InlineTokenText } from "../common/InlineTokenText";
import { renderToolIcon } from "../stream/toolCallIcons";
import { TurnTokenUsage, ContextWindowBadge } from "./SessionViewerTokenViews";
import { AgentIcon } from "../agent/AgentIcon";
import { ForkIcon } from "./AskUserQuestionCard";
import { MarkdownViewer } from "../common/MarkdownViewer";

export function SessionViewerAssistantMessage(props: any) {
  const {
  item, time, spacing, timelineItemKey, rootId, rootPath, displayContent, promptSaveContent,
  promptKey, promptSaved, copySucceeded, imageAttachments, userMessageWidth, hasRichUserAttachments,
  onFileClickRef, onEditUserMessage, savePrompt, copyText, reportError, setSavedPromptKeys,
  setCopiedMessageKeys, copyResetTimersRef, pendingAck, t, themeColor, userMetaButtonStyle,
  hideAssistantMeta, assistantMarkdownContent, canForkAgentMessage, onForkAgentMessage,
  assistantExchangeMeta, assistantDurationLabel, tokenUsage, contextWindow,
  } = props;
  return (
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
                setCopiedMessageKeys((prev: Record<string, true>) => ({
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
                    setCopiedMessageKeys((prev: Record<string, true>) => {
                      const next = { ...prev };
                      delete next[promptKey];
                      return next;
                    });
                    delete copyResetTimersRef.current[promptKey];
                  }, 1000);
              })
              .catch((err: unknown) => {
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
  );
}
