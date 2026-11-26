import { appURL } from "../net/base";
import { protectedJSON } from "../net/api";
import type { RouterDecisionSummary } from "../../shared/routerDecisions";

// 监管空态需要的少量开关信息：Router 是否启用、主动监管是否打开。
export type RouterStatusSummary = {
  enabled: boolean;
  mode: string;
  supervision_mode?: string;
};

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

// Router 运行状态（只读）。失败由调用方按「未知」处理，不影响监管记录展示。
export async function fetchRouterStatus(
  rootId: string,
  nodeId?: string,
): Promise<RouterStatusSummary> {
  const params = new URLSearchParams({ root_id: rootId });
  const payload = await protectedJSON<{ router?: RouterStatusSummary }>(
    appURL("/api/router/status", params, nodeId),
  );
  return payload?.router ?? { enabled: false, mode: "off" };
}
