import type React from "react";
import { useI18n } from "../../i18n/index";
import type { WorkflowChoice } from "../../shared/taskComments";
import type { WorkflowRecommendation } from "../../shared/workflowModel";
import { canSendActivity } from "../../shared/activityModel";
import { WorkflowSelector } from "./WorkflowSelector";

export type ActivityInputProps = {
  body: string;
  onBodyChange: (body: string) => void;
  choice: WorkflowChoice;
  versionId: string;
  onWorkflowChange: (choice: WorkflowChoice, versionId: string) => void;
  recommendation?: WorkflowRecommendation | null;
  sending?: boolean;
  error?: string | null;
  disabled?: boolean;
  placeholder?: string;
  onSend: () => void;
};

/**
 * 对话输入框：受控组件，状态由 ConversationView 持有。
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
  recommendation,
  sending = false,
  error,
  disabled = false,
  placeholder,
  onSend,
}: ActivityInputProps) {
  const { t } = useI18n();
  const canSend = canSendActivity({
    body,
    workflowChoice: choice,
    workflowVersionId: versionId,
    sending,
    disabled,
  });

  return (
    <div
      data-testid="activity-input"
      style={{
        border: "1px solid var(--border-color)",
        borderRadius: 8,
        padding: 10,
        background: "var(--panel-bg)",
        display: "flex",
        flexDirection: "column",
        gap: 8,
      }}
    >
      <WorkflowSelector
        choice={choice}
        versionId={versionId}
        userInput={body}
        recommendation={recommendation}
        onChange={onWorkflowChange}
      />

      <textarea
        value={body}
        onChange={(event) => onBodyChange(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === "Enter" && (event.metaKey || event.ctrlKey) && canSend) {
            event.preventDefault();
            onSend();
          }
        }}
        placeholder={placeholder ?? t("task.activity.inputPlaceholder")}
        disabled={disabled || sending}
        rows={3}
        style={{
          minHeight: 60,
          fontSize: 12,
          padding: 8,
          border: "1px solid var(--border-color)",
          borderRadius: 4,
          resize: "vertical",
          background: "var(--content-bg)",
          color: "inherit",
        }}
      />

      {error && (
        <div data-testid="input-error" style={{ fontSize: 11, color: "#dc2626" }}>
          {error}
        </div>
      )}

      <div style={{ display: "flex", justifyContent: "flex-end" }}>
        <button
          type="button"
          data-testid="activity-send"
          onClick={onSend}
          disabled={!canSend}
          style={{
            padding: "6px 12px",
            fontSize: 12,
            borderRadius: 4,
            border: "none",
            background: "var(--accent-color)",
            color: "#fff",
            cursor: canSend ? "pointer" : "not-allowed",
            opacity: canSend ? 1 : 0.5,
          }}
        >
          {sending ? t("task.activity.sending") : t("task.activity.send")}
        </button>
      </div>
    </div>
  );
}