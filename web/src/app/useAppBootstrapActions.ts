import { useCallback } from "react";

import { normalizeUpdateState } from "./appMisc";
import { confirmDialog } from "../services/dialog";
import {
  cancelScheduledWebViewCacheClear,
  scheduleWebViewCacheClearOnNextLaunch,
} from "../services/platform/nativeCacheControl";
import { triggerUpdate, type UpdateState } from "../services/update";
import type { MessageKey, MessageParams } from "../i18n";

export type UseAppBootstrapActionsParams = {
  setUpdateState: React.Dispatch<React.SetStateAction<UpdateState>>;
  setUpdateSubmitting: React.Dispatch<React.SetStateAction<boolean>>;
  t: (key: MessageKey, params?: MessageParams) => string;
  updateState: UpdateState;
};

/** 启动期才碰得到的应用自更新。 */
export function useAppBootstrapActions(params: UseAppBootstrapActionsParams) {
  const { setUpdateState, setUpdateSubmitting, t, updateState } = params;

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

  return { handleStartUpdate };
}