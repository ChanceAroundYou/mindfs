import { useCallback } from "react";

import { normalizeUpdateState } from "./appMisc";
import { bootstrapService } from "../services/net/bootstrap";
import { confirmDialog } from "../services/dialog";
import {
  cancelScheduledWebViewCacheClear,
  scheduleWebViewCacheClearOnNextLaunch,
} from "../services/platform/nativeCacheControl";
import { triggerUpdate, type UpdateState } from "../services/update";
import type { FilePayload } from "../services/file/types";
import type { GitDiffPayload } from "../services/git/types";
import type { SessionItem } from "./appSession";
import type { MessageKey, MessageParams } from "../i18n";
import type { URLState } from "./appPath";

export type UseAppBootstrapActionsParams = {
  currentRootId: string | null;
  didInitRef: React.RefObject<boolean>;
  e2eeSecretInput: string;
  isMobile: boolean;
  replaceURLState: (next: URLState) => void;
  selectedSessionRef: React.RefObject<SessionItem | null>;
  setE2eePromptBusy: React.Dispatch<React.SetStateAction<boolean>>;
  setE2eePromptError: React.Dispatch<React.SetStateAction<string>>;
  setE2eeSecretInput: React.Dispatch<React.SetStateAction<string>>;
  setFile: React.Dispatch<React.SetStateAction<FilePayload | null>>;
  setGitDiff: React.Dispatch<React.SetStateAction<GitDiffPayload | null>>;
  setIsLeftOpen: React.Dispatch<React.SetStateAction<boolean>>;
  setIsRightOpen: React.Dispatch<React.SetStateAction<boolean>>;
  setOnboardingMainContentViewRoot: React.Dispatch<React.SetStateAction<string | null>>;
  setSelectedDir: React.Dispatch<React.SetStateAction<string | null>>;
  setSelectedSession: React.Dispatch<React.SetStateAction<SessionItem | null>>;
  setSelectedSessionLoading: React.Dispatch<React.SetStateAction<boolean>>;
  setUpdateState: React.Dispatch<React.SetStateAction<UpdateState>>;
  setUpdateSubmitting: React.Dispatch<React.SetStateAction<boolean>>;
  t: (key: MessageKey, params?: MessageParams) => string;
  updateState: UpdateState;
};

/**
 * 启动期才碰得到的四件事：应用自更新、新手引导步进、E2EE 配对口令。
 *
 * 它们和会话/项目的主流程没有共享状态，唯一的共同点是「都在 bootstrap 阶段被触发」，
 * 所以合成一个 hook 而不是四个 —— 四个 hook 的调用点开销比它们省下的行数还多。
 *
 * 注意 `handleOnboardingStepChange` 需要 `replaceURLState`，而后者由
 * `useAppReplaceURLState` 提供，所以本 hook 的调用点必须排在那之后。
 */
export function useAppBootstrapActions(params: UseAppBootstrapActionsParams) {
  const {
    currentRootId,
    didInitRef,
    e2eeSecretInput,
    isMobile,
    replaceURLState,
    selectedSessionRef,
    setE2eePromptBusy,
    setE2eePromptError,
    setE2eeSecretInput,
    setFile,
    setGitDiff,
    setIsLeftOpen,
    setIsRightOpen,
    setOnboardingMainContentViewRoot,
    setSelectedDir,
    setSelectedSession,
    setSelectedSessionLoading,
    setUpdateState,
    setUpdateSubmitting,
    t,
    updateState,
  } = params;

  const handleStartUpdate = useCallback(async () => {
    const next = normalizeUpdateState(updateState);
    const status = (next.status || "idle").toLowerCase();
    if (
      status === "downloading" ||
      status === "installing" ||
      status === "restarting"
    ) {
      return;
    }
    if (next.has_update && status === "available") {
      const target = next.latest_version
        ? `v${next.latest_version}`
        : "the latest version";
      const confirmed = await confirmDialog({
        message: t("update.confirmInstall", { target }),
        confirmLabel: t("update.install"),
      });
      if (!confirmed) {
        return;
      }
    }
    setUpdateSubmitting(true);
    try {
      await scheduleWebViewCacheClearOnNextLaunch();
      setUpdateState(normalizeUpdateState(await triggerUpdate()));
    } catch (error) {
      await cancelScheduledWebViewCacheClear();
      const message =
        error instanceof Error ? error.message : "Failed to start update";
      setUpdateState((prev) =>
        normalizeUpdateState({
          ...prev,
          status: "failed",
          message,
        }),
      );
    } finally {
      setUpdateSubmitting(false);
    }
  }, [updateState]);

  const handleOnboardingStepChange = useCallback((stepId: string) => {
    const showingSidebar = stepId === "sidebar-menu" || stepId === "project-tabs";
    const showingSessions = stepId === "session-actions";
    const showingTasks = stepId === "project-home" || stepId === "main-menu" || stepId === "task-templates" || stepId === "task-template-menu" || stepId === "tasks";

    if (showingTasks && currentRootId) {
      selectedSessionRef.current = null;
      setSelectedSession(null);
      setSelectedSessionLoading(false);
      setFile(null);
      setGitDiff(null);
      setSelectedDir(currentRootId);
      setOnboardingMainContentViewRoot(currentRootId);
      replaceURLState({ root: currentRootId, file: "", session: "", cursor: 0, pluginQuery: {} });
    }

    if (!isMobile) {
      setIsLeftOpen(true);
      setIsRightOpen(true);
      return;
    }
    setIsLeftOpen(showingSidebar);
    setIsRightOpen(showingSessions);
  }, [currentRootId, isMobile, replaceURLState]);

  const describeE2EEPromptError = useCallback((err: unknown) => {
    const code = err instanceof Error ? String(err.message || "").trim() : "";
    switch (code) {
      case "e2ee_proof_invalid":
        return t("e2ee.invalidProof");
      case "e2ee_secure_context_required":
      case "e2ee_webcrypto_unavailable":
        return t("e2ee.secureContextRequired");
      case "e2ee_secret_missing":
        return t("e2ee.secretMissing");
      case "e2ee_open_invalid_response":
        return t("e2ee.invalidResponse");
      default:
        if (code.startsWith("e2ee_open_failed_")) {
          return t("e2ee.openFailed");
        }
        return t("e2ee.failed");
    }
  }, [t]);

  const submitE2EESecret = useCallback(async () => {
    const trimmed = e2eeSecretInput.trim();
    if (!trimmed) {
      setE2eePromptError(t("e2ee.codeRequired"));
      return;
    }
    setE2eePromptBusy(true);
    setE2eePromptError("");
    try {
      await bootstrapService.submitPairingSecret(trimmed);
      didInitRef.current = false;
      setE2eeSecretInput("");
    } catch (err) {
      setE2eePromptError(describeE2EEPromptError(err));
    } finally {
      setE2eePromptBusy(false);
    }
  }, [describeE2EEPromptError, e2eeSecretInput, t]); // ponytail: e2ee removed, pairing dead code kept for type compat

  return {
    handleStartUpdate,
    handleOnboardingStepChange,
    describeE2EEPromptError,
    submitE2EESecret,
  };
}