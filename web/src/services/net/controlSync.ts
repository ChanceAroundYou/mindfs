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
import {
  isControlMode,
  type ControlModeEvent,
  type ControlSyncAction,
  type ControlSyncStatus,
} from "../../shared/controlMode";
import type { SyncConflictChoice, SyncConflictView } from "../../shared/syncConflicts";
import {
  toPairingRequest,
  type PairingDraft,
} from "../../shared/controlPairing";

export type { ControlMode, ControlSyncAction, ControlSyncStatus } from "../../shared/controlMode";
export type { SyncConflictChoice, SyncConflictView } from "../../shared/syncConflicts";

function toEvents(value: unknown): ControlModeEvent[] {
  if (!Array.isArray(value)) return [];
  const events: ControlModeEvent[] = [];
  for (const item of value) {
    if (!item || typeof item !== "object") continue;
    const record = item as Record<string, unknown>;
    if (
      typeof record.id !== "string" ||
      typeof record.at !== "string" ||
      !isControlMode(record.from) ||
      !isControlMode(record.to)
    ) {
      continue;
    }
    events.push({
      id: record.id,
      from: record.from,
      to: record.to,
      at: record.at,
      ...(typeof record.reason === "string" ? { reason: record.reason } : {}),
    });
  }
  return events;
}

/** 后端 /status 的完整投影；少带一个字段，横幅就会少一种能力。 */
export function toControlSyncStatus(payload: unknown): ControlSyncStatus | null {
  if (!payload || typeof payload !== "object") return null;
  const record = payload as Record<string, unknown>;
  if (!isControlMode(record.mode)) return null;
  return {
    mode: record.mode,
    base_rev: typeof record.base_rev === "number" ? record.base_rev : undefined,
    primary_reachable:
      typeof record.primary_reachable === "boolean" ? record.primary_reachable : undefined,
    pending_count: typeof record.pending_count === "number" ? record.pending_count : undefined,
    conflict_count: typeof record.conflict_count === "number" ? record.conflict_count : undefined,
    blocked_reason:
      typeof record.blocked_reason === "string" ? record.blocked_reason : undefined,
    blocked_code: typeof record.blocked_code === "string" ? record.blocked_code : undefined,
    last_primary_at:
      typeof record.last_primary_at === "string" ? record.last_primary_at : undefined,
    last_error: typeof record.last_error === "string" ? record.last_error : undefined,
    recent_events: toEvents(record.recent_events),
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
    return toControlSyncStatus(payload);
  } catch (err) {
    if (endpointMissing(err)) return null;
    throw err;
  }
}

/**
 * 读配对状态。返回 null = 本机不是「等着配对的边缘」。
 *
 * 这条探针本身只回 `pending_pairing` 与实例名，不回任何路径或密钥。
 * 它存在的唯一理由：未配对时 `/status` 也不注册，前端否则无从区分
 * 「等着配对」与「这台机器根本不同步」。
 */
export async function fetchControlSyncPairing(): Promise<ControlSyncPairingState | null> {
  try {
    const payload = await protectedJSON<unknown>(controlPath("/api/control-sync/pair"));
    if (!payload || typeof payload !== "object") return null;
    const record = payload as Record<string, unknown>;
    if (record.pending_pairing !== true) return null;
    return {
      pending_pairing: true,
      instance_id: typeof record.instance_id === "string" ? record.instance_id : undefined,
    };
  } catch (err) {
    if (endpointMissing(err)) return null;
    throw err;
  }
}

/**
 * 提交配对。
 *
 * 失败不抛异常，而是分类返回：三条失败路径对用户是三种完全不同的动作 ——
 * 「去那台机器上打开页面」「检查主控端地址/网络」「改 token」，都交给视图层
 * 直接说，不要再让它们各自去解 HTTP 状态码。
 *
 * 403 不是错误而是设计：配对会改信任锚、写通道密钥、改 config.json，所以服务端
 * 只接受本机回环来源。经反代从外部打开页面时一定会撞到它。
 */
export async function pairControlSync(draft: PairingDraft): Promise<PairControlSyncOutcome> {
  try {
    const payload = await protectedJSON<unknown>(controlPath("/api/control-sync/pair"), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(toPairingRequest(draft)),
    });
    return { ok: true, result: toPairResult(payload) };
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err);
    if (!(err instanceof APIError)) return { ok: false, kind: "rejected", message };
    switch (err.status) {
      case 403:
        return { ok: false, kind: "not-local", message };
      case 404:
        return { ok: false, kind: "unsupported", message };
      case 502:
        return { ok: false, kind: "unreachable", message };
      default:
        return { ok: false, kind: "rejected", message };
    }
  }
}

function toPairResult(payload: unknown): ControlSyncPairResult {
  if (!payload || typeof payload !== "object") return { restart_required: true };
  const record = payload as Record<string, unknown>;
  return {
    // 服务端恒回 true（路由在启动期装配），缺字段时按 true 处理：
    // 显示「已连接，无需重启」而实际还在未配对状态是最糟的方向。
    restart_required: record.restart_required !== false,
    instance_id: typeof record.instance_id === "string" ? record.instance_id : undefined,
    primary_url: typeof record.primary_url === "string" ? record.primary_url : undefined,
    primary_version:
      typeof record.primary_version === "string" ? record.primary_version : undefined,
    head_rev: typeof record.head_rev === "number" ? record.head_rev : undefined,
  };
}

export type ControlSyncPairingState = {
  pending_pairing: boolean;
  instance_id?: string;
};

export type ControlSyncPairResult = {
  restart_required: boolean;
  instance_id?: string;
  primary_url?: string;
  primary_version?: string;
  head_rev?: number;
};

export type PairControlSyncOutcome =
  | { ok: true; result: ControlSyncPairResult }
  | { ok: false; kind: "not-local" | "unsupported" | "unreachable" | "rejected"; message: string };

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
    return toControlSyncStatus(payload);
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

export type SyncConflictPayloadEdit = { id: string; payload: unknown };

/**
 * 批量自定义合并：每条卡带自己的 payload，绝不把同一份 JSON 套到不同 key 上。
 * 和 local/primary 批量走同一条串行路径与计数口径；任何一条失败都不阻断其余。
 */
export async function resolveConflictPayloadEdits(
  edits: readonly SyncConflictPayloadEdit[],
): Promise<SyncConflictBatchResult> {
  let resolved = 0;
  let failed = 0;
  for (const edit of edits) {
    try {
      await resolveSyncConflict(edit.id, "payload", edit.payload);
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