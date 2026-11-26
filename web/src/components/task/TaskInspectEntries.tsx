import React, { useCallback, useState } from "react";
import { useI18n } from "../../i18n/index";
import { ChevronIcon } from "../common/ChevronIcon";
import { RunViewEntry } from "./RunViewEntry";
import { SupervisionEntry } from "./SupervisionEntry";
import { TaskCompletionEntry } from "./TaskCompletionEntry";
import { completionStatusKey } from "./taskCompletionModel";
import { useTaskCompletion } from "./useTaskCompletion";

// 任务详情里的三个只读入口：执行详情 + 监管记录 + 完成证据。
//
// 用户要求（2026-11-21）：小胶囊始终存在于同一行；展开互斥——点开一个就
// 收起另一个；每个胶囊自己带人字箭头（展开时转 90°），一眼能看出当前展开的是哪个。
//
// 互斥靠这里唯一的一份 openSection 状态，不靠各子组件自己记 open：多个独立
// 布尔值一旦都能为 true，就会同时展开。子组件只负责面板内容，按钮不在里面。
//
// 完成证据（2026-11-25）：以前它常驻展开挂在胶囊行下面，长任务的验收项会把详情页
// 底部铺成一墙文字。改成同一行的第三个胶囊后，底部只剩一行胶囊；它是否出现仍由
// 「有没有完成记录」决定，所以取数也提到这一层（useTaskCompletion），胶囊才能按
// loaded + present 决定显不显示。

export type TaskInspectEntriesProps = {
  rootId: string;
  taskId: string;
  nodeId?: string;
  /** 任务头每次更新递增（父层传 task.updated_at）。 */
  refreshKey?: string;
  /** 直连对话任务的执行会话（任务主会话）。 */
  fallbackSessionKey?: string;
  /** 任务状态：直连节点行沿用任务级状态。 */
  taskStatus?: string;
  onOpenSession: (sessionKey: string) => void;
};

type InspectSection = "run" | "supervision" | "completion";

export function TaskInspectEntries({
  rootId,
  taskId,
  nodeId,
  refreshKey,
  fallbackSessionKey,
  taskStatus,
  onOpenSession,
}: TaskInspectEntriesProps) {
  const { t } = useI18n();
  const [openSection, setOpenSection] = useState<InspectSection | null>(null);
  const completionState = useTaskCompletion(rootId, taskId, nodeId, refreshKey);

  const toggle = useCallback((section: InspectSection) => {
    setOpenSection((current) => (current === section ? null : section));
  }, []);

  const completion = completionState.completion;
  const showCompletion = completionState.loaded && !!completion && completion.present;
  const completionStatus = completion ? completionStatusKey(completion.status) : null;

  return (
    <div data-testid="task-inspect-entries" style={containerStyle}>
      <div data-testid="task-inspect-row" role="group" style={rowStyle}>
        <InspectCapsule
          testId="run-view-toggle"
          controls="task-run-panel"
          label={t("task.runTimeline.label")}
          open={openSection === "run"}
          onClick={() => toggle("run")}
        />
        <InspectCapsule
          testId="supervision-toggle"
          controls="task-supervision-panel"
          label={t("task.supervision.label")}
          open={openSection === "supervision"}
          onClick={() => toggle("supervision")}
        />
        {showCompletion ? (
          // 状态词进胶囊标题：「待补做」这类信息不该非要再点一次才看得见。
          <InspectCapsule
            testId="completion-toggle"
            controls="task-completion-panel"
            label={completionStatus
              ? `${t("task.completion.title")} · ${t(completionStatus)}`
              : t("task.completion.title")}
            open={openSection === "completion"}
            onClick={() => toggle("completion")}
          />
        ) : null}
      </div>
      <RunViewEntry
        rootId={rootId}
        taskId={taskId}
        nodeId={nodeId}
        refreshKey={refreshKey}
        open={openSection === "run"}
        fallbackSessionKey={fallbackSessionKey}
        taskStatus={taskStatus}
        onOpenSession={onOpenSession}
      />
      <SupervisionEntry
        rootId={rootId}
        taskId={taskId}
        nodeId={nodeId}
        refreshKey={refreshKey}
        completion={completion}
        open={openSection === "supervision"}
      />
      <TaskCompletionEntry
        completion={completion}
        loaded={completionState.loaded}
        loading={completionState.loading}
        error={completionState.error}
        open={openSection === "completion"}
      />
    </div>
  );
}

type InspectCapsuleProps = {
  testId: string;
  controls: string;
  label: string;
  open: boolean;
  onClick: () => void;
};

function InspectCapsule({ testId, controls, label, open, onClick }: InspectCapsuleProps) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-expanded={open}
      aria-controls={controls}
      data-testid={testId}
      data-open={open ? "true" : "false"}
      style={{ ...capsuleStyle, ...(open ? capsuleOpenStyle : null) }}
    >
      <ChevronIcon open={open} />
      <span>{label}</span>
    </button>
  );
}

const containerStyle: React.CSSProperties = {
  display: "flex",
  flexDirection: "column",
  gap: "6px",
};

const rowStyle: React.CSSProperties = {
  display: "flex",
  alignItems: "center",
  gap: "6px",
  flexWrap: "wrap",
};

const capsuleStyle: React.CSSProperties = {
  display: "inline-flex",
  alignItems: "center",
  gap: "4px",
  border: "1px solid var(--border-color)",
  background: "transparent",
  color: "var(--text-secondary)",
  borderRadius: "999px",
  padding: "2px 10px",
  fontSize: "12px",
  lineHeight: "18px",
  cursor: "pointer",
  whiteSpace: "nowrap",
};

const capsuleOpenStyle: React.CSSProperties = {
  borderColor: "var(--accent-color)",
  color: "var(--text-primary)",
  fontWeight: 600,
};
