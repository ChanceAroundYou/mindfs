/**
 * 插件加载与处理 —— A6a 簇（2 个单元，~150 行）。
 *
 * 覆盖：插件懒加载与插件事件处理入口。
 *
 * 做法：hook，参数面 UsePluginHandlersParams 即完整契约；函数体逐字搬入
 * （保持源码正则断言可命中，配合 source-map 注册）。
 */
import { useCallback, useMemo, type MutableRefObject, type Dispatch, type SetStateAction } from "react";
import { ManagedRootPayload, useResponsive } from "./appMisc";
import { URLState, normalizeCursor, readURLState } from "./appPath";
import { SessionItem } from "./appSession";
import { persistPluginQuery } from "./appStorage";
import { useActionHandlers } from "./useActionHandlers";
import { useNotificationSound } from "./useNotificationSound";
import { useExternalSessionImport } from "./useExternalSessionImport";
import { useGitActions } from "./useGitActions";
import { useGitData } from "./useGitData";
import { useManagedRoots } from "./useManagedRoots";
import { useMultiProjectSessions } from "./useMultiProjectSessions";
import { useProjectLifecycle } from "./useProjectLifecycle";
import { useProjectTreeWorktrees } from "./useProjectTreeWorktrees";
import { useSendMessage } from "./useSendMessage";
import { useSessionArchive } from "./useSessionArchive";
import { useSessionCache } from "./useSessionCache";
import { useSessionLifecycle } from "./useSessionLifecycle";
import { useSessionSearch } from "./useSessionSearch";
import { useSessionSidebarView } from "./useSessionSidebarView";
import { useSessionStreamCache } from "./useSessionStreamCache";
import { useI18n } from "../i18n";
import { type PluginSourceBundle, PluginManager, loadPluginsFromSources, scanPluginSources, snapshotFromPluginSources } from "../plugins/manager";
import { isPluginSnapshotTrusted, readTrustedPluginSet } from "../plugins/trust";
import { scopeKey } from "../shared/scope";
import { type FilePayload } from "../services/file";

export type UsePluginHandlersParams = {
  actionHandlers: ReturnType<typeof useActionHandlers>["handlers"];
  currentRootIdRef: MutableRefObject<string | null>;
  file: FilePayload | null;
  fileCursorRef: MutableRefObject<number>;
  fileRef: MutableRefObject<FilePayload | null>;
  getNodeIdForRoot: (rootId: string) => string | undefined;
  handleSelectSession: ReturnType<typeof useSessionLifecycle>["handleSelectSession"];
  managedRootByIdRef: MutableRefObject<Record<string, ManagedRootPayload>>;
  pluginManagerRef: MutableRefObject<PluginManager>;
  pluginQuery: Record<string, string>;
  pluginsLoadedByRootRef: MutableRefObject<Record<string, boolean>>;
  pluginsLoadingByRootRef: MutableRefObject<Record<string, Promise<void>>>;
  pluginsTrustPendingByRootRef: MutableRefObject<Record<string, boolean>>;
  replaceURLState: (next: URLState) => any;
  scopedRootKey: (rootId: string) => string;
  sessions: SessionItem[];
  setPendingPluginTrust: Dispatch<SetStateAction<{ rootId: string; nodeId?: string; bundle: PluginSourceBundle; } | null>>;
  setPluginLoading: Dispatch<SetStateAction<boolean>>;
  setPluginQuery: Dispatch<SetStateAction<Record<string, string>>>;
  setPluginVersion: Dispatch<SetStateAction<number>>;
};

