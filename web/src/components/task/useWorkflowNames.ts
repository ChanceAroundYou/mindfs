import { useEffect, useState } from "react";
import { listAvailableWorkflows } from "../../services/workflows";

/**
 * workflow id → 用户可见名称。
 *
 * 活动只带 `workflow_version_id`（`<id>:vN`），页脚要显示 friend name 就必须
 * 回查可选工作流列表。取数放在 colocated hook 里：视图层不 import services。
 * 名称只是展示增强，失败时静默返回空表（页脚不显示工作流，而不是报错）。
 */
export function useWorkflowNames(): Record<string, string> {
  const [names, setNames] = useState<Record<string, string>>({});

  useEffect(() => {
    let cancelled = false;
    listAvailableWorkflows()
      .then((workflows) => {
        if (cancelled) return;
        const next: Record<string, string> = {};
        for (const workflow of workflows) {
          const name = `${workflow.name || ""}`.trim();
          if (workflow.id && name) next[workflow.id] = name;
        }
        setNames(next);
      })
      .catch(() => {
        // 名称缺失不影响对话本身。
      });
    return () => {
      cancelled = true;
    };
  }, []);

  return names;
}