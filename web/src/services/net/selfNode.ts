// 本机自报身份的加载入口：拉 /api/node-info，写入 shared/selfNodeStore，跑旧 id 迁移。
//
// 来源：主控 home/主控；边缘取 control_sync.instance_id 与安装时写下的节点名。
// 它决定注册表里 self 节点的 id/name、作用域键的节点段、以及节点表里旧 `local` 行的迁移目标。
//
// 失败时退回缓存值，再退回旧 id `local` —— 刻意保留一条兼容读：
// 后端还没升级或页面离线时，行为与改造前一致，不会把本机认成陌生节点。

import { controlPath } from "./controlPlane";
import { migrateLegacyLocalNodeId } from "../../shared/nodeIdentityMigration";
import {
  LEGACY_SELF_NODE_ID,
  getSelfNodeIdentity,
  setSelfNodeIdentity,
  type SelfNodeIdentity,
} from "../../shared/selfNodeStore";

export {
  LEGACY_SELF_NODE_ID,
  getSelfNodeId,
  getSelfNodeIdentity,
  getSelfNodeName,
  isSelfNode,
  isSelfNodeConnection,
  type SelfNodeIdentity,
} from "../../shared/selfNodeStore";

let initPromise: Promise<SelfNodeIdentity | null> | null = null;

async function fetchSelfIdentity(timeoutMs: number): Promise<SelfNodeIdentity | null> {
  const controller = typeof AbortController !== "undefined" ? new AbortController() : null;
  const timer = controller ? setTimeout(() => controller.abort(), timeoutMs) : null;
  try {
    const res = await fetch(controlPath("/api/node-info"), {
      cache: "no-store",
      signal: controller?.signal,
    });
    if (!res.ok) return null;
    const body = await res.json();
    const id = String(body?.id || "").trim();
    if (!id || id === LEGACY_SELF_NODE_ID) return null;
    return { id, name: String(body?.name || "").trim() || id };
  } catch {
    return null;
  } finally {
    if (timer) clearTimeout(timer);
  }
}

/**
 * 拉一次本机身份并执行旧 id 迁移。幂等：并发的调用共用同一个在途 promise。
 *
 * 失败时返回缓存身份（可能为 null），并把 promise 清掉，让下次同步再试 ——
 * 页面先离线后恢复是常态，一次失败不该把身份永久钉死。
 */
export function initSelfNodeIdentity(): Promise<SelfNodeIdentity | null> {
  if (initPromise) return initPromise;
  initPromise = (async () => {
    const fetched = await fetchSelfIdentity(4000);
    if (fetched) {
      const stored = setSelfNodeIdentity(fetched.id, fetched.name);
      if (stored) migrateLegacyLocalNodeId(stored.id);
      return stored;
    }
    initPromise = null;
    return getSelfNodeIdentity();
  })();
  return initPromise;
}
