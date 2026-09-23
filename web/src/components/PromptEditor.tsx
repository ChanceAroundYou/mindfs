import React, { forwardRef, useEffect, useImperativeHandle, useRef } from "react";
import { AgentSelector } from "./AgentSelector";
import { has1MSuffix } from "./ActionBar";
import TokenEditor, { type TokenEditorHandle } from "./editor/TokenEditor";
import type { StageRole } from "../services/tasks";
import type { AgentStatus } from "../services/agents";
import { useI18n } from "../i18n";

export type PromptEditorMode = "editable" | "readonly" | "done";

export type PromptEditorProps = {
  value: string;
  onChange?: (value: string) => void;
  mode?: PromptEditorMode;
  /** 该值变化时把 value 灌入编辑器一次（例如切换编辑对象） */
  resetKey?: string | number;
  placeholder?: string;
  role?: StageRole;
  agents?: AgentStatus[];
  agent?: string;
  model?: string;
  effort?: string;
  mode2?: string;
  onAgentChange?: (agent: string, model?: string) => void;
  onModeChange?: (mode?: string) => void;
  onEffortChange?: (effort?: string) => void;
  onLongContextChange?: (enabled: boolean) => void;
  canAttach?: boolean;
  onAttach?: () => void;
  onEdit?: () => void;
  onSend?: () => void;
  sending?: boolean;
  sendDisabled?: boolean;
  saveLabel?: string;
  savingLabel?: string;
};

/**
 * PromptEditor —— 简化版 TokenEditor：
 * 三态 = 可编辑 / 只读 / 已执行（只读 + 灰底 + 铅笔灰不可点）。
 * 保留 token chip 渲染（[file: x] 显示为 chip）。
 */
export const PromptEditor = forwardRef<TokenEditorHandle, PromptEditorProps>(function PromptEditor(
  {
    value,
    onChange,
    mode = "editable",
    resetKey,
    placeholder,
    role = "agent",
    agents = [],
    agent = "codex",
    model = "",
    effort = "",
    mode2 = "",
    onAgentChange,
    onModeChange,
    onEffortChange,
    onLongContextChange,
    canAttach,
    onAttach,
    onEdit,
    onSend,
    sending,
    sendDisabled,
    saveLabel,
    savingLabel,
  },
  ref
) {
  const { t } = useI18n();
  const editorRef = useRef<TokenEditorHandle | null>(null);
  const editable = mode === "editable";
  const isUser = role === "user";

  useImperativeHandle(ref, () => ({
    focus: () => editorRef.current?.focus(),
    blur: () => editorRef.current?.blur(),
    getHeight: () => editorRef.current?.getHeight() || 0,
    clear: () => editorRef.current?.clear(),
    setText: (text: string) => editorRef.current?.setText(text),
    insertCandidate: (type, val) => editorRef.current?.insertCandidate(type, val),
  }));

  // resetKey 或 mode 变化（如切换编辑对象 / 进出编辑态）时灌入 value
  useEffect(() => {
    editorRef.current?.setText(value || "");
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [resetKey, mode]);

  const noopAgent = () => {};
  const noopMode = () => {};

  const containerBg = mode === "done"
    ? "rgba(148, 163, 184, 0.10)"
    : "var(--input-bg)";

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "6px" }}>
      <div
        style={{
          position: "relative",
          border: "1px solid var(--border-color)",
          borderRadius: "8px",
          background: containerBg,
          overflow: "auto",
          minHeight: "44px",
        }}
      >
        <TokenEditor
          ref={editorRef}
          placeholder={placeholder || t("taskTemplate.promptTemplate")}
          disabled={sending}
          readOnly={!editable}
          isDark={false}
          rightInset={canAttach ? 48 : 14}
          topInset={0}
          bottomInset={12}
          fillHeight
          onChange={(payload) => onChange?.(payload.serializedText)}
        />
        {/* 已执行态：整块压暗表达不可用 */}
        {mode === "done" ? (
          <div style={{ position: "absolute", inset: 0, background: "rgba(148,163,184,0.06)", borderRadius: "8px", pointerEvents: "none" }} />
        ) : null}
      </div>
      <div style={{ display: "flex", alignItems: "center", gap: "6px" }}>
        {!isUser ? (
          <AgentSelector
            agent={agent}
            model={model}
            mode={mode2}
            effort={effort}
            fastService=""
            longContext={has1MSuffix(model || "")}
            onLongContextChange={editable ? (onLongContextChange || noopMode) : noopMode}
            agents={agents}
            compact
            menuPlacement="top"
            showChevron
            onAgentChange={editable ? (onAgentChange || noopAgent) : noopAgent}
            onModeChange={editable ? (onModeChange || noopMode) : noopMode}
            onEffortChange={editable ? (onEffortChange || noopMode) : noopMode}
            onFastServiceChange={() => {}}
          />
        ) : null}
        <div style={{ marginLeft: "auto", display: "flex", gap: "4px", alignItems: "center" }}>
          {canAttach ? (
            <button
              type="button"
              title={t("task.addAttachment")}
              aria-label={t("task.addAttachment")}
              disabled={!editable || sending}
              onClick={onAttach}
              style={roundButtonStyle(editable)}
            >
              <PlusIcon />
            </button>
          ) : null}
          {editable ? (
            <button
              type="button"
              disabled={sending || sendDisabled}
              onClick={onSend}
              style={{
                height: "28px",
                borderRadius: "6px",
                border: "1px solid var(--accent-color)",
                background: "var(--accent-color)",
                color: "#fff",
                padding: "0 12px",
                fontSize: "12px",
                fontWeight: 800,
                cursor: sending || sendDisabled ? "not-allowed" : "pointer",
                whiteSpace: "nowrap",
                opacity: sending || sendDisabled ? 0.6 : 1,
              }}
            >
              {sending ? (savingLabel || t("common.saving")) : (saveLabel || t("common.save"))}
            </button>
          ) : (
            <button
              type="button"
              title={t("common.edit")}
              aria-label={t("common.edit")}
              disabled={mode === "done" || sending}
              onClick={onEdit}
              style={roundButtonStyle(mode !== "done" && !!onEdit, mode === "done")}
            >
              <PencilIcon />
            </button>
          )}
        </div>
      </div>
    </div>
  );
});

function roundButtonStyle(active: boolean, dimmed = false): React.CSSProperties {
  return {
    height: "28px",
    minWidth: "28px",
    border: "none",
    borderRadius: "6px",
    background: active ? "var(--button-bg)" : "transparent",
    color: dimmed ? "var(--text-secondary)" : active ? "var(--accent-color)" : "var(--text-secondary)",
    display: "inline-flex",
    alignItems: "center",
    justifyContent: "center",
    cursor: active && !dimmed ? "pointer" : "not-allowed",
    padding: "0 6px",
    opacity: dimmed ? 0.4 : 1,
  };
}

export function PencilIcon() {
  return (
    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M12 20h9" />
      <path d="M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4Z" />
    </svg>
  );
}

function PlusIcon() {
  return (
    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" aria-hidden="true">
      <path d="M12 5v14" />
      <path d="M5 12h14" />
    </svg>
  );
}
