import React, { forwardRef, useEffect, useImperativeHandle, useRef } from "react";
import { AgentSelector } from "./AgentSelector";
import TokenEditor, { type TokenEditorHandle } from "./editor/TokenEditor";
import { PencilIcon, PlusIcon, SendIcon, composerContainerStyle, composerIconButtonStyle, composerSendButtonStyle } from "./composerStyles";
import type { StageRole } from "../services/tasks";
import type { AgentStatus } from "../services/agents";
import { useI18n } from "../i18n";

export type PromptEditorMode = "editable" | "readonly" | "done";

export type PromptEditorProps = {
  value: string;
  onChange?: (value: string) => void;
  mode?: PromptEditorMode;
  /** 该值变化时把 value 灌入编辑器一次（例如切换编辑对象 / 进出编辑态） */
  resetKey?: string | number;
  placeholder?: string;
  role?: StageRole;
  agents?: AgentStatus[];
  agent?: string;
  model?: string;
  effort?: string;
  agentMode?: string;
  onAgentChange?: (agent: string, model?: string) => void;
  onModeChange?: (mode?: string) => void;
  onEffortChange?: (effort?: string) => void;
  onLongContextChange?: (enabled: boolean) => void;
  onRestartAgent?: (agent: string) => void | Promise<void>;
  canAttach?: boolean;
  onAttach?: () => void;
  onEdit?: () => void;
  onSend?: () => void;
  sending?: boolean;
  sendDisabled?: boolean;
};

/**
 * PromptEditor —— 任务侧的 prompt 编辑控件。
 * 与对话输入框（ActionBar）共用 composerStyles 的容器与按钮样式，只保留「模型 + 新增文件 + 发送」。
 * 三态：editable / readonly / done（done = readonly + 灰底 + 铅笔灰不可点）。
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
    agentMode = "",
    onAgentChange,
    onModeChange,
    onEffortChange,
    onLongContextChange,
    onRestartAgent,
    canAttach,
    onAttach,
    onEdit,
    onSend,
    sending,
    sendDisabled,
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

  // resetKey 或 mode 变化（切换编辑对象 / 进出编辑态）时灌入 value
  useEffect(() => {
    editorRef.current?.setText(value || "");
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [resetKey, mode]);

  const noop = () => {};
  const canSend = editable && !sending && !sendDisabled;

  return (
    <div style={composerContainerStyle(mode === "done" ? "rgba(148, 163, 184, 0.10)" : "var(--input-bg)")}>
      <TokenEditor
        ref={editorRef}
        placeholder={placeholder || t("taskTemplate.promptTemplate")}
        disabled={sending}
        readOnly={!editable}
        isDark={false}
        rightInset={canAttach ? 42 : 14}
        topInset={0}
        bottomInset={12}
        fillHeight
        onChange={(payload) => onChange?.(payload.serializedText)}
      />
      <div style={{ position: "absolute", right: "6px", bottom: "6px", display: "flex", alignItems: "center", gap: "2px", zIndex: 3 }}>
        {!isUser ? (
          <AgentSelector
            agent={agent}
            model={model}
            mode={agentMode}
            effort={effort}
            agents={agents}
            compact
            menuPlacement="top"
            showChevron
            warnUnavailable={false}
            onAgentChange={editable ? (onAgentChange || noop) : noop}
            onModeChange={editable ? (onModeChange || noop) : noop}
            onEffortChange={editable ? (onEffortChange || noop) : noop}
            onLongContextChange={editable ? (onLongContextChange || noop) : noop}
            onFastServiceChange={noop}
            onAgentRestart={onRestartAgent}
          />
        ) : null}
        {canAttach ? (
          <button
            type="button"
            title={t("task.addAttachment")}
            aria-label={t("task.addAttachment")}
            disabled={!editable || sending}
            onClick={onAttach}
            style={composerIconButtonStyle({ enabled: editable && !sending })}
          >
            <PlusIcon />
          </button>
        ) : null}
        {editable ? (
          <button
            type="button"
            disabled={!canSend}
            onClick={onSend}
            title={t("common.save")}
            aria-label={t("common.save")}
            style={composerSendButtonStyle({ enabled: canSend })}
          >
            <SendIcon />
          </button>
        ) : (
          <button
            type="button"
            title={t("common.edit")}
            aria-label={t("common.edit")}
            disabled={mode === "done" || sending}
            onClick={onEdit}
            style={composerIconButtonStyle({ enabled: mode !== "done" && !!onEdit, dimmed: mode === "done" })}
          >
            <PencilIcon />
          </button>
        )}
      </div>
    </div>
  );
});
