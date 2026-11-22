// 控制面同步（Edge Control Replica）的前端读口。
//
// 这些端点只有「边缘」节点在启用 control_sync 时才注册；主节点、worker、以及
// 关掉开关的机器一律 404。对前端来说 404 不是故障，而是「本机没有这个能力」——
// 必须静默隐藏横幅，绝不能在每个普通节点上弹一条永远失败的请求。
//
// 请求走 controlPath（页面服务器），与账户/偏好等控制面请求同源：同步状态描述的
// 就是「页面服务器这台控制面」的状态，跟随选中节点去打另一台机器毫无意义。
//
// 模式词汇与纯映射在 shared/controlMode.ts（视图层也要用），这里只管网络。
import { APIError, protectedJSON } from "./api";
import { controlPath } from "./controlPlane";
import { isControlMode, type ControlSyncAction, type ControlSyncStatus } from "../../shared/controlMode";
import type { SyncConflictChoice, SyncConflictView } from "../../shared/syncConflicts";

export type { ControlMode, ControlSyncAction, ControlSyncStatus } from "../../shared/controlMode";
export type { SyncConflictChoice, SyncConflictView } from "../../shared/syncConflicts";

function toStatus(payload: unknown): ControlSyncStatus | null {
  if (!payload || typeof payload !== "object") return null;
  const record = payload as Record<string, unknown>;
  if (!isControlMode(record.mode)) return null;
  return {
    mode: record.mode,
    base_rev: typeof record.base_rev === "number" ? record.base_rev : undefined,
    last_primary_at:
      typeof record.last_primary_at === "string" ? record.last_primary_at : undefined,
    last_error: typeof record.last_error === "string" ? record.last_error : undefined,
  };
}

function endpointMissing(err: unknown): boolean {
  return err instanceof APIError && err.status === 404;
}

/**
 * 读同步状态。返回 null = 本机没有注册同步端点（主节点 / 未启用），调用方应停表。
 * 其他错误照常抛出：网络抖动与「没有这个能力」必须能被区分开，否则一次超时
 * 就会把边缘的横幅永久关掉。
 */
export async function fetchControlSyncStatus(): Promise<ControlSyncStatus | null> {
  try {
    const payload = await protectedJSON<unknown>(controlPath("/api/control-sync/status"));
    return toStatus(payload);
  } catch (err) {
    if (endpointMissing(err)) return null;
    throw err;
  }
}

/**
 * 显式切换 / 立即重试。retry 探不通不算失败：服务端仍回 200，状态里的
 * last_error 写明原因，和下一拍轮询看到的完全一致。
 */
export async function switchControlSyncMode(
  action: ControlSyncAction,
): Promise<ControlSyncStatus | null> {
  try {
    const payload = await protectedJSON<unknown>(controlPath("/api/control-sync/mode"), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ action }),
    });
    return toStatus(payload);
  } catch (err) {
    if (endpointMissing(err)) return null;
    throw err;
  }
}

function toConflict(value: unknown): SyncConflictView | null {
  if (!value || typeof value !== "object") return null;
  const record = value as Record<string, unknown>;
  if (
    typeof record.id !== "string" ||
    typeof record.domain !== "string" ||
    typeof record.key !== "string"
  ) {
    return null;
  }
  return {
    id: record.id,
    domain: record.domain,
    key: record.key,
    local_hash: typeof record.local_hash === "string" ? record.local_hash : undefined,
    remote_hash: typeof record.remote_hash === "string" ? record.remote_hash : undefined,
    local_deleted: record.local_deleted === true,
    remote_deleted: record.remote_deleted === true,
    redacted: record.redacted === true,
    local: record.local,
    remote: record.remote,
    created_at: typeof record.created_at === "string" ? record.created_at : undefined,
  };
}

/**
 * 读冲突卡。返回 null = 本机没有注册冲突端点（与状态端点同一套判定）。
 * 载荷形状不对时抛错而不是回空列表：空列表在界面上等于「没有冲突」，
 * 会把服务端的形状错误伪装成好消息。
 */
export async function fetchSyncConflicts(): Promise<SyncConflictView[] | null> {
  try {
    const payload = await protectedJSON<unknown>(controlPath("/api/control-sync/conflicts"));
    const list =
      payload && typeof payload === "object"
        ? (payload as Record<string, unknown>).conflicts
        : undefined;
    if (!Array.isArray(list)) {
      throw new Error("invalid conflicts payload");
    }
    return list
      .map(toConflict)
      .filter((conflict): conflict is SyncConflictView => conflict !== null);
  } catch (err) {
    if (endpointMissing(err)) return null;
    throw err;
  }
}

/**
 * 裁决一条冲突。id 必须整体转义：prompts 的 key 是 `<账户>/sha256:<hash>`、
 * nodes 的 key 是 URL，两者都自带 `/`，不转义会多出一段路径。
 */
export async function resolveSyncConflict(
  id: string,
  choice: SyncConflictChoice,
  payload?: unknown,
): Promise<void> {
  const body: Record<string, unknown> = { choice };
  if (choice === "payload") {
    body.payload = payload;
  }
  await protectedJSON<unknown>(
    controlPath(`/api/control-sync/conflicts/${encodeURIComponent(id)}/resolve`),
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    },
  );
}

export type SyncConflictBatchResult = { resolved: number; failed: number };

/**
 * 批量裁决：逐条发送，一次确认触发一次提交。
 *
 * 服务端只有逐条端点（每条各自 CAS），没有批量接口 —— 这里不发明一个 spec 之外的
 * 批量 API，而是把「一次提交」留在视图层：确认一次、串行发完、逐条汇报结果。
 * 404 / 409 表示这张卡已被另一端裁决或已不存在，目标已达成，按成功计。
 */
export async function resolveSyncConflicts(
  conflicts: readonly SyncConflictView[],
  choice: Exclude<SyncConflictChoice, "payload">,
): Promise<SyncConflictBatchResult> {
  let resolved = 0;
  let failed = 0;
  for (const conflict of conflicts) {
    try {
      await resolveSyncConflict(conflict.id, choice);
      resolved += 1;
    } catch (err) {
      if (err instanceof APIError && (err.status === 404 || err.status === 409)) {
        resolved += 1;
      } else {
        failed += 1;
      }
    }
  }
  return { resolved, failed };
}