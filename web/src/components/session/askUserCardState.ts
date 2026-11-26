import type { AskUserQuestionItem } from "./sessionViewerTypes";

export type AskUserState = "waiting" | "answered" | "closed";

export type AskUserPalette = {
  border: string;
  background: string;
  header: string;
  accent: string;
  chip: string;
};

/**
 * 提问卡的三种状态共用一个判据：答过就是 answered（即便卡已经收口），还在等
 * 且是当前卡就是 waiting，其余（被取代、回合结束、失败）都是 closed。
 */
export function resolveAskUserState(input: {
  submitted: boolean;
  hasPersistedAnswers: boolean;
  active: boolean;
  isCurrent: boolean;
}): AskUserState {
  const answered = input.submitted || input.hasPersistedAnswers;
  if (answered) return "answered";
  return input.active && input.isCurrent ? "waiting" : "closed";
}

// 等待=红、已答复=绿、已关闭=灰。Router 卡与会话卡共用这份配色，任务详情和
// 会话查看器才不会是两套视觉语言。
export function askUserPalette(state: AskUserState): AskUserPalette {
  if (state === "waiting") {
    return {
      border: "rgba(239, 68, 68, 0.28)",
      background:
        "linear-gradient(180deg, rgba(239, 68, 68, 0.08), rgba(239, 68, 68, 0.03))",
      header: "rgba(239, 68, 68, 0.04)",
      accent: "#ef4444",
      chip: "rgba(239, 68, 68, 0.12)",
    };
  }
  if (state === "answered") {
    return {
      border: "rgba(34, 197, 94, 0.32)",
      background:
        "linear-gradient(180deg, rgba(34, 197, 94, 0.07), rgba(34, 197, 94, 0.02))",
      header: "rgba(34, 197, 94, 0.05)",
      accent: "#16a34a",
      chip: "rgba(34, 197, 94, 0.12)",
    };
  }
  return {
    border: "var(--border-color)",
    background: "var(--bg-secondary, transparent)",
    header: "transparent",
    accent: "var(--text-secondary)",
    chip: "var(--bg-tertiary, rgba(148, 163, 184, 0.12))",
  };
}

/** 折叠态的一行答案摘要；q_i 比索引更可靠（用户可能换过题），最后退回全部值。 */
export function askUserAnswerSummary(
  questions: AskUserQuestionItem[],
  answers: Record<string, string>,
): string {
  return (
    questions
      .map((_, index) => `${answers[`q_${index}`] || ""}`.trim())
      .filter(Boolean)
      .join(" · ") ||
    Object.values(answers)
      .map((value) => `${value || ""}`.trim())
      .filter(Boolean)
      .join(" · ")
  );
}