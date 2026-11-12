import { useEffect, useState } from "react";
import { listAvailableWorkflows } from "../../services/workflows";
import type { WorkflowMeta } from "../../shared/workflowModel";

export type WorkflowPickerState = {
  workflows: WorkflowMeta[];
  loading: boolean;
  failed: boolean;
};

/**
 * 拉取可选 workflow 列表。
 *
 * 单域数据获取放在组件旁的 colocated hook 里而不是组件内：`web/src/components`
 * 不允许直接 import `services/`（reviewer-architecture-rules.test.mjs 钉住）。
 * 卸载/重挂时用 cancelled 丢弃过期回包，避免旧节点或旧账户的列表覆盖新状态。
 */
export function useWorkflowPicker(): WorkflowPickerState {
  const [workflows, setWorkflows] = useState<WorkflowMeta[]>([]);
  const [loading, setLoading] = useState(true);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    let cancelled = false;
    listAvailableWorkflows()
      .then((items) => {
        if (!cancelled) setWorkflows(items);
      })
      .catch(() => {
        if (!cancelled) setFailed(true);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  return { workflows, loading, failed };
}