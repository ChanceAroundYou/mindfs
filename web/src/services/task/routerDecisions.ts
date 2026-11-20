import { appURL } from "../net/base";
import { protectedJSON } from "../net/api";
import type { RouterDecisionSummary } from "../../shared/routerDecisions";

export type { RouterDecisionSummary };

// 任务维度的 Router 决策列表（时间倒序）。监管记录入口与将来的诊断面板共用。
export async function fetchRouterDecisions(
  rootId: string,
  taskId: string,
  nodeId?: string,
  limit = 50,
): Promise<RouterDecisionSummary[]> {
  const params = new URLSearchParams({ root_id: rootId, limit: String(limit) });
  const payload = await protectedJSON<{ decisions?: RouterDecisionSummary[] }>(
    appURL(`/api/tasks/${encodeURIComponent(taskId)}/router-decisions`, params, nodeId),
  );
  return Array.isArray(payload?.decisions) ? payload.decisions : [];
}
