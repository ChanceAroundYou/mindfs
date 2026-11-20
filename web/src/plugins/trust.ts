export type PluginSourceRecord = {
  path: string;
  sha256: string;
  size: number;
};

export type PluginSourceSnapshot = {
  rootPath: string;
  plugins: PluginSourceRecord[];
};

export type TrustedPluginSet = PluginSourceSnapshot & {
  trustedAt: string;
};

const TRUST_STORAGE_PREFIX = "mindfs-plugin-trust:";

/**
 * 信任记录的存储键。
 *
 * 带节点段：fn 与 pc 各有一个 `docs` 时，在一台机器上批准过的插件不该顺带
 * 批准另一台的（内容可能完全不同）。不传 nodeId 保留历史裸键，
 * `readTrustedPluginSet` 在带节点查不到时会回退读它（老数据零迁移）。
 */
export function pluginTrustStorageKey(rootId: string, nodeId?: string): string {
  const nid = String(nodeId || "").trim();
  const rid = String(rootId || "");
  return `${TRUST_STORAGE_PREFIX}${nid ? `${nid}::${rid}` : rid}`;
}

export function normalizePluginSnapshot(snapshot: PluginSourceSnapshot): PluginSourceSnapshot {
  return {
    rootPath: String(snapshot.rootPath || ""),
    plugins: [...snapshot.plugins]
      .map((plugin) => ({
        path: String(plugin.path || ""),
        sha256: String(plugin.sha256 || ""),
        size: Number(plugin.size) || 0,
      }))
      .sort((a, b) => a.path.localeCompare(b.path)),
  };
}

export function buildPluginTrustRecord(
  snapshot: PluginSourceSnapshot,
  trustedAt = new Date().toISOString(),
): TrustedPluginSet {
  return {
    ...normalizePluginSnapshot(snapshot),
    trustedAt,
  };
}

export function isPluginSnapshotTrusted(
  snapshot: PluginSourceSnapshot,
  trusted: TrustedPluginSet | null | undefined,
): boolean {
  if (!trusted) return false;
  const current = normalizePluginSnapshot(snapshot);
  const saved = normalizePluginSnapshot(trusted);
  if (current.rootPath !== saved.rootPath) return false;
  if (current.plugins.length !== saved.plugins.length) return false;
  return current.plugins.every((plugin, index) => {
    const other = saved.plugins[index];
    return plugin.path === other.path && plugin.sha256 === other.sha256 && plugin.size === other.size;
  });
}

export function readTrustedPluginSet(rootId: string, nodeId?: string): TrustedPluginSet | null {
  if (typeof window === "undefined" || !rootId) return null;
  try {
    const nid = String(nodeId || "").trim();
    // 先读带节点的键；没有就回退历史裸键（升级前批准的记录，零迁移）
    let raw = window.localStorage.getItem(pluginTrustStorageKey(rootId, nid || undefined));
    if (!raw && nid) raw = window.localStorage.getItem(pluginTrustStorageKey(rootId));
    if (!raw) return null;
    const parsed = JSON.parse(raw) as TrustedPluginSet | null;
    if (!parsed || typeof parsed !== "object" || typeof parsed.rootPath !== "string" || !Array.isArray(parsed.plugins)) {
      return null;
    }
    return parsed;
  } catch {
    return null;
  }
}

export function saveTrustedPluginSet(rootId: string, snapshot: PluginSourceSnapshot, nodeId?: string): void {
  if (typeof window === "undefined" || !rootId) return;
  try {
    window.localStorage.setItem(pluginTrustStorageKey(rootId, nodeId), JSON.stringify(buildPluginTrustRecord(snapshot)));
  } catch {
  }
}
