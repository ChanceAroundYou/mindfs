import React from "react";
import { AttachmentImage } from "./AttachmentImage";
import { InlineTokenText } from "../common/InlineTokenText";
import { renderToolIcon } from "../stream/toolCallIcons";
import { TurnTokenUsage, ContextWindowBadge } from "./SessionViewerTokenViews";
import { AgentIcon } from "../agent/AgentIcon";
import { ForkIcon } from "./AskUserQuestionCard";
import { MarkdownViewer } from "../common/MarkdownViewer";

export function SessionViewerUserMessage(props: any) {
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
          {imageAttachments.map((attachment: { path: string; name: string; isImage: boolean }) => (
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
              setSavedPromptKeys((prev: Record<string, true>) => ({
                ...prev,
                [promptKey]: true,
              }));
            })
            .catch((err: unknown) => {
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
          setCopiedMessageKeys((prev: Record<string, true>) => ({
            ...prev,
            [promptKey]: true,
          }));
          if (copyResetTimersRef.current[promptKey]) {
            window.clearTimeout(copyResetTimersRef.current[promptKey]);
          }
          copyResetTimersRef.current[promptKey] = window.setTimeout(() => {
            setCopiedMessageKeys((prev: Record<string, true>) => {
              const next = { ...prev };
              delete next[promptKey];
              return next;
            });
            delete copyResetTimersRef.current[promptKey];
          }, 1000);
        };
        void copyText(promptSaveContent)
          .then(markCopied)
          .catch((err: unknown) => {
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
  );
}
