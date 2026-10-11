import { useEffect } from "react";
import { sessionService } from "../services/session";
import { bootstrapService } from "../services/net/bootstrap";
import { APP_DOCUMENT_TITLE } from "./appMisc";
import type { SessionItem } from "./appSession";
import type { BootstrapState } from "../services/net/bootstrap";
import type { GitDiffPayload, GitHistoryPayload, GitStatusPayload } from "../services/git";

/**
 * 启动期的 5 条 effect：切 root 后的 git/插件加载、bootstrap 订阅、文档标题。
 */
export type UseAppBootstrapEffectsParams = {
  currentRootId: string | null;
  currentRootNodeId: string | null;
  refreshGitStatus: (rootID: string) => Promise<GitStatusPayload | null>;
  refreshGitHistory: (rootID: string, options?: { force?: boolean; waitForIncremental?: boolean; }) => Promise<GitHistoryPayload | null>;
  setGitStatus: React.Dispatch<React.SetStateAction<GitStatusPayload | null>>;
  setGitHistory: React.Dispatch<React.SetStateAction<GitHistoryPayload | null>>;
  setGitDiff: React.Dispatch<React.SetStateAction<GitDiffPayload | null>>;
  scopedRootKey: (rootId: string) => string;
  ensurePluginsLoaded: (rootId: string) => Promise<void>;
  pluginsLoadedByRootRef: React.RefObject<Record<string, boolean>>;
  setBootstrapState: React.Dispatch<React.SetStateAction<BootstrapState>>;
  setSessionListMode: React.Dispatch<React.SetStateAction<"local" | "import">>;
  setExternalSessions: React.Dispatch<React.SetStateAction<SessionItem[]>>;
  setExternalSelectedKey: React.Dispatch<React.SetStateAction<string>>;
  getNodeIdForRoot: (rootId: string) => string | undefined;
};

/**
 * 同样是**一段连续的声明**，必须整段搬（React 按声明顺序跑 effect）。
 *
 * 三个 `subscribe` 形态的 effect 都返回退订函数 —— 这是它们能安全处理
 * 「订阅源变了」的唯一方式，**别把 return 省掉**，否则每次依赖变化都会叠一份订阅。
 *
 * `document.title = APP_DOCUMENT_TITLE` 看着像可以直接写在模块顶层的常量，
 * 但它放在 effect 里是有原因的：标题要跟着应用状态变，而且不能盖掉
 * 浏览器为未读会话加的前缀。改动前先看它和别处对 title 的写入。
 */
export function useAppBootstrapEffects(params: UseAppBootstrapEffectsParams) {
  const { currentRootId, currentRootNodeId, refreshGitStatus, refreshGitHistory, setGitStatus, setGitHistory, setGitDiff, scopedRootKey, ensurePluginsLoaded, pluginsLoadedByRootRef, setBootstrapState, setSessionListMode, setExternalSessions, setExternalSelectedKey, getNodeIdForRoot } = params;

  useEffect(() => {
    if (!currentRootId) return;
    sessionService.connect(currentRootId, getNodeIdForRoot(currentRootId));
  }, [currentRootId, currentRootNodeId]);

  useEffect(() => {
    if (!currentRootId) {
      setGitStatus(null);
      setGitHistory(null);
      setGitDiff(null);
      setSessionListMode("local");
      setExternalSessions([]);
      setExternalSelectedKey("");
      return;
    }
    void refreshGitStatus(currentRootId);
    void refreshGitHistory(currentRootId);
    setGitDiff(null);
  }, [currentRootId, currentRootNodeId, refreshGitHistory, refreshGitStatus]);

  useEffect(() => {
    if (!currentRootId) return;
    if (pluginsLoadedByRootRef.current[scopedRootKey(currentRootId)]) return;
    void ensurePluginsLoaded(currentRootId).catch(() => {});
  }, [currentRootId, ensurePluginsLoaded]);

  useEffect(() => {
    return bootstrapService.subscribe((state) => {
      setBootstrapState(state);
    });
  }, []);

  useEffect(() => {
    document.title = APP_DOCUMENT_TITLE;
  }, []);

}
