import React from "react";
import type { TurnMeta } from "../../hooks/sessionBlocks";
import type { MessageKey } from "../../i18n/index";
import { AgentIcon } from "../agent/AgentIcon";
import { ForkIcon } from "./AskUserQuestionCard";

/**
 * 一轮**已完整结束**的 agent 回复的控件行：复制 / fork / agent 图标 / 完成时间。
 *
 * 只放「作用于这一轮」的控件；模型、上下文、缓存命中属于会话的持续状态，放在消息
 * 列表尾部的常驻状态条（`SessionViewerStatusBar`），不随每轮重复。
 *
 * 可见性由调用方按 `meta.completed`（服务端已持久化）与轮次末行位置判定 —— 不依赖
 * 客户端的流式/轮询状态，运行中不会闪。
 */
export type SessionViewerTurnActionsProps = {
  meta: TurnMeta;
  time: string;
  durationLabel: string;
  copied: boolean;
  t: (key: MessageKey) => string;
  themeColor: string;
  userMetaButtonStyle: React.CSSProperties;
  /** 距上一行的上外边距：文本流收紧、卡片收尾必须为正（见 turnFooterMarginTop）。 */
  marginTop: string;
  onCopy: () => void;
  onFork?: () => void;
};

export function SessionViewerTurnActions({
  meta,
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
        <AgentIcon
          agentName={meta.agent || ""}
          style={{ width: "12px", height: "12px", flexShrink: 0 }}
        />
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