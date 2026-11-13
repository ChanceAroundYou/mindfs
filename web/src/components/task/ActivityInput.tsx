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
        borderRadius: 10,
        padding: 12,
        background: "var(--content-bg)",
        display: "flex",
        flexDirection: "column",
        gap: 10,
      }}
    >
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
          minHeight: 70,
          fontSize: 13,
          padding: 10,
          border: "1px solid var(--border-color)",
          borderRadius: 6,
          resize: "vertical",
          background: "var(--panel-bg)",
          color: "inherit",
          lineHeight: 1.5,
        }}
      />

      <WorkflowSelector
        choice={choice}
        versionId={versionId}
        recommendation={recommendation}
        onChange={onWorkflowChange}
      />

      {error && (
        <div data-testid="input-error" style={{ fontSize: 12, color: "#dc2626", padding: "4px 0" }}>
          {error}
        </div>
      )}

      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
        <span style={{ fontSize: 11, color: "var(--text-secondary)" }}>
          {canSend ? "⌘+Enter 发送" : ""}
        </span>
        <button
          type="button"
          data-testid="activity-send"
          onClick={onSend}
          disabled={!canSend}
          style={{
            padding: "8px 16px",
            fontSize: 13,
            fontWeight: 500,
            borderRadius: 6,
            border: "none",
            background: canSend ? "var(--accent-color)" : "var(--border-color)",
            color: "#fff",
            cursor: canSend ? "pointer" : "not-allowed",
            opacity: canSend ? 1 : 0.6,
            transition: "all 0.15s ease",
          }}
        >
          {sending ? t("task.activity.sending") : t("task.activity.send")}
        </button>
      </div>
    </div>
  );
}