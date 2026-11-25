// 身份迁移：把旧伪节点 id `local` 换成 /api/node-info 自报的本机 id。
//
// 为什么需要迁移：`local` 曾代表「当前页面所在设备」，主控和边缘都叫这个名字。
// 现在每台机器自报身份（主控 home/主控，边缘取 instance_id），作用域键里的
// 节点段也要跟着换，否则旧数据在所有页面上「消失」——不是丢了，是键对不上。
//
// 迁移原则：
//   - 幂等：版本键写在 localStorage，跑过一次就不再碰；
//   - 逐段替换：作用域键同时有 "::"（根/会话）与 ":"（目录）两种分隔，
//     只替换**节点段**，不做全局字符串替换（路径/会话 key 里可能出现 local）；
//   - 结构化：JSON 里只改对象键与字符串数组元素，不碰普通字符串值
//     （用户正文里写着 "local::" 也不能被改）。唯一的例外是已知的
//     「值就是一个节点 id」的键，见 PLAIN_NODE_ID_KEYS。
//   - 目标键已存在时不覆盖：新数据优先，旧条目只补缺失的部分。

export const LEGACY_SELF_NODE_ID = "local";

const MIGRATION_VERSION_KEY = "mindfs-node-id-migrated-v1";

/** 值本身就是节点 id 的存储键。账户分区会在后面接 `::<username>`。 */
const PLAIN_NODE_ID_KEYS = ["mindfs_active_node_id", "mindfs-last-root-node"];

/**
 * 只替换**节点段**：作用域键的第一个段，可能在字符串开头，也可能跟在
 * 一个存储键前缀（如 `vp-progress:`、`mindfs.git.history.files:`）之后。
 *
 * 为什么不能全局替换：会话 key 自己也带 "::"，`local::root::v2::local::x`
 * 里的第二个 local 是会话 key 的一部分，改了就会指向另一个会话；
 * 路径段同理。git 缓存还会把作用域键 encodeURIComponent 成 `local%3A%3Aroot`。
 */
export function rewriteNodeSegment(raw: string, selfId: string): string {
  if (!raw || !selfId || selfId === LEGACY_SELF_NODE_ID) return raw;
  // 裸作用域键：local::root / local::root:path / local%3A%3Aroot（编码形态）
  if (/^local(?=::|:|%3A)/i.test(raw)) return selfId + raw.slice(LEGACY_SELF_NODE_ID.length);
  // 带存储键前缀：<prefix>:local::root…
  const prefixed = raw.match(/^([^:]+:)local(?=::|:|%3A)/i);
  if (prefixed) return `${prefixed[1]}${selfId}${raw.slice(prefixed[0].length)}`;
  return raw;
}

function rewriteJsonValue(value: unknown, selfId: string): unknown {
  if (Array.isArray(value)) {
    // 重写后可能撞出重复（local::x 与 home::x 同时存在），去重保留先出现的：
    // 这些数组是键集合，重复项没有语义。
    const seen = new Set<string>();
    const out: unknown[] = [];
    for (const item of value) {
      const next = typeof item === "string" ? rewriteNodeSegment(item, selfId) : rewriteJsonValue(item, selfId);
      const marker = typeof next === "string" ? `s:${next}` : JSON.stringify(next);
      if (seen.has(marker)) continue;
      seen.add(marker);
      out.push(next);
    }
    return out;
  }
  if (value && typeof value === "object") {
    const out: Record<string, unknown> = {};
    for (const [key, item] of Object.entries(value as Record<string, unknown>)) {
      out[rewriteNodeSegment(key, selfId)] = rewriteJsonValue(item, selfId);
    }
    return out;
  }
  return value;
}

function plainNodeIdKey(base: string): boolean {
  return PLAIN_NODE_ID_KEYS.some((key) => base === key || base.startsWith(`${key}::`));
}

/** 合并旧值进已存在的新键：对象补缺失项，数组取并集，其余保留新值。 */
function mergeExisting(current: string, legacy: string): string {
  try {
    const next = JSON.parse(current);
    const old = JSON.parse(legacy);
    if (Array.isArray(next) && Array.isArray(old)) {
      return JSON.stringify(Array.from(new Set([...next, ...old])));
    }
    if (next && old && typeof next === "object" && typeof old === "object" && !Array.isArray(next) && !Array.isArray(old)) {
      return JSON.stringify({ ...(old as object), ...(next as object) });
    }
  } catch {}
  return current;
}

/** 在给定 storage 上执行迁移；返回真正被改写的条目数（测试用）。 */
export function migrateLegacyLocalNodeId(selfId: string, storage?: Storage): number {
  const store = storage ?? (typeof window !== "undefined" ? window.localStorage : null);
  if (!store) return 0;
  const targetId = String(selfId || "").trim();
  if (!targetId || targetId === LEGACY_SELF_NODE_ID) return 0;
  if (store.getItem(MIGRATION_VERSION_KEY) === targetId) return 0;

  const keys: string[] = [];
  for (let i = 0; i < store.length; i += 1) {
    const key = store.key(i);
    if (key) keys.push(key);
  }

  let changed = 0;
  for (const key of keys) {
    const raw = store.getItem(key);
    if (raw === null) continue;

    // 1) 值本身就是节点 id 的键
    if (plainNodeIdKey(key) && raw.trim() === LEGACY_SELF_NODE_ID) {
      store.setItem(key, targetId);
      changed += 1;
      continue;
    }

    // 2) JSON 结构：改对象键与字符串数组元素
    let value = raw;
    if (raw.trim().startsWith("{") || raw.trim().startsWith("[")) {
      try {
        value = JSON.stringify(rewriteJsonValue(JSON.parse(raw), targetId));
      } catch {
        value = raw;
      }
    }

    // 3) 键本身可能含作用域（插件查询进度、文件滚动位置……）
    const nextKey = rewriteNodeSegment(key, targetId);
    if (nextKey === key && value === raw) continue;

    if (nextKey !== key) {
      const existing = store.getItem(nextKey);
      if (existing !== null) {
        store.setItem(nextKey, mergeExisting(existing, value));
        store.removeItem(key);
      } else {
        store.setItem(nextKey, value);
        store.removeItem(key);
      }
      changed += 1;
      continue;
    }
    if (value !== raw) {
      store.setItem(key, value);
      changed += 1;
    }
  }

  store.setItem(MIGRATION_VERSION_KEY, targetId);
  return changed;
}
