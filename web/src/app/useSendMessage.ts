/**
 * 发送消息主流程 —— App.tsx 里最大的 handler（~570 行）。
 *
 * 为什么单独抽：它闭包了 35 个 ref/setter/局部函数/状态，是 App.tsx 状态图里
 * 最密的一团。留在 App.tsx 里，任何一处改动都要在 1 万行里翻找这 35 个名字；
 * 抽出来后，参数面（SendMessageParams）就是它的完整契约，一眼看得完。
 *
 * 做法：hook 而非模块函数 —— 函数体大量使用 `t()`（来自 useI18n()），
 * 模块函数拿不到 hook。所以做成 useSendMessage()，内部取 t，再把函数体
 * 逐字搬进来（函数体不被任何源码正则断言钉住，但保持逐字以最小化风险）。
 */

import { useCallback } from "react";
import type { Session } from "../services/session";
import type {
  AttachedFileContext,
  Exchange,
  MultiProjectSessionGroup,
  PendingSend,
  SessionItem,
  SessionMode,
  SlashCommandResult,
} from "../app/appSession";
import type { ManagedRootPayload } from "../app/appMisc";
import type { MainViewMode } from "../app/appPath";
import type { FilePayload } from "../services/file";
import type { PluginManager } from "../plugins/manager";
import { useI18n } from "../i18n";
import { buildClientContext } from "../services/prefs/context";
import { sessionService } from "../services/session";
import { reportError } from "../services/net/error";
import { isPlanCommand, withPlanPrefix } from "../components/action/inputTransforms";
import {
  buildMessageWithViewContext,
  hasExplicitFileContext,
  toPluginInput,
} from "../app/appMisc";
import {
  isSessionShownInMain,
  normalizeMode,
  shouldAutoSelectNewSession,
  toSessionItem,
} from "../app/appSession";
import { mergeSessionItems } from "../services/session/sessionListMerge";
import { resolveLockedSessionKey } from "../services/session/sessionLock";
import { getViewModeSystemPrompt } from "../renderer/viewCatalog";

/** handleSendMessage 的完整参数面：35 个闭包。 */
export type SendMessageParams = {
  boundSessionByRootRef: React.RefObject<Record<string, string | null>>;
  currentRootIdRef: React.RefObject<string | null>;
  currentSessionRef: React.RefObject<SessionItem | null>;
  drawerSessionByRootRef: React.RefObject<Record<string, SessionItem | null>>;
  fileRef: React.RefObject<FilePayload | null>;
  interactionModeRef: React.RefObject<"main" | "drawer">;
  mainViewRef: React.RefObject<MainViewMode>;
  managedRootByIdRef: React.RefObject<Record<string, ManagedRootPayload>>;
  pendingDraftRef: React.RefObject<PendingSend | null>;
  pendingRequestRef: React.RefObject<Record<string, PendingSend>>;
  pluginBypassRef: React.RefObject<boolean>;
  pluginManagerRef: React.RefObject<PluginManager>;
  pluginQueryRef: React.RefObject<Record<string, string>>;
  selectedSessionByRootRef: React.RefObject<Record<string, string | null>>;
  selectedSessionRef: React.RefObject<SessionItem | null>;
  sessionCacheRef: React.RefObject<Record<string, Session>>;
  setSessions: React.Dispatch<React.SetStateAction<SessionItem[]>>;
  setSlashCommandResults: React.Dispatch<React.SetStateAction<Record<string, SlashCommandResult>>>;
  setMultiProjectSessionGroups: React.Dispatch<React.SetStateAction<MultiProjectSessionGroup[]>>;
  setInteractionMode: React.Dispatch<React.SetStateAction<"main" | "drawer">>;
  setPendingPlanMode: React.Dispatch<React.SetStateAction<boolean>>;
  setSelectedSession: React.Dispatch<React.SetStateAction<SessionItem | null>>;
  attachedFileContext: AttachedFileContext | null;
  pendingPlanMode: boolean;
  slashCommandResults: Record<string, SlashCommandResult>;
  setBoundSessionForRoot: (rootID: string | null | undefined, key: string | null) => void;
  setDrawerSessionForRoot: (rootID: string | null | undefined, session: Session | SessionItem | null) => void;
  setMultiProjectSessionPending: (rootID: string | null | undefined, sessionKey: string | null | undefined, pending: boolean) => void;
  setDrawerOpenForRoot: (rootID: string | null | undefined, open: boolean) => void;
  bumpCacheVersion: () => void;
  clearLocalPendingForSession: (rootID: string | null | undefined, sessionKey: string | null | undefined) => void;
  resolvePendingForSession: (rootID: string | null | undefined, sessionKey: string | null | undefined, fallback?: boolean) => boolean;
  clearSlashCommandResultForSession: (rootID: string, sessionKey: string) => void;
  rootSessionKey: (rootId: string, sessionKey: string) => string;
  scopedRootKey: (rootId: string) => string;
  updateSessionAgentForKey: (
    rootID: string,
    sessionKey: string,
    agent: string,
    model?: string,
    agentMode?: string,
    effort?: string,
    fastService?: "" | "on" | "off",
    planMode?: boolean,
    shell?: string,
  ) => void;
};

