import type React from "react";
import { useI18n } from "../../i18n/index";
import type { WorkflowChoice } from "../../shared/taskComments";
import { canSendActivity } from "../../shared/activityModel";
import { formatSendShortcut, type SendShortcut } from "../../shared/sendShortcut";
import { PromptEditor } from "../editor/PromptEditor";
import { WorkflowSelector } from "./WorkflowSelector";

// PromptEditor 默认「回车发送、Shift+Enter 换行」，这里只把提示文案对齐，
// 不再沿用旧 textarea 的 Ctrl/⌘+Enter（两套快捷键会让用户按错）。
const ENTER_SEND_SHORTCUT: SendShortcut = {
  key: "Enter",
  alt: false,
  ctrl: false,
  meta: false,
  shift: false,
};

export type ActivityInputProps = {
  body: string;
  onBodyChange: (body: string) => void;
  choice: WorkflowChoice;
  versionId: string;
  onWorkflowChange: (choice: WorkflowChoice, versionId: string) => void;
  sending?: boolean;
  error?: string | null;
  placeholder?: string;
  /** 每次发送成功后由父层递增，把编辑器清空。 */
  resetKey?: number;
  onSend: (text: string) => void;
};

/**
 * 对话输入框：受控组件，状态由 ConversationView 持有。
 *
 * 与新建任务面板共用 PromptEditor（Lexical token 编辑器），不再是裸 textarea
 * 加手搓按钮：圆角、字号、回车行为、发送键位置都跟系统其它输入框一致。
 *
 * 视图层不碰 services（仓库硬规则），发送动作通过 onSend 回调交给 hook；
 * 禁用条件统一走 canSendActivity，避免「按钮能点但请求必然 400」的假可用。
 */
export function ActivityInput({
  body,
  onBodyChange,
  choice,
  versionId,
  onWorkflowChange,
  sending = false,
  error,
  placeholder,
  resetKey,
  onSend,
}: ActivityInputProps) {
  const { t } = useI18n();
  const canSend = canSendActivity({
    body,
    workflowChoice: choice,
    workflowVersionId: versionId,
    sending,
  });

  // 发送入口从编辑器读最新文本：输入后立刻回车时，父层 body 可能还没回流。
  const handleSend = (text: string) => {
    const latest = text || body;
    if (!canSendActivity({ body: latest, workflowChoice: choice, workflowVersionId: versionId, sending })) return;
    onBodyChange(latest);
    onSend(latest);
  };

  return (
    <div
      data-testid="activity-input"
      style={{
        display: "flex",
        flexDirection: "column",
        gap: 8,
      }}
    >
      <PromptEditor
        value={body}
        onChange={onBodyChange}
        resetKey={resetKey}
        placeholder={placeholder ?? t("task.activity.inputPlaceholder")}
        onSend={handleSend}
        sending={sending}
        sendDisabled={!canSend}
        sendTestId="activity-send"
        sendLabel={sending ? t("task.activity.sending") : t("task.activity.send")}
      />

      <WorkflowSelector
        choice={choice}
        versionId={versionId}
        onChange={onWorkflowChange}
      />

      {error && (
        <div data-testid="input-error" style={{ fontSize: 12, color: "#dc2626", padding: "4px 0" }}>
          {error}
        </div>
      )}

      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", gap: 8 }}>
        <span style={{ fontSize: 11, color: "var(--text-secondary)" }}>
          {sending
            ? t("task.activity.sending")
            : canSend
              ? t("task.activity.sendHint", { shortcut: formatSendShortcut(ENTER_SEND_SHORTCUT) })
              : ""}
        </span>
      </div>
    </div>
  );
}