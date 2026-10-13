import React from "react";
import { AgentIcon } from "../agent/AgentIcon";
import { AsyncStateMessage } from "../common/AsyncStateMessage";
import { renderToolIcon } from "../stream/toolCallIcons";
import { DeleteIcon, RunNowIcon, StopIcon, taskIconButtonStyle } from "./ScheduledCronEditor";
import { formatTime } from "./scheduledTaskUtils";
import { useI18n } from "../../i18n/index";
import type { Locale } from "../../i18n/index";
import type { ScheduledAgentTask } from "../../shared/scheduledTaskTypes";

export type ScheduledAgentTaskListProps = {
  tasks: ScheduledAgentTask[];
  loading: boolean;
  runningTaskId: string | null;
  locale: Locale;
  onToggleEnabled: (task: ScheduledAgentTask) => void;
  onRunNow: (task: ScheduledAgentTask) => void;
  onEdit: (task: ScheduledAgentTask) => void;
  onDelete: (task: ScheduledAgentTask) => void;
};

export function ScheduledAgentTaskList({
  tasks,
  loading,
  runningTaskId,
  locale,
  onToggleEnabled,
  onRunNow,
  onEdit,
  onDelete,
}: ScheduledAgentTaskListProps) {
  const { t } = useI18n();
  return (
    <>
      {loading ? (
        <AsyncStateMessage
          kind="loading"
          style={{
            border: "1px solid var(--border-color)",
            borderRadius: 8,
            padding: 24,
            color: "var(--text-secondary)",
            textAlign: "center",
            fontSize: 13,
          }}
        >
          {t("common.loading")}
        </AsyncStateMessage>
      ) : tasks.length === 0 ? (
        <AsyncStateMessage
          kind="empty"
          style={{
            border: "1px dashed var(--border-color)",
            borderRadius: 8,
            padding: 24,
            color: "var(--text-secondary)",
            textAlign: "center",
            fontSize: 13,
          }}
        >
          {t("scheduled.empty")}
        </AsyncStateMessage>
      ) : (
        <div
          style={{
            display: "flex",
            flexDirection: "column",
            gap: 8,
            maxHeight: "56vh",
            overflow: "auto",
          }}
        >
          {tasks.map((task) => (
            <div
              key={task.id}
              style={{
                border: "1px solid var(--border-color)",
                borderRadius: 8,
                padding: 12,
                background: "var(--content-bg, #fff)",
              }}
            >
              <div
                style={{
                  display: "flex",
                  justifyContent: "space-between",
                  alignItems: "center",
                  gap: 12,
                }}
              >
                <button
                  type="button"
                  onClick={() => onEdit(task)}
                  style={{
                    border: "none",
                    background: "transparent",
                    padding: 0,
                    color: "var(--text-primary)",
                    textAlign: "left",
                    cursor: "pointer",
                    minWidth: 0,
                    flex: 1,
                  }}
                >
                  <div
                    style={{
                      fontSize: 14,
                      fontWeight: 700,
                      overflow: "hidden",
                      textOverflow: "ellipsis",
                      whiteSpace: "nowrap",
                    }}
                  >
                    {task.name || t("scheduled.unnamedTask")}
                  </div>
                </button>
                <div
                  style={{
                    display: "flex",
                    alignItems: "center",
                    gap: 4,
                    flexShrink: 0,
                  }}
                >
                  <button
                    type="button"
                    title={t("scheduled.stop")}
                    aria-label={t("scheduled.stopTask")}
                    disabled={!task.enabled}
                    onClick={() => {
                      if (task.enabled) void onToggleEnabled(task);
                    }}
                    style={{
                      ...taskIconButtonStyle,
                      color: task.enabled ? "#dc2626" : "var(--text-secondary)",
                      opacity: task.enabled ? 1 : 0.5,
                      cursor: task.enabled ? "pointer" : "not-allowed",
                    }}
                  >
                    <StopIcon />
                  </button>
                  <button
                    type="button"
                    title={t("scheduled.runNow")}
                    aria-label={t("scheduled.runNowTask")}
                    disabled={runningTaskId === task.id}
                    onClick={() => void onRunNow(task)}
                    style={{
                      ...taskIconButtonStyle,
                      color: "var(--accent-color)",
                      opacity: runningTaskId === task.id ? 0.6 : 1,
                    }}
                  >
                    <RunNowIcon />
                  </button>
                  <button
                    type="button"
                    title={t("common.edit")}
                    aria-label={t("scheduled.editTask")}
                    onClick={() => onEdit(task)}
                    style={taskIconButtonStyle}
                  >
                    {renderToolIcon("edit")}
                  </button>
                  <button
                    type="button"
                    title={t("common.delete")}
                    aria-label={t("scheduled.deleteTask")}
                    onClick={() => void onDelete(task)}
                    style={{ ...taskIconButtonStyle, color: "#dc2626" }}
                  >
                    <DeleteIcon />
                  </button>
                </div>
              </div>
              <div
                style={{
                  marginTop: 6,
                  display: "grid",
                  gridTemplateColumns: "1fr 1fr",
                  gap: 8,
                  fontSize: 12,
                  color: "var(--text-secondary)",
                }}
              >
                <span
                  style={{
                    display: "inline-flex",
                    alignItems: "center",
                    gap: 8,
                    overflow: "hidden",
                    textOverflow: "ellipsis",
                    whiteSpace: "nowrap",
                  }}
                >
                  <span>{task.enabled ? t("scheduled.enabled") : t("scheduled.disabled")}</span>
                  <span>{task.task_cron}</span>
                  {task.new_session_cron ? (
                    <span>{t("scheduled.newSessionCron", { cron: task.new_session_cron })}</span>
                  ) : null}
                  {task.running ? (
                    <span style={{ color: "var(--accent-color)" }}>{t("scheduled.running")}</span>
                  ) : null}
                </span>
                <span
                  style={{
                    display: "inline-flex",
                    alignItems: "center",
                    gap: 4,
                    overflow: "hidden",
                    textOverflow: "ellipsis",
                    whiteSpace: "nowrap",
                  }}
                >
                  <AgentIcon agentName={task.agent} size={14} />
                  <span>{task.agent}</span>
                </span>
              </div>
              <div
                style={{
                  marginTop: 10,
                  display: "grid",
                  gridTemplateColumns: "1fr 1fr",
                  gap: 8,
                  fontSize: 12,
                  color: "var(--text-secondary)",
                }}
              >
                <span>{t("scheduled.lastRun", { time: formatTime(task.last_run_at, locale, t("scheduled.neverRun")) })}</span>
                <span>{t("scheduled.nextRun", { time: formatTime(task.next_run_at, locale, t("scheduled.neverRun")) })}</span>
              </div>
            </div>
          ))}
        </div>
      )}
    </>);
}
