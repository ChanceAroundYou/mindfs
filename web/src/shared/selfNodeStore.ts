// 本机身份的进程内状态。
//
// 取值来自 /api/node-info（见 services/net/selfNode.ts）；放在 shared 是因为
// 组件层要判断「这个节点是不是本机」，而组件不允许直接 import services
//（web/tests/reviewer-architecture-rules.test.mjs 钉住）。
//
// 为什么需要它：`local` 曾代表「当前页面所在设备」，主控和边缘都叫这个名字；
// 现在每台机器自报 id（主控 home/主控，边缘取 instance_id），比较必须走 isSelfNode，
// 不能再写死字符串。

export const LEGACY_SELF_NODE_ID = "local";

const IDENTITY_CACHE_KEY = "mindfs_self_node";

export type SelfNodeIdentity = {
  id: string;
  name: string;
};

let identity: SelfNodeIdentity | null = readCachedIdentity();

function readCachedIdentity(): SelfNodeIdentity | null {
  try {
    const raw = window.localStorage.getItem(IDENTITY_CACHE_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw);
    const id = String(parsed?.id || "").trim();
    if (!id || id === LEGACY_SELF_NODE_ID) return null;
    return { id, name: String(parsed?.name || "").trim() || id };
  } catch {
    return null;
  }
}

/** 已知的身份；null = 还没拿到过（此时 id 退回 `local`，与改造前行为一致）。 */
export function getSelfNodeIdentity(): SelfNodeIdentity | null {
  return identity;
}

/** 作用域键/路由用的本机 id。 */
export function getSelfNodeId(): string {
  return identity?.id || LEGACY_SELF_NODE_ID;
}

/** 展示用的本机名字（托盘、标题、缺失时回退）。 */
export function getSelfNodeName(): string {
  return identity?.name || LEGACY_SELF_NODE_ID;
}

/**
 * 这个 id 是不是本机。
 *
 * 旧 id `local` 一律当本机：迁移是懒执行的，页面早期读到的仍是旧键；
 * 把它当陌生节点会让本机在列表里出现两次、且路由到错误的机器。
 */
export function isSelfNode(nodeId: string | null | undefined): boolean {
  const id = String(nodeId || "").trim();
  if (!id) return false;
  if (id === LEGACY_SELF_NODE_ID) return true;
  if (!identity) return false;
  return id.toLowerCase() === identity.id.toLowerCase();
}

/** 写入身份（由 services/net/selfNode.ts 在拉到 node-info 后调用）。 */
export function setSelfNodeIdentity(id: string, name: string): SelfNodeIdentity | null {
  const nextId = String(id || "").trim();
  if (!nextId || nextId === LEGACY_SELF_NODE_ID) return identity;
  identity = { id: nextId, name: String(name || "").trim() || nextId };
  try {
    window.localStorage.setItem(IDENTITY_CACHE_KEY, JSON.stringify(identity));
  } catch {}
  return identity;
}
