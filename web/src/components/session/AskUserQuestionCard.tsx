import React, { useEffect, useState } from "react";
import { useI18n } from "../../i18n/index";
import type { ToolCall } from "../../shared/sessionTypes";
import type { AskUserQuestionItem, SessionViewerProps } from "./sessionViewerTypes";
import { askUserAnswerSummary, askUserPalette, resolveAskUserState } from "./askUserCardState";
import { AskUserIcon } from "./askUserIcons";

// 图标仍从这个模块导出：会话页脚只关心 ForkIcon，不该多记一个文件路径。
export { AskUserIcon, ForkIcon } from "./askUserIcons";

export function getAskUserQuestions(toolCall: Partial<ToolCall>): AskUserQuestionItem[] {
  const raw = toolCall.meta?.questions;
  if (Array.isArray(raw)) {
    return raw as AskUserQuestionItem[];
  }
  const input = toolCall.meta?.input;
  if (typeof input !== "string" || input.trim() === "") {
    return [];
  }
  try {
    const parsed = JSON.parse(input) as { questions?: AskUserQuestionItem[] };
    return Array.isArray(parsed.questions) ? parsed.questions : [];
  } catch {
    return [];
  }
}

export function getAskUserAnswers(toolCall: Partial<ToolCall>): Record<string, string> {
  const raw = toolCall.meta?.answers;
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) {
    return {};
  }
  const answers: Record<string, string> = {};
  for (const [key, value] of Object.entries(raw as Record<string, unknown>)) {
    const cleanKey = `${key || ""}`.trim();
    const cleanValue = `${value || ""}`.trim();
    if (cleanKey && cleanValue) {
      answers[cleanKey] = cleanValue;
    }
  }
  return answers;
}

export function askUserQuestionTitle(question?: AskUserQuestionItem): string {
  const questionText = `${question?.question || ""}`.trim();
  const header = `${question?.header || ""}`.trim();
  if (header && questionText) {
    return `${header}：${questionText}`;
  }
  return questionText || header;
}

