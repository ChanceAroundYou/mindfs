import React, { memo, useState } from "react";
import { ToolCallCard, renderToolIcon } from "./ToolCallCard";
import { useI18n } from "../../i18n";
import type { MessageKey } from "../../i18n";
import type { ToolCall } from "../../services/session";

/**
 * 连续同类工具卡的折叠组卡（2026-10-07）。
 *
 * 为什么存在：agent 一个回合里连开 100+ 个 edit/read/execute 是常态，逐个渲染
 * ToolCallCard 是「打开会话卡」的主要渲染成本（实测单会话 389 张、单 seq 189 张）。
 * useSessionStream 的 groupConsecutiveToolCalls 把连续同类、数量 >= 5 的卡折成
 * 一个 tool_group 项，这里渲染成一张组卡；展开才逐个渲染真实的 ToolCallCard。
 *
 * 折叠态不丢信息：edit/read/execute 的折叠卡片本来就只有图标+标题+状态，
 * 组卡把它们汇总成「N 个编辑」+ 运行中/失败计数，信息量等价。
 */
type ToolCallGroupCardProps = {
  kind: string;
  toolCalls: ToolCall[];
  rootPath?: string;
  rootId?: string | null;
  sessionKey?: string | null;
  getFallbackResult?: (toolCall: Partial<ToolCall>) => string;
};

function kindLabelKey(kind: string): MessageKey | null {
  switch (kind) {
    case "edit":
      return "session.toolKindEdit";
    case "read":
      return "session.toolKindRead";
    case "execute":
      return "session.toolKindExecute";
    default:
      return null;
  }
}

function summarizeStatus(toolCalls: ToolCall[]): { running: number; failed: number } {
  let running = 0;
  let failed = 0;
  for (const toolCall of toolCalls) {
    const status = `${toolCall.status || ""}`.toLowerCase();
    if (status === "running" || status === "pending" || status === "in_progress") {
      running += 1;
    } else if (status === "failed" || status === "error") {
      failed += 1;
    }
  }
  return { running, failed };
}

export const ToolCallGroupCard = memo(function ToolCallGroupCard({
  kind,
  toolCalls,
  rootPath,
  rootId,
  sessionKey,
  getFallbackResult,
}: ToolCallGroupCardProps) {
  const { t } = useI18n();
  const [expanded, setExpanded] = useState(false);
  const normalizedKind = (kind || "").toLowerCase();
  const labelKey = kindLabelKey(normalizedKind);
  const kindLabel = labelKey ? t(labelKey) : normalizedKind || "tool";
  const { running, failed } = summarizeStatus(toolCalls);

  return (
    <div
      style={{
        border: "1px solid color-mix(in srgb, var(--accent-color) 12%, transparent)",
        borderRadius: "6px",
        overflow: "hidden",
        background: "color-mix(in srgb, var(--accent-color) 2%, transparent)",
      }}
    >
      <button
        type="button"
        onClick={() => setExpanded((prev) => !prev)}
        aria-expanded={expanded}
        title={expanded ? t("common.collapse") : t("common.expand")}
        style={{
          display: "flex",
          alignItems: "center",
          gap: "6px",
          width: "100%",
          padding: "6px 8px",
          background: "none",
          border: "none",
          cursor: "pointer",
          fontSize: "12px",
          textAlign: "left",
        }}
      >
        <span style={{ display: "inline-flex", flexShrink: 0 }}>{renderToolIcon(normalizedKind)}</span>
        <span
          style={{
            fontWeight: 500,
            color: "var(--text-primary)",
            flex: 1,
            minWidth: 0,
            whiteSpace: "nowrap",
            overflow: "hidden",
            textOverflow: "ellipsis",
          }}
        >
          {t("session.toolGroupCount", { count: toolCalls.length, kind: kindLabel })}
        </span>
        {running > 0 && (
          <span
            style={{
              width: "6px",
              height: "6px",
              borderRadius: "50%",
              background: "#f59e0b",
              animation: "pulse 1s infinite",
              flexShrink: 0,
            }}
          />
        )}
        {failed > 0 && (
          <span style={{ color: "#ef4444", fontSize: "12px", lineHeight: 1, flexShrink: 0 }}>
            ✕{failed > 1 ? failed : ""}
          </span>
        )}
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
      {expanded && (
        <div style={{ display: "flex", flexDirection: "column", gap: "2px", padding: "0 4px 4px" }}>
          {toolCalls.map((toolCall, index) => (
            <ToolCallCard
              key={toolCall.callId || `${normalizedKind}-${index}`}
              kind={toolCall.kind}
              title={
                (toolCall as { title?: string }).title ||
                (toolCall.meta && typeof toolCall.meta.title === "string"
                  ? (toolCall.meta.title as string)
                  : "")
              }
              callId={toolCall.callId || ""}
              status={toolCall.status || "running"}
              content={toolCall.content}
              result={getFallbackResult ? getFallbackResult(toolCall) : undefined}
              locations={toolCall.locations}
              meta={toolCall.meta}
              rootPath={rootPath}
              rootId={rootId}
              sessionKey={sessionKey}
            />
          ))}
        </div>
      )}
    </div>
  );
});
