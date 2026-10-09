import type { I18nContextValue } from "../../i18n/index";
import type { TaskDetail } from "../../services/tasks";

export function statusText(status: string, t: (key: Parameters<I18nContextValue["t"]>[0]) => string): string {
  switch (status) {
    case "pending": return t("task.status.pending");
    case "running": return t("task.status.running");
    case "waiting_user": return t("task.status.waitingUser");
    case "paused": return t("task.status.paused");
    case "success": return t("task.status.success");
    case "fail": return t("task.status.fail");
    case "cancelled": return t("task.status.cancelled");
    case "approved": return t("task.status.approved");
    case "rejected": return t("task.status.rejected");
    default: return status;
  }
}

export function latestStageRun(detail: TaskDetail, index: number): TaskDetail["stage_runs"][number] | null {
  return (detail.stage_runs || [])
    .filter((run) => run.stage_index === index)
    .sort((a, b) => (a.updated_at < b.updated_at ? 1 : -1))[0] || null;
}
