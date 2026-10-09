import { useCallback } from "react";

import {
  loadPluginsFromSources,
  snapshotFromPluginSources,
  type PluginManager,
  type PluginSourceBundle,
} from "../plugins/manager";
import { saveTrustedPluginSet } from "../plugins/trust";

export type UseAppPluginTrustParams = {
  pendingPluginTrust: { rootId: string; bundle: PluginSourceBundle } | null;
  setPluginLoading: React.Dispatch<React.SetStateAction<boolean>>;
  pluginManagerRef: React.RefObject<PluginManager>;
  pluginsLoadedByRootRef: React.RefObject<Record<string, boolean>>;
  scopedRootKey: (rootId: string) => string;
  pluginsTrustPendingByRootRef: React.RefObject<Record<string, boolean>>;
  setPendingPluginTrust: React.Dispatch<
    React.SetStateAction<{ rootId: string; bundle: PluginSourceBundle } | null>
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

  const handleTrustPendingPlugins = useCallback(async () => {
    const pending = pendingPluginTrust;
    if (!pending) return;
    setPluginLoading(true);
    try {
      const snapshot = snapshotFromPluginSources(pending.bundle);
      saveTrustedPluginSet(pending.rootId, snapshot);
      const plugins = await loadPluginsFromSources(pending.bundle.plugins);
      pluginManagerRef.current.set(pending.rootId, plugins);
      pluginsLoadedByRootRef.current[scopedRootKey(pending.rootId)] = true;
      delete pluginsTrustPendingByRootRef.current[scopedRootKey(pending.rootId)];
      setPendingPluginTrust((current) => current?.rootId === pending.rootId ? null : current);
      setPluginVersion((v) => v + 1);
    } finally {
      setPluginLoading(false);
    }
  }, [pendingPluginTrust]);

  const handleDisablePendingPlugins = useCallback(() => {
    const pending = pendingPluginTrust;
    if (!pending) return;
    pluginManagerRef.current.clear(pending.rootId);
    pluginsLoadedByRootRef.current[scopedRootKey(pending.rootId)] = true;
    delete pluginsTrustPendingByRootRef.current[scopedRootKey(pending.rootId)];
    setPendingPluginTrust((current) => current?.rootId === pending.rootId ? null : current);
    setPluginVersion((v) => v + 1);
  }, [pendingPluginTrust]);

  const invalidatePluginsForRoot = useCallback((rootId: string) => {
    if (!rootId) return;
    pluginManagerRef.current.clear(rootId);
    delete pluginsLoadedByRootRef.current[scopedRootKey(rootId)];
    delete pluginsLoadingByRootRef.current[scopedRootKey(rootId)];
    delete pluginsTrustPendingByRootRef.current[scopedRootKey(rootId)];
    setPendingPluginTrust((current) => current?.rootId === rootId ? null : current);
    setPluginVersion((v) => v + 1);
    if (rootId === currentRootIdRef.current) {
      void ensurePluginsLoaded(rootId).catch(() => {});
    }
  }, [ensurePluginsLoaded]);

  return {
    handleTrustPendingPlugins,
    handleDisablePendingPlugins,
    invalidatePluginsForRoot,
  };
}