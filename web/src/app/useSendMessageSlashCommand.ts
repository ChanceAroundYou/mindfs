import { useCallback } from "react";
import type { Session } from "../services/session";
import { useI18n } from "../i18n";
import { sessionService } from "../services/session";
import { reportError } from "../services/net/error";
import type { SendMessageParams } from "./useSendMessageTypes";

type Params = Pick<SendMessageParams,
  | "slashCommandResults" | "clearLocalPendingForSession"
  | "setSlashCommandResults" | "rootSessionKey"
>;
type Input = {
  activeRoot: string;
  sendSessionKey: string | null | undefined;
  session: Session | null;
  effectiveAgent: string;
  effectiveModel: string;
  effectiveAgentMode: string;
  effectiveEffort: string;
  effectiveFastService: "" | "on" | "off";
  transientSlashCommand: string;
};

export function useSendMessageSlashCommand(params: Params) {
  const { t } = useI18n();
  const { slashCommandResults, clearLocalPendingForSession, setSlashCommandResults, rootSessionKey } = params;
  return useCallback(async (input: Input) => {
    const { activeRoot, sendSessionKey, session, effectiveAgent, effectiveModel, effectiveAgentMode, effectiveEffort, effectiveFastService, transientSlashCommand } = input;
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
  }, [clearLocalPendingForSession, rootSessionKey, setSlashCommandResults, slashCommandResults, t]);
}
