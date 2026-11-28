import React from "react";
import type { MessageKey } from "../../i18n/index";
import { ForkIcon } from "./AskUserQuestionCard";

/**
 * 一轮**已完整结束**的 agent 回复的控件行：复制 / fork / 完成时间。
 *
 * 只放「作用于这一轮」的控件；模型、上下文、缓存命中属于会话的持续状态，放在同行的
 * 右侧常驻状态条（`SessionViewerStatusBar`），不随每轮重复。agent 身份也在状态条里，
 * 这里不再重复画图标。
 *
 * 位置由 `SessionViewerBottomBar` 的左侧槽位决定：运行中/子代理运行中时整条隐藏，
 * 让位给等待提示；同一时刻只显示最新一个已完成轮次的控件。
 */
export type SessionViewerTurnActionsProps = {
  time: string;
  durationLabel: string;
  copied: boolean;
  t: (key: MessageKey) => string;
  themeColor: string;
  userMetaButtonStyle: React.CSSProperties;
  /** 距槽位上沿的外边距；底部栏传 0，由行容器统一控制间距。 */
  marginTop: string;
  onCopy: () => void;
  onFork?: () => void;
};

export function SessionViewerTurnActions({
  time,
  durationLabel,
  copied,
  t,
  themeColor,
  userMetaButtonStyle,
  marginTop,
  onCopy,
  onFork,
}: SessionViewerTurnActionsProps) {
  return (
    <span
      data-testid="turn-actions"
      style={{
        alignSelf: "center",
        display: "inline-flex",
        flexWrap: "nowrap",
        alignItems: "center",
        gap: "8px",
        maxWidth: "100%",
        minWidth: 0,
        fontSize: "10px",
        color: "var(--text-secondary)",
        opacity: 0.5,
        marginTop,
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
          onClick={onCopy}
          style={userMetaButtonStyle}
          aria-label={t("session.copyMarkdown")}
          title={t("session.copyMarkdown")}
        >
          {copied ? (
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
        {onFork ? (
          <button
            type="button"
            onClick={onFork}
            style={userMetaButtonStyle}
            aria-label={t("session.forkFromMessage")}
            title={t("session.forkFromMessage")}
          >
            <ForkIcon />
          </button>
        ) : null}
      </span>
      <span
        data-testid="turn-meta-time"
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
        {time}
        {durationLabel ? ` ${durationLabel}` : ""}
      </span>
    </span>
  );
}