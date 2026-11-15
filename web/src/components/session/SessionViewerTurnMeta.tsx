import React from "react";
import type { TurnMeta } from "../../hooks/sessionBlocks";
import type { MessageKey } from "../../i18n/index";
import { AgentIcon } from "../agent/AgentIcon";
import { ForkIcon } from "./AskUserQuestionCard";
import { ContextWindowBadge, TurnTokenUsage } from "./SessionViewerTokenViews";

/**
 * 一轮 agent 回复的页脚：复制 / fork / agent 图标 / 模型·effort / 用量 / 时间 / 上下文。
 *
 * 为什么从 `SessionViewerAssistantMessage` 里拆出来：页脚属于**整轮**，不属于某一段
 * 正文。一轮可能以工具卡或思考卡收尾（CLI 常见），也可能整轮没有正文（纯标点
 * reasoning 被过滤），只把页脚挂在 assistant_text 上时这些轮次就没有页脚。
 * 现在由时间线在「该轮最后一行」统一渲染这一行。
 */
export type SessionViewerTurnMetaProps = {
  meta: TurnMeta;
  time: string;
  durationLabel: string;
  exchangeMeta: string;
  copied: boolean;
  t: (key: MessageKey) => string;
  themeColor: string;
  userMetaButtonStyle: React.CSSProperties;
  onCopy: () => void;
  onFork?: () => void;
};

export function SessionViewerTurnMeta({
  meta,
  time,
  durationLabel,
  exchangeMeta,
  copied,
  t,
  themeColor,
  userMetaButtonStyle,
  onCopy,
  onFork,
}: SessionViewerTurnMetaProps) {
  return (
    <span
      data-testid="turn-meta"
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
        {exchangeMeta ? (
          <span
            data-testid="turn-meta-model"
            style={{
              display: "inline-flex",
              alignItems: "center",
              minHeight: "16px",
              minWidth: 0,
              maxWidth: "100%",
              overflowWrap: "anywhere",
            }}
          >
            {exchangeMeta}
          </span>
        ) : null}
        <span
          data-testid="turn-meta-usage"
          style={{
            display: "inline-flex",
            alignItems: "center",
            minHeight: "16px",
            minWidth: 0,
            maxWidth: "100%",
            overflowWrap: "anywhere",
          }}
        >
          <TurnTokenUsage usage={meta.tokenUsage} />
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
        <span
          data-testid="turn-meta-context"
          style={{
            display: "inline-flex",
            alignItems: "center",
            minHeight: "16px",
            minWidth: 0,
            maxWidth: "100%",
            overflowWrap: "anywhere",
          }}
        >
          <ContextWindowBadge contextWindow={meta.contextWindow} />
        </span>
      </span>
    </span>
  );
}