export function AskUserQuestionCard({
  toolCall,
  rootId,
  sessionKey,
  agent,
  active,
  statusNote,
  onAnswer,
}: {
  toolCall: ToolCall;
  rootId?: string | null;
  sessionKey?: string | null;
  agent?: string;
  active: boolean;
  /** 卡被收口时的原因（回合结束、被新提问取代等）；只用于「已关闭」的说明行。 */
  statusNote?: string;
  onAnswer?: SessionViewerProps["onAskUserAnswer"];
}) {
  const { t } = useI18n();
  const questions = getAskUserQuestions(toolCall);
  const [focusedCustomAnswerKey, setFocusedCustomAnswerKey] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const status = `${toolCall.status || ""}`.toLowerCase();
  const isCurrent =
    status === "running" || status === "pending" || status === "in_progress";
  const [expanded, setExpanded] = useState(isCurrent);
  const toolUseId =
    toolCall.callId ||
    (typeof toolCall.meta?.toolUseId === "string" ? toolCall.meta.toolUseId : "");
  const persistedAnswers = getAskUserAnswers(toolCall);
  const persistedAnswersKey = JSON.stringify(persistedAnswers);
  const hasPersistedAnswers = Object.keys(persistedAnswers).length > 0;
  const [answers, setAnswers] = useState<Record<string, string>>(persistedAnswers);
  const [submitted, setSubmitted] = useState(hasPersistedAnswers);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const firstQuestionTitle = askUserQuestionTitle(questions[0]);
  const title =
    firstQuestionTitle ||
    `${toolCall.title || ""}`.trim() ||
    (typeof toolCall.meta?.title === "string" ? toolCall.meta.title : "") ||
    "ask user";
  // 任务里的 Router 提问卡没有会话与 tool call：答案要落成一条用户留言，
  // 由任务面板自己回传（payload 里 sessionKey/toolUseId 为空，调用方据此分流）。
  // 有会话的卡仍然必须带齐 session 与 tool id —— 少了其中之一说明数据坏了，
  // 不能当成 Router 卡放行。
  const sessionBound = !!sessionKey;
  const routerCard = !sessionBound && !toolUseId;
  const canSubmit =
    !!rootId &&
    (sessionBound ? !!toolUseId : routerCard) &&
    !!onAnswer &&
    questions.length > 0 &&
    questions.every((_, index) => (answers[`q_${index}`] || "").trim() !== "") &&
    !submitted &&
    active &&
    !submitting;

  // 三种状态用同一套视觉语言：等待=红、已答复=绿、已关闭=灰。Router 卡与会话卡
  // 共用这个组件，任务详情和会话查看器因此不会各显示一套。
  const askState = resolveAskUserState({
    submitted,
    hasPersistedAnswers,
    active,
    isCurrent,
  });
  const askPalette = askUserPalette(askState);
  // 已收口的卡不能再改答案：允许编辑却提交不了，比直接锁住更让人困惑。
  const locked = submitted || !active;
  const answerSummary = askUserAnswerSummary(questions, answers);

  useEffect(() => {
    if (hasPersistedAnswers) {
      setAnswers(persistedAnswers);
      setSubmitted(true);
      setExpanded(false);
      setSubmitError(null);
      return;
    }
    setAnswers({});
    setSubmitted(false);
    setSubmitError(null);
  }, [toolUseId, persistedAnswersKey, hasPersistedAnswers]);

  useEffect(() => {
    if (submitted) {
      setExpanded(false);
      return;
    }
    setExpanded(active && isCurrent);
  }, [active, isCurrent, submitted, toolUseId]);

  const setAnswer = (index: number, value: string) => {
    setAnswers((prev) => ({ ...prev, [`q_${index}`]: value }));
  };
  const toggleMultiAnswer = (index: number, label: string, validLabels: string[]) => {
    const key = `q_${index}`;
    setAnswers((prev) => {
      const current = (prev[key] || "")
        .split(",")
        .map((item) => item.trim())
        .filter((item) => item && validLabels.includes(item));
      const next = current.includes(label)
        ? current.filter((item) => item !== label)
        : [...current, label];
      return { ...prev, [key]: next.join(", ") };
    });
  };

  return (
    <div
      style={{
        width: "100%",
        minWidth: 0,
        borderRadius: "10px",
        border: `1px solid ${askPalette.border}`,
        background: askPalette.background,
        overflow: "hidden",
      }}
    >
      <button
        type="button"
        onClick={() => setExpanded((value) => !value)}
        style={{
          width: "100%",
          display: "flex",
          alignItems: "center",
          justifyContent: "flex-start",
          padding: "6px 8px",
          background: askPalette.header,
          border: "none",
          borderBottom: expanded ? "1px solid var(--border-color)" : "none",
          cursor: "pointer",
          fontSize: "12px",
          gap: "6px",
          minWidth: 0,
        }}
        title={title}
      >
        <AskUserIcon color={askPalette.accent} />
        <span
          style={{
            minWidth: 0,
            flex: 1,
            fontWeight: 500,
            color: "var(--text-primary)",
            whiteSpace: expanded ? "normal" : "nowrap",
            overflow: expanded ? "visible" : "hidden",
            textOverflow: expanded ? "clip" : "ellipsis",
            textAlign: "left",
            overflowWrap: "anywhere",
            lineHeight: 1.35,
          }}
        >
          {title}
        </span>
        <span
          data-testid="ask-user-state"
          data-state={askState}
          style={{
            flexShrink: 0,
            borderRadius: "999px",
            padding: "1px 8px",
            fontSize: "11px",
            fontWeight: 600,
            color: askPalette.accent,
            background: askPalette.chip,
            whiteSpace: "nowrap",
          }}
        >
          {t(`task.askState.${askState}`)}
        </span>
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
      {!expanded && askState === "answered" && answerSummary ? (
        <div
          data-testid="ask-user-answer-summary"
          title={answerSummary}
          style={{
            padding: "0 10px 7px",
            fontSize: "11px",
            color: "var(--text-secondary)",
            whiteSpace: "nowrap",
            overflow: "hidden",
            textOverflow: "ellipsis",
          }}
        >
          {t("task.askState.answerSummary", { answer: answerSummary })}
        </div>
      ) : null}
      {askState === "closed" && `${statusNote || ""}`.trim() ? (
        <div
          data-testid="ask-user-status-note"
          style={{
            padding: "0 10px 7px",
            fontSize: "11px",
            color: "var(--text-secondary)",
            whiteSpace: "nowrap",
            overflow: "hidden",
            textOverflow: "ellipsis",
          }}
        >
          {statusNote}
        </div>
      ) : null}
      {expanded ? (
        <div style={{ padding: "10px", display: "flex", flexDirection: "column", gap: "12px" }}>
          {questions.map((question, index) => {
            const key = `q_${index}`;
            const options = Array.isArray(question.options) ? question.options : [];
            const selected = answers[key] || "";
            const optionLabels = options
              .map((option) => `${option.label || ""}`.trim())
              .filter(Boolean);
            const selectedSet = new Set(
              selected
                .split(",")
                .map((item) => item.trim())
                .filter(Boolean),
            );
            const hasOptionSelection = question.multiSelect
              ? optionLabels.some((label) => selectedSet.has(label))
              : optionLabels.includes(selected);
            const customAnswer = hasOptionSelection ? "" : selected;
            const customAnswerActive =
              customAnswer.trim() !== "" || focusedCustomAnswerKey === key;
            const itemTitle = askUserQuestionTitle(question);
            return (
              <div key={key} style={{ display: "flex", flexDirection: "column", gap: "8px" }}>
                {itemTitle ? (
                  <div
                    style={{
                      fontSize: "13px",
                      fontWeight: 700,
                      color: "var(--text-primary)",
                      lineHeight: 1.45,
                      overflowWrap: "anywhere",
                    }}
                  >
                    {itemTitle}
                  </div>
                ) : null}
                {options.length > 0 ? (
                  <div style={{ display: "flex", flexDirection: "column", gap: "6px" }}>
                    {options.map((option) => {
                      const label = `${option.label || ""}`.trim();
                      if (!label) return null;
                      const checked = question.multiSelect
                        ? selectedSet.has(label)
                        : selected === label;
                      return (
                        <label
                          key={label}
                          style={{
                            display: "flex",
                            gap: "8px",
                            alignItems: "flex-start",
                            padding: "7px 8px",
                            borderRadius: "8px",
                            border: checked
                              ? "1px solid rgba(239, 68, 68, 0.42)"
                              : "1px solid var(--border-color)",
                            background: checked ? "rgba(239, 68, 68, 0.10)" : "var(--content-bg)",
                            cursor: locked ? "default" : "pointer",
                          }}
                        >
                          <input
                            type={question.multiSelect ? "checkbox" : "radio"}
                            name={`${toolUseId}-${key}`}
                            checked={checked}
                            disabled={locked}
                            onChange={() =>
                              question.multiSelect
                                ? toggleMultiAnswer(index, label, optionLabels)
                                : setAnswer(index, label)
                            }
                            style={{ marginTop: "2px" }}
                          />
                          <span style={{ display: "flex", flexDirection: "column", gap: "2px" }}>
                            <span style={{ fontSize: "13px", color: "var(--text-primary)" }}>{label}</span>
                            {option.description ? (
                              <span style={{ fontSize: "12px", color: "var(--text-secondary)" }}>
                                {option.description}
                              </span>
                            ) : null}
                          </span>
                        </label>
                      );
                    })}
                    <textarea
                      value={customAnswer}
                      disabled={locked}
                      onFocus={() => setFocusedCustomAnswerKey(key)}
                      onBlur={() => setFocusedCustomAnswerKey((current) => (current === key ? "" : current))}
                      onChange={(event) => setAnswer(index, event.target.value)}
                      placeholder={t("session.askCustomAnswer")}
                      rows={2}
                      style={{
                        width: "100%",
                        resize: "vertical",
                        borderRadius: "8px",
                        border: customAnswerActive
                          ? "1px solid rgba(239, 68, 68, 0.42)"
                          : "1px solid var(--border-color)",
                        outline: "none",
                        background: "var(--content-bg)",
                        color: "var(--text-primary)",
                        padding: "8px",
                        fontSize: "13px",
                        boxSizing: "border-box",
                      }}
                    />
                  </div>
                ) : (
                  <textarea
                    value={selected}
                    disabled={locked}
                    onChange={(event) => setAnswer(index, event.target.value)}
                    placeholder={t("session.askAnswer")}
                    rows={3}
                    style={{
                      width: "100%",
                      resize: "vertical",
                      borderRadius: "8px",
                      border: "1px solid var(--border-color)",
                      background: "var(--content-bg)",
                      color: "var(--text-primary)",
                      padding: "8px",
                      fontSize: "13px",
                      boxSizing: "border-box",
                    }}
                  />
              )}
            </div>
          );
        })}
        <button
          type="button"
          disabled={!canSubmit}
          onClick={async () => {
            if (!canSubmit || !rootId || !onAnswer) return;
            setSubmitting(true);
            setSubmitError(null);
            try {
              // Router 卡没有会话目标，两个 id 传空串：调用方按「都为空」分流到
              // 任务留言端点，而不是往一个不存在的会话里回 tool_result。
              await onAnswer({
                rootId,
                sessionKey: sessionKey || "",
                agent,
                toolUseId: toolUseId || "",
                answers,
              });
              setSubmitted(true);
              setExpanded(false);
            } catch (err) {
              // 失败时保持未提交，让用户可以直接重试；错误必须可见，不能静默吞掉。
              setSubmitError(err instanceof Error ? err.message : String(err));
            } finally {
              setSubmitting(false);
            }
          }}
          style={{
            alignSelf: "flex-start",
            border: "none",
            borderRadius: "999px",
            padding: "6px 12px",
            background: canSubmit ? "#ef4444" : "var(--border-color)",
            color: canSubmit ? "#fff" : "var(--text-secondary)",
            fontSize: "12px",
            fontWeight: 700,
            cursor: canSubmit ? "pointer" : "default",
          }}
        >
          {submitted ? t("session.askSubmitted") : submitting ? t("session.askSubmitting") : t("session.askSubmit")}
        </button>
        {submitError && (
          <div data-testid="ask-user-submit-error" style={{ fontSize: 12, color: "var(--status-bad, #dc2626)" }}>
            {submitError}
          </div>
        )}
        </div>
      ) : null}
    </div>
  );
}
