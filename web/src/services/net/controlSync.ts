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

export type { ControlMode, ControlSyncAction, ControlSyncStatus } from "../../shared/controlMode";

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