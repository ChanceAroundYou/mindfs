// 会话维护（会话压缩 skill 的无损部分）的 HTTP 封装。
//
// 这些端点都是数据面请求，跟着会话所属节点走；rootless 诊断（跨项目）用页面当前
// 激活节点。组件不许直接 import 本文件 —— 入口是 useSessionMaintenance。
import { protectedJSON } from "../net/api";
import { appURL } from "../net/base";
import { getRootNodeId } from "../net/rootNode";
import type {
  MaintenanceCleanResult,
  MaintenanceLockResult,
  MaintenanceReport,
  RuntimeIssueReport,
  RuntimeSweepReport,
  SessionMaintenanceTarget,
} from "../../shared/sessionMaintenance";

function maintenanceURL(path: string, rootId?: string, nodeId?: string): string {
  const params = new URLSearchParams();
  if (rootId) params.set("root", rootId);
  const targetNode = nodeId || (rootId ? getRootNodeId(rootId) : undefined);
  return appURL(path, params, targetNode);
}

function postJSON<T>(path: string, rootId: string | undefined, body: Record<string, unknown>, nodeId?: string): Promise<T> {
  return protectedJSON<T>(maintenanceURL(path, rootId, nodeId), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ ...body, root_id: rootId ?? "" }),
  });
}

export function fetchMaintenanceDiagnose(rootId?: string, nodeId?: string): Promise<MaintenanceReport> {
  return protectedJSON<MaintenanceReport>(maintenanceURL("/api/sessions/maintenance/diagnose", rootId, nodeId));
}

export function cleanMaintenanceLocks(nodeId?: string): Promise<MaintenanceLockResult> {
  return postJSON<MaintenanceLockResult>("/api/sessions/maintenance/locks/clean", undefined, {}, nodeId);
}

export function cleanSessionImages(sessionKey: string, rootId?: string, nodeId?: string): Promise<MaintenanceCleanResult[]> {
  return postJSON<MaintenanceCleanResult[]>(
    `/api/sessions/${encodeURIComponent(sessionKey)}/maintenance/images/clean`,
    rootId,
    {},
    nodeId,
  );
}

/** 删空会话是破坏性动作：服务端只在 confirm 为真时执行，非空会话会返回可读错误。 */
export function purgeEmptySession(sessionKey: string, rootId?: string, nodeId?: string): Promise<MaintenanceCleanResult[]> {
  return postJSON<MaintenanceCleanResult[]>(
    `/api/sessions/${encodeURIComponent(sessionKey)}/maintenance/purge`,
    rootId,
    { confirm: true },
    nodeId,
  );
}

export function fetchRuntimeIssues(rootId: string, nodeId?: string): Promise<RuntimeIssueReport> {
  return protectedJSON<RuntimeIssueReport>(maintenanceURL("/api/sessions/runtime-issues", rootId, nodeId));
}

export function sweepRuntimeIssues(rootId: string, nodeId?: string): Promise<RuntimeSweepReport> {
  return postJSON<RuntimeSweepReport>("/api/sessions/runtime-issues/sweep", rootId, { apply: true }, nodeId);
}

/**
 * 换目录能力。后端现在没有单独的 capability 端点：有 root 且会话空闲才显示入口，
 * 其它 harness（codex 等）与不支持的会话由服务端返回可读错误，前端不提前拦截、
 * 也不再写死 agent === "claude"。
 */
export function canRepointSession(target: Pick<SessionMaintenanceTarget, "rootId" | "running"> | null): boolean {
  return !!target?.rootId && !target.running;
}
