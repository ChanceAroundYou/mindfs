import React, { useEffect, useState } from "react";
import { useI18n } from "../../i18n/index";
import type { ToolCall } from "../../shared/sessionTypes";
import type { AskUserQuestionItem, SessionViewerProps } from "./sessionViewerTypes";

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

export function AskUserIcon() {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      width="16"
      height="16"
      viewBox="0 0 16 16"
      aria-hidden="true"
      style={{ color: "#ef4444", flexShrink: 0 }}
    >
      <g fill="currentColor">
        <path d="M8 11a.75.75 0 1 1 0 1.5a.75.75 0 0 1 0-1.5m0-7c1.262 0 2.25.988 2.25 2.25c0 1.083-.566 1.648-1.021 2.104c-.408.407-.729.728-.729 1.396a.5.5 0 0 1-1 0c0-1.083.566-1.648 1.021-2.104c.408-.407.729-.728.729-1.396C9.25 5.538 8.712 5 8 5s-1.25.538-1.25 1.25a.5.5 0 0 1-1 0C5.75 4.988 6.738 4 8 4" />
        <path
          fillRule="evenodd"
          d="M8 1a7 7 0 0 1 6.999 7.001a7 7 0 0 1-10.504 6.06l-2.728.91a.582.582 0 0 1-.744-.714l.83-2.906A7 7 0 0 1 8 1m.001 1.001c-3.308 0-6 2.692-6 6c0 1.003.252 1.996.73 2.871l.196.36l-.726 2.54l1.978-.659l.428-.143l.39.226A6 6 0 0 0 8 14l.001.001c3.308 0 6-2.692 6-6s-2.692-6-6-6"
          clipRule="evenodd"
        />
      </g>
    </svg>
  );
}

export function ForkIcon() {
  return (
    <svg
      width="14"
      height="14"
      viewBox="0 0 24 24"
      xmlns="http://www.w3.org/2000/svg"
      aria-hidden="true"
      style={{ transform: "rotate(180deg) scaleX(-1)" }}
    >
      <path d="M0 0h24v24H0z" fill="none" />
      <path
        fill="none"
        stroke="currentColor"
        strokeLinecap="round"
        strokeLinejoin="round"
        strokeWidth="1.5"
        d="M17 7a2 2 0 1 0 0-4a2 2 0 0 0 0 4M7 7a2 2 0 1 0 0-4a2 2 0 0 0 0 4m0 14a2 2 0 1 0 0-4a2 2 0 0 0 0 4M7 7v10M17 7v1c0 2.5-2 3-2 3l-6 2s-2 .5-2 3v1"
      />
      <circle cx="17" cy="5" r="2" fill="currentColor" />
    </svg>
  );
}

export function AskUserQuestionCard({
  toolCall,
  rootId,
  sessionKey,
  agent,
  active,
  onAnswer,
}: {
  toolCall: ToolCall;
  rootId?: string | null;
  sessionKey?: string | null;
  agent?: string;
  active: boolean;
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
  const firstQuestionTitle = askUserQuestionTitle(questions[0]);
  const title =
    firstQuestionTitle ||
    `${toolCall.title || ""}`.trim() ||
    (typeof toolCall.meta?.title === "string" ? toolCall.meta.title : "") ||
    "ask user";
  const canSubmit =
    !!rootId &&
    !!sessionKey &&
    !!toolUseId &&
    !!onAnswer &&
    questions.length > 0 &&
    questions.every((_, index) => (answers[`q_${index}`] || "").trim() !== "") &&
    !submitting &&
    !submitted;

  useEffect(() => {
    if (hasPersistedAnswers) {
      setAnswers(persistedAnswers);
      setSubmitted(true);
      setExpanded(false);
      return;
    }
    setAnswers({});
    setSubmitted(false);
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
        border: "1px solid rgba(239, 68, 68, 0.28)",
        background:
          "linear-gradient(180deg, rgba(239, 68, 68, 0.08), rgba(239, 68, 68, 0.03))",
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
          background: "rgba(239, 68, 68, 0.04)",
          border: "none",
          borderBottom: expanded ? "1px solid var(--border-color)" : "none",
          cursor: "pointer",
          fontSize: "12px",
          gap: "6px",
          minWidth: 0,
        }}
        title={title}
      >
        <AskUserIcon />
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
                            cursor: submitted ? "default" : "pointer",
                          }}
                        >
                          <input
                            type={question.multiSelect ? "checkbox" : "radio"}
                            name={`${toolUseId}-${key}`}
                            checked={checked}
                            disabled={submitted}
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
                      disabled={submitted}
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
                    disabled={submitted}
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
            if (!canSubmit || !rootId || !sessionKey || !toolUseId || !onAnswer) return;
            setSubmitting(true);
            try {
              await onAnswer({ rootId, sessionKey, agent, toolUseId, answers });
              setSubmitted(true);
              setExpanded(false);
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
        </div>
      ) : null}
    </div>
  );
}