export function usePluginHandlers(params: UsePluginHandlersParams) {
  const {
    actionHandlers,
    currentRootIdRef,
    file,
    fileCursorRef,
    fileRef,
    getNodeIdForRoot,
    handleSelectSession,
    managedRootByIdRef,
    pluginManagerRef,
    pluginQuery,
    pluginsLoadedByRootRef,
    pluginsLoadingByRootRef,
    pluginsTrustPendingByRootRef,
    replaceURLState,
    scopedRootKey,
    sessions,
    setPendingPluginTrust,
    setPluginLoading,
    setPluginQuery,
    setPluginVersion,
  } = params;

  const ensurePluginsLoaded = useCallback(async (rootId: string) => {
    // 扫描、信任记录、插件表三处都必须按**项目身份**（node + root）分桶：
    // fn 与 pc 各有一个 `docs` 时只按 rootId 会互相顶替。
    const scanNodeId = getNodeIdForRoot(rootId);
    // 四张 ref 表的桶键与 `scopedRootKey` 同源：拿得到节点就按 (node, root)，
    // 拿不到才退回旧口径（否则同名项目的加载态会串台）。
    const bucket = scanNodeId ? scopeKey(scanNodeId, rootId) : scopedRootKey(rootId);
    if (!rootId || pluginsLoadedByRootRef.current[bucket]) {
      return;
    }
    if (pluginsTrustPendingByRootRef.current[bucket]) {
      return;
    }
    const inflight = pluginsLoadingByRootRef.current[bucket];
    if (inflight) {
      await inflight;
      return;
    }
    setPluginLoading(true);
    const request = scanPluginSources(rootId, managedRootByIdRef.current[rootId]?.root_path || rootId, scanNodeId)
      .then(async (bundle) => {
        const snapshot = snapshotFromPluginSources(bundle);
        if (bundle.plugins.length > 0 && !isPluginSnapshotTrusted(snapshot, readTrustedPluginSet(rootId, scanNodeId))) {
          pluginManagerRef.current.clear(rootId, scanNodeId);
          pluginsTrustPendingByRootRef.current[bucket] = true;
          setPendingPluginTrust({ rootId, nodeId: scanNodeId, bundle });
          setPluginVersion((v) => v + 1);
          return;
        }
        const plugins = await loadPluginsFromSources(bundle.plugins);
        pluginManagerRef.current.set(rootId, plugins, scanNodeId);
        pluginsLoadedByRootRef.current[bucket] = true;
        setPluginVersion((v) => v + 1);
      })
      .catch(() => {
        pluginManagerRef.current.clear(rootId, scanNodeId);
        pluginsLoadedByRootRef.current[bucket] = true;
        setPluginVersion((v) => v + 1);
      })
      .finally(() => {
        delete pluginsLoadingByRootRef.current[bucket];
        setPluginLoading(false);
      });
    pluginsLoadingByRootRef.current[bucket] = request;
    await request;
  }, []);
  const pluginHandlers = useMemo(
    () => ({
      open: async (params: Record<string, unknown>) => {
        await actionHandlers.open(params);
      },
      open_dir: async (params: Record<string, unknown>) => {
        await actionHandlers.open_dir(params);
      },
      select_session: async (params: Record<string, unknown>) => {
        const key = typeof params?.key === "string" ? params.key : "";
        if (!key) return;
        const root = currentRootIdRef.current;
        if (!root) return;
        const matched = sessions.find(
          (item) => (item.key || item.session_key) === key,
        );
        if (matched) {
          await handleSelectSession(matched);
          return;
        }
        await handleSelectSession({ key, session_key: key, root_id: root });
      },
      navigate: async (params: Record<string, unknown>) => {
        const current = readURLState();
        const nextRoot = current.root || currentRootIdRef.current || "";
        const nextPath =
          typeof params?.path === "string" ? params.path : current.file;
        const explicitCursor = normalizeCursor(params?.cursor);
        const rawQuery =
          params?.query &&
          typeof params.query === "object" &&
          !Array.isArray(params.query)
            ? (params.query as Record<string, unknown>)
            : null;

        const nextPluginQuery: Record<string, string> = {
          ...current.pluginQuery,
        };
        if (rawQuery) {
          Object.entries(rawQuery).forEach(([key, value]) => {
            if (!key) return;
            nextPluginQuery[key] = String(value);
          });
        }

        let nextCursor = current.cursor;
        if (explicitCursor !== null) {
          nextCursor = explicitCursor;
        } else if (
          typeof params?.path === "string" &&
          params.path !== current.file
        ) {
          nextCursor = 0;
        }
        if (!nextPath) {
          nextCursor = 0;
        }

        const nextState: URLState = {
          root: nextRoot || "",
          file: nextPath || "",
          session: "",
          cursor: nextCursor,
          pluginQuery: nextPluginQuery,
        };
        replaceURLState(nextState);
        if (nextState.file) {
          persistPluginQuery(
            nextState.root,
            nextState.file,
            nextState.pluginQuery,
            getNodeIdForRoot(nextState.root),
          );
        }

        const rootChanged =
          (nextState.root || "") !== (currentRootIdRef.current || "");
        const fileChanged =
          (nextState.file || "") !== (fileRef.current?.path || "");
        const pluginChanged =
          JSON.stringify(nextState.pluginQuery) !==
          JSON.stringify(current.pluginQuery);

        if (pluginChanged) {
          setPluginQuery(nextState.pluginQuery);
        }

        if (!nextState.file) {
          if (nextState.root) {
            await actionHandlers.open_dir({
              path: nextState.root,
              root: nextState.root,
              preservePluginQuery: true,
              isRoot: true,
            });
          }
          return;
        }

        const cursorChanged = nextState.cursor !== fileCursorRef.current;
        if (rootChanged || fileChanged || cursorChanged) {
          await actionHandlers.open({
            path: nextState.file,
            root: nextState.root,
            cursor: nextState.cursor,
            preservePluginQuery: true,
          });
        }
      },
    }),
    [actionHandlers, sessions, handleSelectSession, replaceURLState],
  );

  return {
    ensurePluginsLoaded,
    pluginHandlers,
  };
}
