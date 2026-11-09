import React, { useState } from "react";
import { useI18n } from "../../i18n/index";
import type { TaskActivity, WorkflowChoice } from "../../shared/taskComments";
import { panelButtonStyle } from "../shell/PanelShell";
import { useCommentThread } from "./useCommentThread";

function isAgent(a: TaskActivity): boolean {
  return a.role === "agent" || a.type === "agent_reply";
}

function AgentDrilldown({ activity, onOpenSession }: { activity: TaskActivity; onOpenSession?: (key: string) => void }) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  if (!isAgent(activity)) return null;
  return (
    <div style={{ marginTop: "4px" }}>
      <button type="button" onClick={() => setOpen((v) => !v)} style={{ ...panelButtonStyle("secondary"), height: "24px", fontSize: "11px" }}>
        {open ? t("task.comments.collapse") : t("task.comments.expand")}
      </button>
      {open ? (
        <div data-testid="agent-drilldown" style={{ marginTop: "6px", border: "1px solid var(--border-color)", borderRadius: "6px", padding: "8px", background: "var(--content-bg)" }}>
          <div style={{ fontSize: "11px", color: "var(--text-secondary)", overflowWrap: "anywhere", whiteSpace: "pre-wrap" }}>{activity.body}</div>
          {activity.workflow_version_id ? (
            <div style={{ fontSize: "11px", color: "var(--text-secondary)", marginTop: "4px" }}>workflow: {activity.workflow_version_id}</div>
          ) : null}
          {onOpenSession ? (
            <button type="button" onClick={() => onOpenSession(activity.id)} style={{ ...panelButtonStyle("secondary"), height: "24px", fontSize: "11px", marginTop: "6px" }}>
              {t("task.comments.openSession")}
            </button>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

export function CommentThread({
  rootId,
  taskId,
  nodeId,
  onOpenSession,
}: {
  rootId: string;
  taskId: string;
  nodeId?: string;
  onOpenSession?: (sessionKey: string) => void;
}) {
  const { t } = useI18n();
  const { activities, loading, error, posting, post } = useCommentThread(rootId, taskId, nodeId);
  const [draft, setDraft] = useState("");
  const [choice, setChoice] = useState<WorkflowChoice>("empty");
  const [versionId, setVersionId] = useState("");

  const send = async () => {
    if (!draft.trim() || posting) return;
    if (choice === "specified" && !versionId.trim()) return;
    await post(draft, choice, versionId.trim() || undefined);
    setDraft("");
    setVersionId("");
  };

  return (
    <div data-testid="comment-thread" style={{ display: "flex", flexDirection: "column", gap: "8px", border: "1px solid var(--border-color)", borderRadius: "8px", padding: "10px", background: "var(--panel-bg)" }}>
      <span style={{ fontSize: "11px", fontWeight: 800, color: "var(--text-secondary)" }}>{t("task.comments.title")}</span>
      {loading ? <div style={{ fontSize: "12px", color: "var(--text-secondary)" }}>{t("task.loading")}</div> : null}
      {error ? <div data-testid="comment-error" style={{ fontSize: "12px", color: "#dc2626" }}>{error}</div> : null}
      <div style={{ display: "flex", flexDirection: "column", gap: "8px" }}>
        {activities.map((a) => (
          <div
            key={a.id}
            data-testid={isAgent(a) ? "agent-segment" : "user-message"}
            style={{
              alignSelf: isAgent(a) ? "flex-start" : "flex-end",
              maxWidth: "85%",
              border: "1px solid var(--border-color)",
              borderRadius: "8px",
              padding: "8px 10px",
              background: "var(--content-bg)",
            }}
          >
            <div style={{ display: "flex", gap: "6px", alignItems: "center", marginBottom: "4px" }}>
              <span style={{ fontSize: "11px", fontWeight: 700, color: isAgent(a) ? "var(--accent-color)" : "var(--text-color)" }}>
                {isAgent(a) ? t("task.comments.agent") : (a.author || t("task.comments.user"))}
              </span>
              {a.workflow_choice && a.workflow_choice !== "empty" ? (
                <span style={{ fontSize: "10px", color: "var(--text-secondary)", border: "1px solid var(--border-color)", borderRadius: "4px", padding: "0 4px" }}>
                  {a.workflow_choice}
                </span>
              ) : null}
              <span style={{ fontSize: "10px", color: "var(--text-secondary)" }}>{a.created_at}</span>
            </div>
            <div style={{ fontSize: "12px", overflowWrap: "anywhere", whiteSpace: "pre-wrap" }}>{a.body}</div>
            <AgentDrilldown activity={a} onOpenSession={onOpenSession} />
          </div>
        ))}
        {!loading && activities.length === 0 ? (
          <div style={{ fontSize: "12px", color: "var(--text-secondary)" }}>{t("task.comments.empty")}</div>
        ) : null}
      </div>
      <div style={{ display: "flex", gap: "6px", flexWrap: "wrap", alignItems: "center" }}>
        {/* 三态选择用按钮组而不是原生 <select>：仓库禁止原生 select（iOS/安卓壳里
            弹层样式不可控，见 web/tests/task-panel-options.test.mjs）。 */}
        <div role="radiogroup" aria-label={t("task.comments.workflowChoice")} style={{ display: "flex", gap: "4px" }}>
          {(["empty", "specified", "disabled"] as WorkflowChoice[]).map((value) => (
            <button
              key={value}
              type="button"
              role="radio"
              aria-checked={choice === value}
              onClick={() => setChoice(value)}
              style={{
                height: "28px",
                fontSize: "11px",
                padding: "0 8px",
                borderRadius: "6px",
                border: `1px solid ${choice === value ? "var(--accent-color)" : "var(--border-color)"}`,
                background: "transparent",
                color: choice === value ? "var(--accent-color)" : "var(--text-secondary)",
                cursor: "pointer",
              }}
            >
              {value === "empty"
                ? t("task.comments.choiceEmpty")
                : value === "specified"
                  ? t("task.comments.choiceSpecified")
                  : t("task.comments.choiceDisabled")}
            </button>
          ))}
        </div>
        {choice === "specified" ? (
          <input
            aria-label={t("task.comments.workflowVersion")}
            value={versionId}
            onChange={(e) => setVersionId(e.target.value)}
            placeholder={t("task.comments.workflowVersion")}
            style={{ height: "28px", fontSize: "11px", flex: "1 1 160px", minWidth: "120px" }}
          />
        ) : null}
      </div>
      <div style={{ display: "flex", gap: "6px" }}>
        <input
          aria-label={t("task.comments.placeholder")}
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => { if (e.key === "Enter") void send(); }}
          placeholder={t("task.comments.placeholder")}
          style={{ flex: "1 1 auto", minWidth: 0, height: "30px", fontSize: "12px" }}
        />
        <button
          type="button"
          disabled={posting || !draft.trim() || (choice === "specified" && !versionId.trim())}
          onClick={() => void send()}
          style={panelButtonStyle("primary")}
        >
          {t("task.comments.send")}
        </button>
      </div>
    </div>
  );
}
