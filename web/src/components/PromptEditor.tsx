import React, { forwardRef, useEffect, useImperativeHandle, useRef, useState } from "react";
import { AgentSelector } from "./AgentSelector";
import { has1MSuffix } from "./ActionBar";
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
  onAttach?: () => void;
  onEdit?: () => void;
  onSend?: () => void;
  /** 回车处理，返回 true 表示已消费（交给 TokenEditor 的 Enter 命令）。 */
  onEnter?: (event: KeyboardEvent | null) => boolean;
  sending?: boolean;
  sendDisabled?: boolean;
  /** 节点主题色（hex），用于发送/编辑按钮的外框与底色 */
  accentColor?: string;
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
    onAttach,
    onEdit,
    onSend,
    onEnter,
    sending,
    sendDisabled,
    accentColor,
  },
  ref
) {
  const { t } = useI18n();
  const editorRef = useRef<TokenEditorHandle | null>(null);
  const editable = mode === "editable";
  const isUser = role === "user";
  // 单行时右侧给控件留位；一旦折行就与对话输入框一样铺满整宽，只在底部让出控件高度。
  const [isMultiLine, setIsMultiLine] = useState(false);

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

  // 灌入后高度会变，重算一次多行标记（否则从长文本切到短文本会一直留着底部留白）。
  useEffect(() => {
    const frame = requestAnimationFrame(() => {
      setIsMultiLine((editorRef.current?.getHeight() || 44) > 50);
    });
    return () => cancelAnimationFrame(frame);
  }, [value, resetKey, mode]);

  const syncEditorHeight = () => {
    setIsMultiLine((editorRef.current?.getHeight() || 44) > 50);
  };

  const noop = () => {};
  const canSend = editable && !sending && !sendDisabled;

  // 默认回车 = 发送（Shift+Enter 换行），与对话输入框一致；输入法组合中的回车放行。
  const isComposing = (event: KeyboardEvent | null) => {
    const native = event as (KeyboardEvent & { isComposing?: boolean; keyCode?: number }) | null | undefined;
    return !!native?.isComposing || native?.keyCode === 229;
  };
  const handleEnter = (event: KeyboardEvent | null): boolean => {
    if (onEnter) return onEnter(event);
    if (isComposing(event) || event?.shiftKey) return false;
    event?.preventDefault();
    if (canSend) onSend?.();
    return true;
  };

  return (
    <div style={composerContainerStyle(mode === "done" ? "rgba(148, 163, 184, 0.10)" : "var(--input-bg)")}>
      <TokenEditor
        ref={editorRef}
        placeholder={placeholder || t("taskTemplate.promptTemplate")}
        disabled={sending}
        readOnly={!editable}
        isDark={false}
        rightInset={isMultiLine ? 14 : 96}
        topInset={0}
        bottomInset={isMultiLine ? 44 : 12}
        fillHeight
        onEnter={handleEnter}
        onChange={(payload) => {
          onChange?.(payload.serializedText);
          if (!payload.displayText.trim()) {
            setIsMultiLine(false);
            return;
          }
          requestAnimationFrame(syncEditorHeight);
        }}
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
            viewportMenu
            showChevron={false}
            warnUnavailable={false}
            longContext={has1MSuffix(model || "")}
            onAgentChange={editable ? (onAgentChange || noop) : noop}
            onModeChange={editable ? (onModeChange || noop) : noop}
            onEffortChange={editable ? (onEffortChange || noop) : noop}
            onLongContextChange={editable ? (onLongContextChange || noop) : noop}
            onFastServiceChange={noop}
            onAgentRestart={onRestartAgent}
          />
        ) : null}
        {onAttach ? (
          <button
            type="button"
            title={t("task.addAttachment")}
            aria-label={t("task.addAttachment")}
            disabled={!editable || sending}
            onMouseDown={(event) => event.preventDefault()}
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
            onMouseDown={(event) => event.preventDefault()}
            onClick={onSend}
            title={t("common.save")}
            aria-label={t("common.save")}
            style={composerSendButtonStyle({ enabled: canSend, variant: "panel", accent: accentColor })}
          >
            <SendIcon />
          </button>
        ) : (
          <button
            type="button"
            title={t("common.edit")}
            aria-label={t("common.edit")}
            disabled={mode === "done" || sending}
            onMouseDown={(event) => event.preventDefault()}
            onClick={onEdit}
            style={composerSendButtonStyle({
              enabled: mode !== "done" && !!onEdit,
              variant: "panel",
              accent: accentColor,
            })}
          >
            <PencilIcon />
          </button>
        )}
      </div>
    </div>
  );
});
