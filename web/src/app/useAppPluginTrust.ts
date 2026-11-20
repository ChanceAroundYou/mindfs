import { useCallback } from "react";

import {
  loadPluginsFromSources,
  snapshotFromPluginSources,
  type PluginManager,
  type PluginSourceBundle,
} from "../plugins/manager";
import { saveTrustedPluginSet } from "../plugins/trust";
import { scopeKey } from "../shared/scope";

export type UseAppPluginTrustParams = {
  pendingPluginTrust: { rootId: string; nodeId?: string; bundle: PluginSourceBundle } | null;
  setPluginLoading: React.Dispatch<React.SetStateAction<boolean>>;
  pluginManagerRef: React.RefObject<PluginManager>;
  pluginsLoadedByRootRef: React.RefObject<Record<string, boolean>>;
  scopedRootKey: (rootId: string) => string;
  pluginsTrustPendingByRootRef: React.RefObject<Record<string, boolean>>;
  setPendingPluginTrust: React.Dispatch<
    React.SetStateAction<{ rootId: string; nodeId?: string; bundle: PluginSourceBundle } | null>
  >;
  setPluginVersion: React.Dispatch<React.SetStateAction<number>>;
  pluginsLoadingByRootRef: React.RefObject<Record<string, Promise<void>>>;
  currentRootIdRef: React.RefObject<string | null>;
  ensurePluginsLoaded: (rootId: string) => Promise<void>;
};

/**
 * 插件信任状态机：待信任 → 信任/拒绝 → 作废重载。
 *
 * 三个函数共享同一套「按 scopedRootKey 分桶的四张 ref 表」：manager、loaded、
 * loading、trustPending。这四张表必须同步增删 —— 只清其中一张就会留下
 * 「以为没加载其实加载过」或「一直显示待信任」的僵尸态，所以放在一起。
 *
 * invalidatePluginsForRoot 只在作废的是**当前项目**时才立刻重载，
 * 否则等切过去时再加载。
 */
export function useAppPluginTrust(params: UseAppPluginTrustParams) {
  const {
    pendingPluginTrust,
    setPluginLoading,
    pluginManagerRef,
    pluginsLoadedByRootRef,
    scopedRootKey,
    pluginsTrustPendingByRootRef,
    setPendingPluginTrust,
    setPluginVersion,
    pluginsLoadingByRootRef,
    currentRootIdRef,
    ensurePluginsLoaded,
  } = params;

  /**
   * 四张 ref 表的桶键。
   *
   * 带显式节点时按项目身份 `(nodeId, rootId)` 分桶；否则沿用 `scopedRootKey`
   * 的既有解析（当前选中项目 → 其选中节点 → 唯一归属）。fn 与 pc 各有一个
   * `docs` 时，两条链的加载态/待信任态不能互相顶替。
   */
  const bucketKey = useCallback(
    (rootId: string, nodeId?: string): string => {
      const nid = String(nodeId || "").trim();
      return nid ? scopeKey(nid, rootId) : scopedRootKey(rootId);
    },
    [scopedRootKey],
  );

  const handleTrustPendingPlugins = useCallback(async () => {
    const pending = pendingPluginTrust;
    if (!pending) return;
    setPluginLoading(true);
    try {
      const snapshot = snapshotFromPluginSources(pending.bundle);
      const key = bucketKey(pending.rootId, pending.nodeId);
      saveTrustedPluginSet(pending.rootId, snapshot, pending.nodeId);
      const plugins = await loadPluginsFromSources(pending.bundle.plugins);
      pluginManagerRef.current.set(pending.rootId, plugins, pending.nodeId);
      pluginsLoadedByRootRef.current[key] = true;
      delete pluginsTrustPendingByRootRef.current[key];
      setPendingPluginTrust((current) => current?.rootId === pending.rootId ? null : current);
      setPluginVersion((v) => v + 1);
    } finally {
      setPluginLoading(false);
    }
  }, [pendingPluginTrust, bucketKey]);

  const handleDisablePendingPlugins = useCallback(() => {
    const pending = pendingPluginTrust;
    if (!pending) return;
    const key = bucketKey(pending.rootId, pending.nodeId);
    pluginManagerRef.current.clear(pending.rootId, pending.nodeId);
    pluginsLoadedByRootRef.current[key] = true;
    delete pluginsTrustPendingByRootRef.current[key];
    setPendingPluginTrust((current) => current?.rootId === pending.rootId ? null : current);
    setPluginVersion((v) => v + 1);
  }, [pendingPluginTrust, bucketKey]);

  const invalidatePluginsForRoot = useCallback((rootId: string, nodeId?: string) => {
    if (!rootId) return;
    const key = bucketKey(rootId, nodeId);
    pluginManagerRef.current.clear(rootId, nodeId);
    delete pluginsLoadedByRootRef.current[key];
    delete pluginsLoadingByRootRef.current[key];
    delete pluginsTrustPendingByRootRef.current[key];
    setPendingPluginTrust((current) => current?.rootId === rootId ? null : current);
    setPluginVersion((v) => v + 1);
    if (rootId === currentRootIdRef.current) {
      void ensurePluginsLoaded(rootId).catch(() => {});
    }
  }, [ensurePluginsLoaded, bucketKey]);

  return {
    handleTrustPendingPlugins,
    handleDisablePendingPlugins,
    invalidatePluginsForRoot,
  };
}