export function useSendMessage(params: SendMessageParams): {
  handleSendMessage: (
    message: string,
    mode: SessionMode,
    agent: string,
    model?: string,
    agentMode?: string,
    effort?: string,
    fastService?: "" | "on" | "off",
    shell?: string,
    newSessionWorktree?: {
      create: boolean;
      branchMode: "new" | "existing";
      branch: string;
    },
  ) => Promise<void>;
} {
  const { t } = useI18n();
  const {
    boundSessionByRootRef,
    currentRootIdRef,
    currentSessionRef,
    drawerSessionByRootRef,
    fileRef,
    interactionModeRef,
    mainViewRef,
    managedRootByIdRef,
    pendingDraftRef,
    pendingRequestRef,
    pluginBypassRef,
    pluginManagerRef,
    pluginQueryRef,
    selectedSessionByRootRef,
    selectedSessionRef,
    sessionCacheRef,
    setSessions,
    setSlashCommandResults,
    setMultiProjectSessionGroups,
    setInteractionMode,
    setPendingPlanMode,
    setSelectedSession,
    attachedFileContext,
    pendingPlanMode,
    slashCommandResults,
    setBoundSessionForRoot,
    setDrawerSessionForRoot,
    setMultiProjectSessionPending,
    setDrawerOpenForRoot,
    bumpCacheVersion,
    clearLocalPendingForSession,
    resolvePendingForSession,
    clearSlashCommandResultForSession,
    rootSessionKey,
    scopedRootKey,
    updateSessionAgentForKey,
  } = params;

  const handleSendMessage = useCallback(
    async (
      message: string,
      mode: SessionMode,
      agent: string,
      model?: string,
      agentMode?: string,
      effort?: string,
      fastService?: "" | "on" | "off",
      shell?: string,
      newSessionWorktree?: {
        create: boolean;
        branchMode: "new" | "existing";
        branch: string;
      },
    ) => {
      const activeRoot = currentRootIdRef.current;
      if (!activeRoot) return;
      const selected = selectedSessionRef.current;
      const selectedKey = selected?.key || selected?.session_key;
      const selectedRoot =
        (selected?.root_id as string | undefined) || activeRoot;
      const currentBoundSessionKey =
        boundSessionByRootRef.current[scopedRootKey(activeRoot)] || null;
      const resolvedBoundKey = resolveLockedSessionKey(currentBoundSessionKey);
      let sendSessionKey: string | null | undefined = resolvedBoundKey;
      // The server must assign a real key before this draft can receive more input.
      if (sendSessionKey?.startsWith("pending-")) return;
      let session: Session | null = null;
      if (sendSessionKey) {
        session =
          sessionCacheRef.current[rootSessionKey(activeRoot, sendSessionKey)];
        if (!session) {
          const current = currentSessionRef.current;
          if (current?.key === sendSessionKey) {
            session = current as Session;
          }
        }
        if (!session && selectedKey === sendSessionKey) {
          session = { ...(selected as any), key: sendSessionKey } as Session;
        }
      }
      let effectiveMode = mode,
        effectiveAgent = agent,
        effectiveModel = model || "",
        effectiveAgentMode = agentMode || "",
        effectiveEffort = effort || "",
        effectiveFastService = (fastService || "") as "" | "on" | "off",
        effectiveShell = shell || "";
      const messageRequestsPlanMode = isPlanCommand(message);
      const normalizedMessage = message.trim().toLowerCase();
      const messageRequestsStatus = normalizedMessage === "/status";
      const messageRequestsLogin = normalizedMessage === "/login";
      const transientSlashCommand = messageRequestsStatus
        ? "status"
        : messageRequestsLogin
          ? "login"
          : "";
      const isQueueSend =
        !!sendSessionKey &&
        !!session &&
        // pending 纯派生：先看 multiProjectPendingByKey，表里没有才落回会话对象上
        // 那份（服务端列表回包带的快照）。
        resolvePendingForSession(
          ((session as any).root_id as string | undefined) || currentRootIdRef.current || "",
          sendSessionKey,
          (session as any).pending === true ||
            (currentSessionRef.current?.key === sendSessionKey &&
              currentSessionRef.current?.pending === true),
        );
      if (sendSessionKey && session) {
        const targetSessionKey = sendSessionKey;
        const previousAgent = session.agent || "";
        effectiveMode = normalizeMode(session.type as any);
        effectiveAgent =
          agent ||
          previousAgent ||
          "";
        effectiveModel =
          model ||
          (effectiveAgent === previousAgent ? session.model || "" : "");
        effectiveAgentMode =
          agentMode ||
          (effectiveAgent === previousAgent ? (session as any).mode || "" : "");
        effectiveEffort =
          effort ||
          (effectiveAgent === previousAgent ? (session as any).effort || "" : "");
        effectiveFastService =
          ((fastService || "") as "" | "on" | "off") ||
          (effectiveAgent === previousAgent
            ? (((session as any).fast_service || "") as "" | "on" | "off")
            : "");
        effectiveShell =
          effectiveMode === "command"
            ? (shell ||
                (session as any).shell ||
                "")
            : "";
        updateSessionAgentForKey(
          activeRoot,
          targetSessionKey,
          effectiveAgent,
          effectiveModel,
          effectiveAgentMode,
          effectiveEffort,
          effectiveFastService,
        );
        session = {
          ...(session as any),
          agent: effectiveAgent,
          model: effectiveModel,
          mode: effectiveAgentMode,
          effort: effectiveEffort,
          fast_service: effectiveFastService,
          shell: effectiveShell,
        } as Session;
        setBoundSessionForRoot(activeRoot, targetSessionKey);
        if (!isQueueSend) {
          // pending 不再写在会话对象上（纯派生自 multiProjectPendingByKey，见下方
          // setMultiProjectSessionPending）；这里只把抽屉指向这条会话。
          setDrawerSessionForRoot(activeRoot, session as Session);
        }
      } else {
        if (transientSlashCommand) {
          sendSessionKey = `transient-${Date.now()}`;
          session = null;
        } else {
          sendSessionKey = undefined;
          const tempKey = `pending-${Date.now()}`;
          session = {
            key: tempKey,
            type: mode,
            agent,
            model: effectiveModel,
            mode: effectiveAgentMode,
            effort: effectiveEffort,
            fast_service: effectiveFastService,
            shell: effectiveShell,
            plan_mode: pendingPlanMode || messageRequestsPlanMode,
            name: t("session.new"),
          } as any;
          setBoundSessionForRoot(activeRoot, tempKey);
        }
      }
      if (transientSlashCommand) {
        if (!sendSessionKey) return;
        if (effectiveAgent !== "codex") {
          reportError(
            "session.slash_command_failed",
            t("session.slashCommandCodexOnly", { command: transientSlashCommand }),
          );
          return;
        }
	        const requestId = sessionService.createRequestId("slash");
	        const targetSessionKey = sendSessionKey;
	        const resultKey = rootSessionKey(activeRoot, targetSessionKey);
        const runningTransientLoginKeys = Array.from(
          new Set(
            Object.values(slashCommandResults)
              .filter(
                (value) =>
                  value.rootId === activeRoot &&
                  value.command === "login" &&
                  value.status === "running",
              )
              .map((value) => value.sessionKey),
          ),
        );
        if (runningTransientLoginKeys.length > 0) {
          await Promise.all(
            runningTransientLoginKeys.map((sessionKey) =>
              sessionService.cancelMessage(activeRoot, sessionKey),
            ),
          );
        }
	        if (session) {
	          // 曾经这里只清两处（选中 + 小蓝灯），缓存与抽屉仍留着 `pending: true` ——
	          // 那是「灯不灭」/「状态不收敛」的直接来源（冲突⑤）。交给清除编排者。
	          // 现在它只改 multiProjectPendingByKey（唯一真相）+ 取消后的 pending_ack 收尾。
	          clearLocalPendingForSession(activeRoot, targetSessionKey);
	        }
	        setSlashCommandResults((prev) => {
          const next = { ...prev };
          for (const [key, value] of Object.entries(prev)) {
            if (
              value.rootId === activeRoot &&
              value.sessionKey.startsWith("transient-")
            ) {
              delete next[key];
            }
          }
          next[resultKey] = {
            rootId: activeRoot,
            sessionKey: targetSessionKey,
            requestId,
            command: transientSlashCommand,
            content: "",
            status: "running",
            createdAt: Date.now(),
          };
          return next;
        });
        const sent = await sessionService.runSlashCommand(
          activeRoot,
          targetSessionKey,
          transientSlashCommand,
          effectiveAgent,
          effectiveModel || undefined,
          effectiveAgentMode || undefined,
          effectiveEffort || undefined,
          effectiveFastService,
          requestId,
        );
        if (!sent) {
          setSlashCommandResults((prev) => ({
            ...prev,
            [resultKey]: {
              rootId: activeRoot,
              sessionKey: targetSessionKey,
              requestId,
              command: transientSlashCommand,
              content: "",
              status: "failed",
              error: t("session.connectionNotReady"),
              createdAt: Date.now(),
            },
          }));
          reportError(
            "network.disconnected",
            t("session.commandSendFailedNotReady"),
          );
        }
        return;
      }
      if (sendSessionKey) {
        clearSlashCommandResultForSession(activeRoot, sendSessionKey);
      }
      const runningTransientLoginKeys = Array.from(
        new Set(
          Object.values(slashCommandResults)
            .filter(
              (value) =>
                value.rootId === activeRoot &&
                value.command === "login" &&
                value.status === "running",
            )
            .map((value) => value.sessionKey),
        ),
      );
      if (runningTransientLoginKeys.length > 0) {
        await Promise.all(
          runningTransientLoginKeys.map((sessionKey) =>
            sessionService.cancelMessage(activeRoot, sessionKey),
          ),
        );
      }
      setSlashCommandResults((prev) => {
        let changed = false;
        const next = { ...prev };
        for (const [key, value] of Object.entries(prev)) {
          if (value.rootId === activeRoot && value.sessionKey.startsWith("transient-")) {
            delete next[key];
            changed = true;
          }
        }
        return changed ? next : prev;
      });
      const now = new Date().toISOString();
      const requestId = sessionService.createRequestId("msg");
      const tempKey = sendSessionKey ? "" : session?.key || "";
      const userEx: Exchange = {
        role: "user",
        agent: effectiveAgent,
        model: effectiveModel,
        mode: effectiveAgentMode,
        effort: effectiveEffort,
        fast_service: effectiveFastService,
        content: message,
        timestamp: now,
        pending_ack: true,
      };
      if (sendSessionKey) {
        const targetSessionKey = sendSessionKey;
        setMultiProjectSessionPending(activeRoot, targetSessionKey, true);
        setSessions((prev) =>
          prev.map((item) => {
            const itemKey = item.key || item.session_key;
            if (itemKey !== targetSessionKey) {
              return item;
            }
            return {
              ...(item as any),
              updated_at: now,
              agent: effectiveAgent,
              model: effectiveModel,
              mode: effectiveAgentMode,
              effort: effectiveEffort,
              fast_service: effectiveFastService,
              shell: effectiveShell,
            } as SessionItem;
          }),
        );
      }
      pendingRequestRef.current[requestId] = {
        rootId: activeRoot,
        mode: effectiveMode,
        agent: effectiveAgent,
        model: effectiveModel,
        agentMode: effectiveAgentMode,
        effort: effectiveEffort,
        fastService: effectiveFastService,
        shell: effectiveShell,
        message,
        timestamp: now,
        requestId,
        sessionKey: sendSessionKey || undefined,
        tempKey,
      };
      if (sendSessionKey && !isQueueSend) {
        const ck = rootSessionKey(activeRoot, sendSessionKey);
        const cached =
          sessionCacheRef.current[ck] || ({ ...(session as any) } as Session);
        const prevExchanges = Array.isArray((cached as any).exchanges)
          ? ((cached as any).exchanges as Exchange[])
          : [];
        sessionCacheRef.current[ck] = {
          ...(cached as any),
          exchanges: [...prevExchanges, userEx],
          mode: effectiveAgentMode,
          effort: effectiveEffort,
          fast_service: effectiveFastService,
          shell: effectiveShell,
          updated_at: now,
        } as Session;
        session = sessionCacheRef.current[ck];
        bumpCacheVersion();
      } else if (!sendSessionKey) {
        const tempSessionKey = session?.key || "";
        pendingDraftRef.current = {
          rootId: activeRoot,
          mode: effectiveMode,
          agent: effectiveAgent,
          model: effectiveModel,
          agentMode: effectiveAgentMode,
          effort: effectiveEffort,
          fastService: effectiveFastService,
          shell: effectiveShell,
          message,
          timestamp: now,
          requestId,
          tempKey,
        };
        const draftSession = {
          ...(session as any),
          exchanges: [userEx],
          updated_at: now,
        } as Session;
        if (tempSessionKey) {
          setMultiProjectSessionPending(activeRoot, tempSessionKey, true);
          sessionCacheRef.current[rootSessionKey(activeRoot, tempSessionKey)] =
            draftSession;
          const draftItem = toSessionItem(activeRoot, {
            ...(draftSession as any),
            key: tempSessionKey,
            session_key: tempSessionKey,
            root_id: activeRoot,
            created_at: now,
            updated_at: now,
          });
          if (draftItem) {
            setSessions((prev) => mergeSessionItems(prev, [draftItem]));
            // 新建会话立即同步进右侧多项目列表，避免依赖 WS 重拉（断连/竞态时永不出现）
            setMultiProjectSessionGroups((prev) => {
              const existing = prev.find((g) => g.rootId === activeRoot);
              if (existing) {
                return prev.map((g) =>
                  g.rootId === activeRoot
                    ? { ...g, sessions: mergeSessionItems(g.sessions, [draftItem]) }
                    : g,
                );
              }
              const rootName =
                (managedRootByIdRef.current as Record<string, any>)[activeRoot]?.display_name ||
                activeRoot;
              return [
                {
                  rootId: activeRoot,
                  rootName,
                  latestSessionTime: draftItem.updated_at || now,
                  sessions: [draftItem],
                  totalCount: 1,
                },
                ...prev,
              ];
            });
          }
          bumpCacheVersion();
          // 判据见 appSession.shouldAutoSelectNewSession。
          if (
            shouldAutoSelectNewSession({
              selectedSession: selectedSessionRef.current,
              mainView: mainViewRef.current,
              interactionMode: interactionModeRef.current,
              draftItem,
            })
          ) {
            selectedSessionByRootRef.current[scopedRootKey(activeRoot)] =
              tempSessionKey;
            selectedSessionRef.current = draftItem;
            setSelectedSession(draftItem);
            interactionModeRef.current = "main";
            setInteractionMode("main");
            setDrawerOpenForRoot(activeRoot, false);
          }
        }
        session = draftSession;
      }
      const isBoundInMain = isSessionShownInMain({
        selectedKey: selectedSessionRef.current?.key || "",
        sendSessionKey,
        tempKey: tempKey || "",
        interactionMode: interactionModeRef.current,
      });
      if (!isBoundInMain) {
        setInteractionMode("drawer");
        setDrawerOpenForRoot(activeRoot, true);
      }
      if (!isQueueSend) {
        // pending 不再写在会话对象上（唯一真相是 multiProjectPendingByKey）。
        setDrawerSessionForRoot(activeRoot, session as Session);
      }
      const explicitFileContext = hasExplicitFileContext(message);
      const selection =
        explicitFileContext || !attachedFileContext?.filePath
          ? undefined
          : {
              filePath: attachedFileContext.filePath,
              startLine: attachedFileContext.startLine,
              endLine: attachedFileContext.endLine,
              text: attachedFileContext.text,
            };
      const context = buildClientContext({
        currentRoot: activeRoot,
        selection,
        pluginCatalog:
          effectiveMode === "plugin" ? await getViewModeSystemPrompt() : undefined,
      });
      let outgoingMessage = message;
      const applyPendingPlanPrefix = pendingPlanMode && !sendSessionKey;
      const currentFile = fileRef.current;
      if (currentFile && !pluginBypassRef.current) {
        try {
          const pluginInput = toPluginInput(currentFile, pluginQueryRef.current);
          const plugin = pluginManagerRef.current.match(activeRoot, pluginInput);
          if (plugin?.viewContext) {
            outgoingMessage = buildMessageWithViewContext(
              message,
              pluginManagerRef.current.viewContext(plugin, pluginInput),
            );
          }
        } catch (err) {
          console.warn("[plugin/view-context] failed", err);
        }
      }
      if (applyPendingPlanPrefix) {
        outgoingMessage = withPlanPrefix(outgoingMessage);
      }
      const sent = await sessionService.sendMessage(
        activeRoot,
        sendSessionKey || undefined,
        outgoingMessage,
        effectiveMode,
        effectiveAgent,
        effectiveModel || undefined,
        effectiveAgentMode || undefined,
        effectiveEffort || undefined,
        effectiveFastService,
        context,
        effectiveShell || undefined,
        requestId,
        newSessionWorktree,
      );
      if (sent && applyPendingPlanPrefix) {
        setPendingPlanMode(false);
      }
      console.info("[session/send] dispatched", { requestId, rootId: activeRoot, sessionKey: sendSessionKey || null, tempKey: tempKey || null, sent });
      if (!sent) {
        console.warn("[session/send] dispatch_failed", { requestId, rootId: activeRoot, sessionKey: sendSessionKey || null });
        reportError("network.disconnected", t("session.messageSendFailedNotReady"), {
          details: {
            requestId,
            rootId: activeRoot,
            sessionKey: sendSessionKey || null,
          },
        });
        delete pendingRequestRef.current[requestId];
      }
      if (!sent && sendSessionKey) {
        const failedSessionKey = sendSessionKey;
        // 发送失败必须把 pending 收敛掉，否则那条会话永远显示「在回复」。
        // 现在收敛只有一处：clearLocalPendingForSession 清 multiProjectPendingByKey。
        clearLocalPendingForSession(activeRoot, failedSessionKey);
      }
      if (!sent && !sendSessionKey && tempKey) {
        setMultiProjectSessionPending(activeRoot, tempKey, false);
        setSessions((prev) =>
          prev.filter((item) => (item.key || item.session_key) !== tempKey),
        );
        delete sessionCacheRef.current[rootSessionKey(activeRoot, tempKey)];
        if (boundSessionByRootRef.current[scopedRootKey(activeRoot)] === tempKey) {
          setBoundSessionForRoot(activeRoot, null);
        }
      }
    },
    [
      attachedFileContext,
      rootSessionKey,
      mergeSessionItems,
      bumpCacheVersion,
      clearSlashCommandResultForSession,
      slashCommandResults,
      setBoundSessionForRoot,
      setDrawerOpenForRoot,
      setDrawerSessionForRoot,
      setMultiProjectSessionPending,
      updateSessionAgentForKey,
      pendingPlanMode,
    ],
  );

  return { handleSendMessage };
}
