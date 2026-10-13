/**
 * 普通消息发送编排：准备目标交给 prepareSendMessageTarget，瞬时 slash command 交给专用 hook。
 * 本 hook 保留乐观消息、上下文/插件处理、发送与失败收敛的顺序。
 */

import { useCallback } from "react";
import type { Session } from "../services/session";
import type { SendMessageParams } from "./useSendMessageTypes";
import { prepareSendMessageTarget } from "./prepareSendMessageTarget";
import { useSendMessageSlashCommand } from "./useSendMessageSlashCommand";
import type { Exchange, SessionItem, SessionMode } from "../app/appSession";
import { useI18n } from "../i18n";
import { buildClientContext } from "../services/prefs/context";
import { sessionService } from "../services/session";
import { reportError } from "../services/net/error";
import { withPlanPrefix } from "../shared/planCommand";
import {
  buildMessageWithViewContext,
  hasExplicitFileContext,
  toPluginInput,
} from "../app/appMisc";
import {
  isSessionShownInMain,
  shouldAutoSelectNewSession,
  toSessionItem,
} from "../app/appSession";
import { mergeSessionItems } from "../services/session/sessionListMerge";
import { getViewModeSystemPrompt } from "../renderer/viewCatalog";

export type { SendMessageParams } from "./useSendMessageTypes";

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
  const runSlashCommand = useSendMessageSlashCommand(params);
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
      const target = prepareSendMessageTarget(params, {
        message, mode, agent, model, agentMode, effort, fastService, shell,
        newSessionName: t("session.new"),
      });
      if (!target) return;
      const {
        activeRoot, sendSessionKey, effectiveMode, effectiveAgent,
        effectiveModel, effectiveAgentMode, effectiveEffort, effectiveFastService,
        effectiveShell, transientSlashCommand, isQueueSend,
      } = target;
      let session = target.session;
      if (transientSlashCommand) {
        await runSlashCommand({
          activeRoot, sendSessionKey, session, effectiveAgent, effectiveModel,
          effectiveAgentMode, effectiveEffort, effectiveFastService, transientSlashCommand,
        });
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
      runSlashCommand,
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
