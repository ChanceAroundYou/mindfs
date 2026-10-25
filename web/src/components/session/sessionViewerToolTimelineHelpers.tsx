import React from "react";
import { ToolCallCard } from "../stream/ToolCallCard";
import { AskUserQuestionCard, getAskUserQuestions } from "./AskUserQuestionCard";
import { formatToolCallFallbackResult } from "./sessionViewerFormat";
import type { TimelineItem } from "../../hooks/useSessionStream";
import type { SessionViewerProps } from "./sessionViewerTypes";

export function renderToolTimelineItem({ item, timelineItemKey, spacing, rootId, rootPath, sessionKey, sessionAgent, activeAskUserCallId, onAskUserAnswer }: { item: Extract<TimelineItem, { type: "tool" }>; timelineItemKey: string; spacing: string; rootId?: string | null; rootPath?: string | null; sessionKey: string | null; sessionAgent?: string; activeAskUserCallId: string; onAskUserAnswer?: SessionViewerProps["onAskUserAnswer"] }): React.ReactNode {
if (item.type === "tool") {
  const tc = item.toolCall || {};
  const kind = `${tc.kind || ""}`.toLowerCase();
  const hasTitle = Boolean((tc as any).title || (tc.meta && typeof tc.meta.title === "string" && tc.meta.title));
  const hasContent = Array.isArray(tc.content) && tc.content.length > 0;
  const hasLocations = Array.isArray(tc.locations) && tc.locations.length > 0;
  const hasCommand = typeof tc.meta?.command === "string" && String(tc.meta.command).trim() !== "" ||
    typeof tc.meta?.input === "string" && String(tc.meta.input).trim() !== "";
  if ((kind === "bash" || kind === "execute") && !hasContent && !hasLocations && !hasCommand && !hasTitle) {
    return null;
  }
  // 无输出也无命令的 bash 空壳：标题仅 "Bash"/"bash" 也算空壳
  if ((kind === "bash" || kind === "execute") && !hasContent && !hasLocations && !hasCommand && (!hasTitle || /^bash$/i.test(String((tc as any).title || tc.meta?.title || "").trim()))) {
    return null;
  }
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
          agent={sessionAgent}
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
}}
