import React from "react";
import { useI18n } from "../../i18n/index";
import { TaskCompletionChecklist } from "./TaskCompletionChecklist";
import type { TaskCompletionView } from "../../shared/taskCompletion";

// 完成证据面板：由 TaskInspectEntries 的胶囊展开，本身不再画按钮。
//
// 数据由同一行的容器取（useTaskCompletion）：胶囊要不要出现取决于「有没有完成
// 记录」，所以取数必须发生在画按钮的那一层。2026-11-25 起默认收起 —— 以前这里
// 常驻展开一份清单，长任务会把详情页底部铺成一墙验收项文字（用户：「太丑了」）。
// 展开与否由父层唯一的 openSection 决定，与执行详情 / 监管记录互斥。
export type TaskCompletionEntryProps = {
  completion: TaskCompletionView | null;
  loaded: boolean;
  loading: boolean;
  error: string;
  /** 由同一行的胶囊组控制；false 时面板不渲染内容。 */
  open: boolean;
};

export function TaskCompletionEntry({ completion, loaded, loading, error, open }: TaskCompletionEntryProps) {
  const { t } = useI18n();

  if (!open || !loaded || !completion || !completion.present) return null;

  return (
    <div id="task-completion-panel" data-testid="task-completion-entry" style={panelStyle}>
      {error ? (
        <div style={{ fontSize: "12px", color: "var(--danger-color, #ef4444)" }} role="alert">
          {error}
        </div>
      ) : null}
      <TaskCompletionChecklist completion={completion} />
      {loading ? <div style={hintStyle}>{t("task.supervision.loading")}</div> : null}
    </div>
  );
}

const panelStyle: React.CSSProperties = {
  display: "flex",
  flexDirection: "column",
  gap: "6px",
  minWidth: 0,
};

const hintStyle: React.CSSProperties = {
  fontSize: "11px",
  color: "var(--text-secondary)",
};
