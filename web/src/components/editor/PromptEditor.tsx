import React, { forwardRef, useEffect, useImperativeHandle, useRef } from "react";
import { composerEditorInsets, useComposerEditorHeight } from "../action/useComposerEditorHeight";
import TokenEditor, { type TokenEditorHandle } from "./TokenEditor";
import type { ActiveToken } from "./tokenEditorUtils";
import { PlusIcon, SendIcon } from "../action/composerIcons";
import { composerContainerStyle, composerIconButtonStyle, composerSendButtonStyle } from "../action/composerStyles";
import { useI18n } from "../../i18n";

export type PromptEditorProps = {
  value: string;
  onChange?: (value: string) => void;
  /** 编辑器里的 @ / # / / 触发词变化（用于候选下拉）；清空时给 null。 */
  onActiveTokenChange?: (token: ActiveToken | null) => void;
  /** 该值变化时把 value 灌入编辑器一次（例如切换编辑对象） */
  resetKey?: string | number;
  placeholder?: string;
  onAttach?: () => void;
  onSend?: (text: string) => void;
  /** 回车处理，返回 true 表示已消费（交给 TokenEditor 的 Enter 命令）。 */
  onEnter?: (event: KeyboardEvent | null) => boolean;
  /** 键盘处理（候选上下键 / Tab / Esc）；返回 true 表示已消费。 */
  onKeyDown?: (event: React.KeyboardEvent<HTMLDivElement>) => void;
  /** 粘贴：图片文件交给附件链路时由调用方 preventDefault。 */
  onPaste?: (event: React.ClipboardEvent<HTMLDivElement>) => void;
  sending?: boolean;
  sendDisabled?: boolean;
  /** 节点主题色（hex），用于发送按钮的外框与底色 */
  accentColor?: string;
  /**
   * 附件/发送按钮左侧的额外控件（例如任务对话的工作流选择器）。
   * 放在编辑器右下角控件行里，与输入框同源，不再另起一行。
   */
  extraControls?: React.ReactNode;
  /** 测试/定位用：挂在最外层容器上。 */
  containerTestId?: string;
  /** 测试/定位用：挂在发送按钮上。 */
  sendTestId?: string;
  /** 发送按钮的可读标签；默认沿用「保存」。 */
  sendLabel?: string;
};

/**
 * PromptEditor —— 任务侧的单条留言编辑控件。
 *
 * 与对话输入框（ActionBar）共用 composerStyles 的容器与按钮样式，只保留
 * 「新增文件 + 发送」。任务不再有阶段，所以这里没有 agent 选择器、没有
 * 只读/已完成态，也没有段选项栏。
 */
