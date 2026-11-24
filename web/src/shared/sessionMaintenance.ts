// 会话维护的词汇、分组与摘要。
//
// 放在 shared 的理由和 syncConflicts.ts 相同：视图不能 import services，而「哪些会话
// 要提示」「清理前后怎么算」「事件从哪来」既是渲染依据、也是用例钉的对象，必须是纯函数。
import type { MessageKey } from "../i18n";

/** 与后端 sessionmaintenance.SessionReport 一一对应。 */
export type MaintenanceEntry = {
  session_key: string;
  agent: string;
  agent_session_id?: string;
  path?: string;
  bytes: number;
  lines: number;
  image_bytes: number;
  runtime_path?: string;
  runtime_missing: boolean;
  unsupported?: string;
  error?: string;
};

export type MaintenanceReport = {
  sessions: MaintenanceEntry[];
  stale_locks?: string[];
  scanned: number;
};

export type MaintenanceCleanResult = {
  path: string;
  removed_images?: number;
  reclaimed_bytes?: number;
  backup_path?: string;
  purged?: boolean;
};

export type MaintenanceLockResult = {
  removed?: string[];
  failed?: string[];
};

/** 与后端 RuntimeIssueView 一一对应。 */
export type RuntimeIssue = {
  session_key: string;
  agent: string;
  path: string;
  class: string;
  root_id?: string;
};

export type RuntimeIssueReport = {
  scanned: number;
  issues: RuntimeIssue[];
};

export type RuntimeSweepReport = {
  scanned: number;
  repaired: number;
  skipped: number;
  failed: number;
  details?: string[];
};

export const SESSION_MAINTENANCE_EVENT = "mindfs:session-maintenance";
export const SESSION_HEALTH_EVENT = "mindfs:session-health";

export type SessionMaintenanceTarget = {
  sessionKey: string;
  rootId?: string;
  nodeId?: string;
  agent?: string;
  running?: boolean;
};

/** 一个会话只落一个分组；优先级按「越需要处理越靠前」排。 */
export const MAINTENANCE_GROUP_ORDER = ["attention", "images", "runtime", "clean"] as const;
export type MaintenanceGroupKey = (typeof MAINTENANCE_GROUP_ORDER)[number];

const GROUP_LABELS: Record<MaintenanceGroupKey, MessageKey> = {
  attention: "sessionMaintenance.group.attention",
  images: "sessionMaintenance.group.images",
  runtime: "sessionMaintenance.group.runtime",
  clean: "sessionMaintenance.group.clean",
};

export type MaintenanceGroup = {
  key: MaintenanceGroupKey;
  label: MessageKey;
  entries: MaintenanceEntry[];
};

export function maintenanceGroupKey(entry: MaintenanceEntry): MaintenanceGroupKey {
  if (entry.error || entry.unsupported) return "attention";
  if (entry.runtime_missing) return "runtime";
  if (entry.image_bytes > 0) return "images";
  return "clean";
}

/** 空组不返回：面板只渲染有内容的组。 */
export function groupMaintenanceEntries(report: MaintenanceReport | null): MaintenanceGroup[] {
  const byKey = new Map<MaintenanceGroupKey, MaintenanceEntry[]>();
  for (const entry of report?.sessions ?? []) {
    const key = maintenanceGroupKey(entry);
    const list = byKey.get(key);
    if (list) list.push(entry);
    else byKey.set(key, [entry]);
  }
  return MAINTENANCE_GROUP_ORDER.flatMap((key) => {
    const entries = byKey.get(key);
    return entries?.length ? [{ key, label: GROUP_LABELS[key], entries }] : [];
  });
}

/** 字节只用于展示，单位保持十进制以免把 1000 与 1024 混着读。 */
export function formatMaintenanceBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return "—";
  const units = ["B", "KB", "MB", "GB"];
  let value = bytes;
  let unit = 0;
  while (value >= 1000 && unit < units.length - 1) {
    value /= 1000;
    unit += 1;
  }
  const text = unit === 0 ? String(Math.round(value)) : value >= 100 ? value.toFixed(0) : value.toFixed(1);
  return `${text} ${units[unit]}`;
}

export type MaintenanceCapabilities = {
  cleanImages: boolean;
  purgeEmpty: boolean;
};

/**
 * 服务端没给单会话 capability 端点，这里只按诊断结果判断「有没有可做的事」；
 * 真正能否执行由服务端复核并返回可读错误（例如空会话非空、不支持的 harness）。
 */
export function maintenanceCapabilities(entry: MaintenanceEntry): MaintenanceCapabilities {
  const blocked = !!entry.error || !!entry.unsupported;
  return {
    cleanImages: !blocked && entry.image_bytes > 0,
    // 诊断不带「是否真为空」，按钮常驻但文案写明只删空会话，confirm 由服务端把关。
    purgeEmpty: !blocked,
  };
}

export function maintenanceCapabilityKeys(entry: MaintenanceEntry, canRepoint: boolean): MessageKey[] {
  const capabilities = maintenanceCapabilities(entry);
  const keys: MessageKey[] = ["sessionMaintenance.cap.diagnose"];
  if (capabilities.cleanImages) keys.push("sessionMaintenance.cap.cleanImages");
  if (capabilities.purgeEmpty) keys.push("sessionMaintenance.cap.purgeEmpty");
  if (canRepoint) keys.push("sessionMaintenance.cap.repoint");
  return keys;
}

export function shortSessionKey(sessionKey: string): string {
  const tail = sessionKey.split("/").filter(Boolean).pop() ?? sessionKey;
  return tail.length > 24 ? `${tail.slice(0, 24)}…` : tail;
}

/** 相同路径可能被多次清理；按结果累加，不让「回收前后」被重复计数。 */
export function maintenanceReclaim(beforeBytes: number, results: readonly MaintenanceCleanResult[]) {
  const reclaimed = results.reduce((sum, result) => sum + Math.max(0, result.reclaimed_bytes ?? 0), 0);
  const removedImages = results.reduce((sum, result) => sum + Math.max(0, result.removed_images ?? 0), 0);
  return {
    before: beforeBytes,
    after: Math.max(0, beforeBytes - reclaimed),
    reclaimed,
    removedImages,
    purged: results.some((result) => result.purged),
  };
}

const RUNTIME_CLASS_LABELS: Record<string, MessageKey> = {
  "worktree-missing": "sessionHealth.class.worktree-missing",
  "workdir-missing": "sessionHealth.class.workdir-missing",
};

export function runtimeIssueClassLabel(className: string): MessageKey {
  return RUNTIME_CLASS_LABELS[className] ?? "sessionHealth.class.other";
}

export type RuntimeIssueGroup = {
  className: string;
  label: MessageKey;
  rootIds: string[];
  issues: RuntimeIssue[];
};

/** 按类别分组、组内按项目与会话键排序，项目多时顺序依然稳定。 */
export function groupRuntimeIssues(issues: readonly RuntimeIssue[]): RuntimeIssueGroup[] {
  const byClass = new Map<string, RuntimeIssue[]>();
  for (const issue of issues) {
    const list = byClass.get(issue.class);
    if (list) list.push(issue);
    else byClass.set(issue.class, [issue]);
  }
  return [...byClass.entries()]
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([className, list]) => {
      const sorted = [...list].sort(
        (a, b) => (a.root_id ?? "").localeCompare(b.root_id ?? "") || a.session_key.localeCompare(b.session_key),
      );
      return {
        className,
        label: runtimeIssueClassLabel(className),
        rootIds: [...new Set(sorted.map((issue) => issue.root_id).filter(Boolean))] as string[],
        issues: sorted,
      };
    });
}
