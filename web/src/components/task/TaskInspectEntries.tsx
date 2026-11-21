import React, { useCallback, useState } from "react";
import { useI18n } from "../../i18n/index";
import { RunViewEntry } from "./RunViewEntry";
import { SupervisionEntry } from "./SupervisionEntry";

// 任务详情里的两个只读入口：执行详情 + 监管记录。
//
// 用户要求（2026-11-21）：两个小胶囊始终存在于同一行；展开互斥——点开一个就
// 收起另一个；每个胶囊自己带 ▸/▾ 记号，一眼能看出当前展开的是哪个。
//
// 互斥靠这里唯一的一份 openSection 状态，不靠两个子组件各自记 open：两个独立
// 布尔值一旦都能为 true，就会同时展开。子组件只负责面板内容，按钮不在里面。

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

type InspectSection = "run" | "supervision";

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

  const toggle = useCallback((section: InspectSection) => {
    setOpenSection((current) => (current === section ? null : section));
  }, []);

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
        open={openSection === "supervision"}
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
      <span aria-hidden style={chevronStyle}>
        {open ? "▾" : "▸"}
      </span>
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

const chevronStyle: React.CSSProperties = {
  fontSize: "10px",
  lineHeight: 1,
};