export const PromptEditor = forwardRef<TokenEditorHandle, PromptEditorProps>(function PromptEditor(
  {
    value,
    onChange,
    onActiveTokenChange,
    resetKey,
    placeholder,
    onAttach,
    onSend,
    onEnter,
    onKeyDown,
    onPaste,
    sending,
    sendDisabled,
    accentColor,
    extraControls,
    containerTestId,
    sendTestId,
    sendLabel,
  },
  ref
) {
  const { t } = useI18n();
  const editorRef = useRef<TokenEditorHandle | null>(null);
  // 折行判定与 inset 切换与对话输入框共用同一 hook（阈值只有一处，不会各改各的）。
  const {
    isMultiLine,
    syncEditorHeight,
    resetEditorHeight,
  } = useComposerEditorHeight(editorRef);

  useImperativeHandle(ref, () => ({
    focus: () => editorRef.current?.focus(),
    blur: () => editorRef.current?.blur(),
    getHeight: () => editorRef.current?.getHeight() || 0,
    clear: () => editorRef.current?.clear(),
    setText: (text: string) => editorRef.current?.setText(text),
    getSerializedText: () => editorRef.current?.getSerializedText() ?? value,
    insertCandidate: (type, val) => editorRef.current?.insertCandidate(type, val),
  }));

  // resetKey 变化（切换编辑对象）时灌入 value
  useEffect(() => {
    editorRef.current?.setText(value || "");
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [resetKey]);

  // 灌入后高度会变，重算一次多行标记（否则从长文本切到短文本会一直留着底部留白）。
  useEffect(() => {
    const frame = requestAnimationFrame(syncEditorHeight);
    return () => cancelAnimationFrame(frame);
  }, [value, resetKey, syncEditorHeight]);

  const canSend = !sending && !sendDisabled;
  // 右侧控件行多一个按钮时把单行留位加宽，否则文字会压在控件上。
  const insets = composerEditorInsets(isMultiLine, extraControls ? 132 : 96);

  // 发送入口同步从编辑器读最新文本：内容经 onChange 异步进父 state，
  // 快打后立刻发送时父 value 还是旧值。按钮点击与回车共用这一条。
  const flushAndSend = () => {
    const latest = editorRef.current?.getSerializedText() ?? value;
    onChange?.(latest);
    onSend?.(latest);
  };

  // 默认回车 = 发送（Shift+Enter 换行），与对话输入框一致；输入法组合中的回车放行。
  const isComposing = (event: KeyboardEvent | null) => {
    const native = event as (KeyboardEvent & { isComposing?: boolean; keyCode?: number }) | null | undefined;
    return !!native?.isComposing || native?.keyCode === 229;
  };
  const handleEnter = (event: KeyboardEvent | null): boolean => {
    // 候选下拉优先：面板返回 true 时由它选中候选，不再触发发送。
    if (onEnter?.(event)) return true;
    if (isComposing(event) || event?.shiftKey) return false;
    event?.preventDefault();
    if (canSend) flushAndSend();
    return true;
  };

  return (
    <div data-testid={containerTestId} style={composerContainerStyle("var(--input-bg)")}>
      {/* 刻意**不传** fillHeight：那条路径把 contentEditable 设成 height:100% /
          maxHeight:none，高度随内容无限长高，而容器只有 minHeight、发送键又是
          absolute + bottom:6 挂在容器上 —— 任务文本一长，发送键就跟着沉到滚动视口
          之外，用户翻到底也点不到（2026-10-26 用户实测）。不传就走 TokenEditor 的
          默认路径：maxHeight 240px + 编辑器内部滚动，与对话输入框同源。 */}
      <TokenEditor
        ref={editorRef}
        placeholder={placeholder || t("task.editPlaceholder")}
        disabled={sending}
        isDark={false}
        rightInset={insets.rightInset}
        topInset={insets.topInset}
        bottomInset={insets.bottomInset}
        onEnter={handleEnter}
        onKeyDown={onKeyDown}
        onPaste={onPaste}
        onChange={(payload) => {
          onChange?.(payload.serializedText);
          onActiveTokenChange?.(payload.activeToken);
          if (!payload.displayText.trim()) {
            resetEditorHeight();
            return;
          }
          requestAnimationFrame(syncEditorHeight);
        }}
      />
      <div style={{ position: "absolute", right: "6px", bottom: "6px", display: "flex", alignItems: "center", gap: "2px", zIndex: 3 }}>
        {extraControls}
        {onAttach ? (
          <button
            type="button"
            title={t("task.addAttachment")}
            aria-label={t("task.addAttachment")}
            disabled={!!sending}
            onMouseDown={(event) => event.preventDefault()}
            onClick={onAttach}
            style={composerIconButtonStyle({ enabled: !sending })}
          >
            <PlusIcon />
          </button>
        ) : null}
        {onSend ? (
          <button
            type="button"
            data-testid={sendTestId}
            disabled={!canSend}
            onMouseDown={(event) => event.preventDefault()}
            onClick={() => {
              if (canSend) flushAndSend();
            }}
            title={sendLabel || t("common.save")}
            aria-label={sendLabel || t("common.save")}
            style={composerSendButtonStyle({ enabled: canSend, variant: "panel", accent: accentColor })}
          >
            <SendIcon />
          </button>
        ) : null}
      </div>
    </div>
  );
});