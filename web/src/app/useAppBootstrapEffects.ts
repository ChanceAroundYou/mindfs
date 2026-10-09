import { useEffect } from "react";
import { sessionService } from "../services/session";
import { bootstrapService } from "../services/net/bootstrap";
import { e2eeService } from "../services/net/e2ee";
import { syncNativeReplyPollerE2EE } from "../services/net/replyPoller";
import { APP_DOCUMENT_TITLE } from "./appMisc";
import type { SessionItem } from "./appSession";
import type { BootstrapState } from "../services/net/bootstrap";
import type { E2EEState } from "../services/net/e2ee";
import type { GitDiffPayload, GitHistoryPayload, GitStatusPayload } from "../services/git";

/**
 * 启动期与 E2EE 相关的 8 条 effect：切 root 后的 git/插件加载、bootstrap 订阅、
 * 文档标题、原生回复轮询、E2EE 订阅与提示复位。
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
  setE2eeState: React.Dispatch<React.SetStateAction<E2EEState>>;
  setE2eePromptError: React.Dispatch<React.SetStateAction<string>>;
  setE2eeSecretInput: React.Dispatch<React.SetStateAction<string>>;
  e2eeState: E2EEState;
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
 *
 * E2EE 那两条的依赖数组各自不同（一条看 nodeId/required/unlocked，
 * 一条只看 required/secretPresent）—— 合并成一条会让提示在密钥就绪前就弹出。
 */
export function useAppBootstrapEffects(params: UseAppBootstrapEffectsParams) {
  const { currentRootId, currentRootNodeId, refreshGitStatus, refreshGitHistory, setGitStatus, setGitHistory, setGitDiff, scopedRootKey, ensurePluginsLoaded, pluginsLoadedByRootRef, setBootstrapState, setSessionListMode, setExternalSessions, setExternalSelectedKey, setE2eeState, setE2eePromptError, setE2eeSecretInput, e2eeState, getNodeIdForRoot } = params;

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
      setE2eeState(state.e2ee);
    });
  }, []);

  useEffect(() => {
    document.title = APP_DOCUMENT_TITLE;
  }, []);

  useEffect(() => {
    return e2eeService.subscribe((state) => {
      setE2eeState(state);
    });
  }, []);

  useEffect(() => {
    void syncNativeReplyPollerE2EE().catch((err) => {
      console.warn("[ReplyPoller] Failed to sync E2EE state:", err);
    });
  }, [e2eeState.nodeId, e2eeState.required, e2eeState.unlocked]);

  useEffect(() => {
    if (!e2eeState.required) {
      setE2eeSecretInput("");
      setE2eePromptError("");
    }
  }, [e2eeState.required, e2eeState.secretPresent]);

}
