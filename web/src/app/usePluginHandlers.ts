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
import { useCompletionSound } from "./useCompletionSound";
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
import { useTaskTemplates } from "./useTaskTemplates";
import { useI18n } from "../i18n";
import { type PluginSourceBundle, PluginManager, loadPluginsFromSources, scanPluginSources, snapshotFromPluginSources } from "../plugins/manager";
import { isPluginSnapshotTrusted, readTrustedPluginSet } from "../plugins/trust";
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
  setPendingPluginTrust: Dispatch<SetStateAction<{ rootId: string; bundle: PluginSourceBundle; } | null>>;
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
    if (!rootId || pluginsLoadedByRootRef.current[scopedRootKey(rootId)]) {
      return;
    }
    if (pluginsTrustPendingByRootRef.current[scopedRootKey(rootId)]) {
      return;
    }
    const inflight = pluginsLoadingByRootRef.current[scopedRootKey(rootId)];
    if (inflight) {
      await inflight;
      return;
    }
    setPluginLoading(true);
    const request = scanPluginSources(rootId, managedRootByIdRef.current[rootId]?.root_path || rootId, getNodeIdForRoot(rootId))
      .then(async (bundle) => {
        const snapshot = snapshotFromPluginSources(bundle);
        if (bundle.plugins.length > 0 && !isPluginSnapshotTrusted(snapshot, readTrustedPluginSet(rootId))) {
          pluginManagerRef.current.clear(rootId);
          pluginsTrustPendingByRootRef.current[scopedRootKey(rootId)] = true;
          setPendingPluginTrust({ rootId, bundle });
          setPluginVersion((v) => v + 1);
          return;
        }
        const plugins = await loadPluginsFromSources(bundle.plugins);
        pluginManagerRef.current.set(rootId, plugins);
        pluginsLoadedByRootRef.current[scopedRootKey(rootId)] = true;
        setPluginVersion((v) => v + 1);
      })
      .catch(() => {
        pluginManagerRef.current.clear(rootId);
        pluginsLoadedByRootRef.current[scopedRootKey(rootId)] = true;
        setPluginVersion((v) => v + 1);
      })
      .finally(() => {
        delete pluginsLoadingByRootRef.current[scopedRootKey(rootId)];
        setPluginLoading(false);
      });
    pluginsLoadingByRootRef.current[scopedRootKey(rootId)] = request;
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
