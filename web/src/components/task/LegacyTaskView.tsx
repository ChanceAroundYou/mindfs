import { useState } from "react";
import { PanelShell, panelButtonStyle } from "../shell/PanelShell";
import { useI18n } from "../../i18n/index";
import { ActivityMessage } from "./ActivityMessage";
import { Collapsible } from "./Collapsible";
import { latestStageRun, statusText } from "./taskDetailUtils";
import { extractSummaryFromStageRuns } from "../../shared/legacySummary";
import { taskStatusColor } from "../../app/appTask";
import type { AgentStatus } from "../../shared/agentTypes";
import type { ActivityStatus, TaskActivity } from "../../shared/taskComments";
import type { TaskDetail, TaskStatus } from "../../shared/taskTypes";

export type LegacyTaskViewProps = {
  detail: TaskDetail;
  agents?: AgentStatus[];
  onClose: () => void;
  onOpenSession?: (sessionKey: string) => void;
  nodeId?: string;
  /** 测试与调试用：初始是否展开阶段列表。默认收起。 */
  initialStagesOpen?: boolean;
};

function taskActivityStatus(status: TaskStatus): ActivityStatus {
  switch (status) {
    case "success": return "completed";
    case "fail": return "failed";
    case "cancelled": return "cancelled";
    case "running": return "running";
    default: return "pending";
  }
}

/**
 * 历史任务（模板时代 / 阶段模式）的详情视图。
 *
 * 这些任务没有 activities 表记录，所以把它投影成一条「Agent 总结回复」：
 * 总结从 stage_runs 提取，原始阶段列表收进可折叠的审计区。
 * 不写库、不改执行引擎 —— 只是给旧数据一个与对话式视图一致的读法。
 */
export function LegacyTaskView({
  detail,
  onClose,
  onOpenSession,
  initialStagesOpen = false,
}: LegacyTaskViewProps) {
  const { t } = useI18n();
  const [showStages, setShowStages] = useState(initialStagesOpen);

  const task = detail.task;
  const stages = task.stages ?? [];
  const initialInput = (latestStageRun(detail, 0)?.input || stages[0]?.prompt_template || "").trim();
  const succeeded = task.status === "success";
  const summary = extractSummaryFromStageRuns(
    detail.stage_runs,
    succeeded ? undefined : t("task.legacy.noRecord"),
  );
  // 主会话可能没落在 task 上（老任务）；退到最近一条带 session 的阶段运行。
  const sessionKey = task.main_session_key
    || [...detail.stage_runs].reverse().find((run) => run.session_key)?.session_key;

  const syntheticAgentReply: TaskActivity = {
    id: "legacy-summary",
    task_id: task.id,
    root_id: task.root_id,
    type: "agent_reply",
    author: t("task.activity.agent"),
    role: "agent",
    body: "",
    workflow_choice: "empty",
    // 让 ActivityMessage 显示「查看执行详情」，点击即展开下面的阶段列表。
    run_id: "legacy-summary",
    status: taskActivityStatus(task.status),
    summary,
    session_key: sessionKey,
    created_at: task.completed_at || task.updated_at || task.created_at,
    updated_at: task.updated_at,
  };

  return (
    <PanelShell
      width={720}
      onClose={onClose}
      title={(
        <span style={{ flex: "1 1 auto", minWidth: 0, fontSize: "14px", fontWeight: 700, color: "var(--text-color)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
          {task.name || t("task.namePlaceholder")}
        </span>
      )}
      headerLeft={(
        <span style={{ fontSize: "12px", fontWeight: 800, color: taskStatusColor(task.status), flexShrink: 0, whiteSpace: "nowrap" }}>
          {statusText(task.status, t)}
        </span>
      )}
    >
      <div style={{ padding: "12px 14px", display: "flex", flexDirection: "column", gap: "12px" }}>
        {initialInput ? (
          <div style={{ border: "1px dashed var(--border-color)", borderRadius: "8px", padding: "10px", background: "var(--panel-bg)" }}>
            <span style={{ fontSize: "11px", fontWeight: 800, color: "var(--text-secondary)" }}>
              {t("task.initialInput")}
            </span>
            <div style={{ fontSize: "12px", marginTop: "6px", whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>
              {initialInput}
            </div>
          </div>
        ) : null}

        <div data-testid="legacy-summary-reply">
          <ActivityMessage
            activity={syntheticAgentReply}
            onViewRun={() => setShowStages(true)}
            onOpenSession={(sessionKey) => onOpenSession?.(sessionKey)}
          />
        </div>

        <Collapsible
          title={t("task.legacy.viewStages")}
          subtitle={t("task.legacy.stagesSubtitle")}
          open={showStages}
          onToggle={() => setShowStages((current) => !current)}
          testId="legacy-stages-collapsible"
        >
          {stages.length === 0 ? (
            <div style={{ fontSize: "12px", color: "var(--text-secondary)" }}>{t("task.noStages")}</div>
          ) : null}
          {stages.map((stage, index) => {
            const run = latestStageRun(detail, index + 1);
            return (
              <div
                key={stage.id || `legacy-stage-${index + 1}`}
                data-testid="legacy-stage-row"
                style={{
                  border: "1px solid var(--border-color)",
                  borderRadius: "8px",
                  padding: "10px",
                  marginBottom: "8px",
                  background: "var(--panel-bg)",
                }}
              >
                <div style={{ fontWeight: 700, marginBottom: "6px" }}>
                  {stage.name || t("task.stageLabel", { index: index + 1 })}
                </div>
                <div style={{ fontSize: "11px", color: "var(--text-secondary)" }}>
                  {run ? statusText(run.status, t) : t("task.stage.notExecuted")}
                </div>
                {run?.session_key ? (
                  <button
                    type="button"
                    onClick={() => onOpenSession?.(run.session_key as string)}
                    style={{ ...panelButtonStyle("secondary"), marginTop: "6px", height: "26px", fontSize: "11px" }}
                  >
                    {t("task.openSession", { index: index + 1 })}
                  </button>
                ) : null}
              </div>
            );
          })}
        </Collapsible>
      </div>
    </PanelShell>
  );
}