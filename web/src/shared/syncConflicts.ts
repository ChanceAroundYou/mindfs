// 同步冲突卡的词汇、分组与摘要。
//
// 放在 shared 的理由和 controlMode.ts 相同：视图不能 import services，而「哪个域
// 排第几」「users 域给不给手动合并」既是渲染依据、也是用例钉的对象，必须是纯函数。
import type { MessageKey } from "../i18n";

export type SyncConflictSide = "local" | "remote";

/** 与 spec §5 的同步域表一致；未知域排最后并按字典序，新域不会插队。 */
export const SYNC_CONFLICT_DOMAIN_ORDER = [
  "users",
  "preferences",
  "nodes",
  "prompts",
  "pins",
  "workflows",
] as const;

const DOMAIN_LABELS: Record<string, MessageKey> = {
  users: "syncConflict.domain.users",
  preferences: "syncConflict.domain.preferences",
  nodes: "syncConflict.domain.nodes",
  prompts: "syncConflict.domain.prompts",
  pins: "syncConflict.domain.pins",
  workflows: "syncConflict.domain.workflows",
};

// 与服务端 ConflictView 一一对应。载荷只在非 users 域下发，但前端仍按域兜底。
export type SyncConflictView = {
  id: string;
  domain: string;
  key: string;
  local_hash?: string;
  remote_hash?: string;
  local_deleted?: boolean;
  remote_deleted?: boolean;
  redacted?: boolean;
  local?: unknown;
  remote?: unknown;
  created_at?: string;
};

export type SyncConflictChoice = "local" | "primary" | "payload";

export type SyncConflictGroup = {
  domain: string;
  /** 已知域给文案键；未知域为 null，视图直接显示域名。 */
  label: MessageKey | null;
  conflicts: SyncConflictView[];
};

export type SyncConflictSummary =
  | { kind: "deleted"; text: "" }
  | { kind: "redacted"; text: "" }
  | { kind: "missing"; text: "" }
  | { kind: "payload"; text: string };

const PAYLOAD_PREVIEW_LIMIT = 600;

export function groupSyncConflicts(conflicts: readonly SyncConflictView[]): SyncConflictGroup[] {
  const byDomain = new Map<string, SyncConflictView[]>();
  for (const conflict of conflicts) {
    const list = byDomain.get(conflict.domain);
    if (list) list.push(conflict);
    else byDomain.set(conflict.domain, [conflict]);
  }
  const rank = (domain: string) => {
    const index = (SYNC_CONFLICT_DOMAIN_ORDER as readonly string[]).indexOf(domain);
    return index === -1 ? Number.MAX_SAFE_INTEGER : index;
  };
  return [...byDomain.entries()]
    .sort(([left], [right]) => rank(left) - rank(right) || left.localeCompare(right))
    .map(([domain, list]) => ({
      domain,
      label: DOMAIN_LABELS[domain] ?? null,
      conflicts: list,
    }));
}

/**
 * users 域只允许「保留本地 / 采用主节点」：载荷里是口令 hash，让人手改等于把
 * 敏感字段当普通 JSON 编辑。判断按域做（不依赖服务端有没有带 redacted 标记），
 * 两边都拦一层。
 */
export function conflictChoiceKeys(view: SyncConflictView): SyncConflictChoice[] {
  if (view.domain === "users" || view.redacted) return ["local", "primary"];
  return ["local", "primary", "payload"];
}

export function conflictSideSummary(
  view: SyncConflictView,
  side: SyncConflictSide,
): SyncConflictSummary {
  const deleted = side === "local" ? view.local_deleted : view.remote_deleted;
  // 删除标记不含载荷，可以先说 —— 比笼统地写「密码已变更」准确。
  if (deleted) return { kind: "deleted", text: "" };
  if (view.domain === "users" || view.redacted) return { kind: "redacted", text: "" };

  const value = side === "local" ? view.local : view.remote;
  if (value === undefined) return { kind: "missing", text: "" };
  const text = JSON.stringify(value, null, 2) ?? "";
  return {
    kind: "payload",
    text: text.length > PAYLOAD_PREVIEW_LIMIT ? `${text.slice(0, PAYLOAD_PREVIEW_LIMIT)}…` : text,
  };
}