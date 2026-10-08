import type { TimelineItem } from "../../hooks/useSessionStream";
import { getAskUserQuestions } from "./AskUserQuestionCard";

export function findActiveAskUserCallId(timeline: TimelineItem[], isAwaiting: boolean): string {
  if (!isAwaiting) return "";
  for (let i = timeline.length - 1; i >= 0; i -= 1) {
    const item = timeline[i];
    if (item.type === "user_text" || item.type === "assistant_text") return "";
    if (item.type !== "tool") continue;
    const toolCall = item.toolCall || {};
    const kind = `${toolCall.kind || ""}`.toLowerCase();
    const status = `${toolCall.status || ""}`.toLowerCase();
    if (kind !== "ask_user" || !["running", "pending", "in_progress"].includes(status)) continue;
    if (getAskUserQuestions(toolCall).length === 0) continue;
    return toolCall.callId || (typeof toolCall.meta?.toolUseId === "string" ? toolCall.meta.toolUseId : "");
  }
  return "";
}
