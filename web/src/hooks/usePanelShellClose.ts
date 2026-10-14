import { useCallback } from "react";
import { confirmDialog } from "../services/dialog";
import type { MessageKey } from "../i18n/index";

export function usePanelShellClose({
  onClose,
  hasUnsavedChanges,
  t,
}: {
  onClose?: () => void;
  hasUnsavedChanges?: () => boolean;
  t: (key: MessageKey) => string;
}) {
  return useCallback(() => {
    if (!onClose) return;
    if (!hasUnsavedChanges?.()) {
      onClose();
      return;
    }
    void confirmDialog({
      message: t("common.discardChangesConfirm"),
      confirmLabel: t("common.discardChanges"),
      cancelLabel: t("common.keepEditing"),
      danger: true,
    }).then((ok) => {
      if (ok) onClose();
    });
  }, [onClose, hasUnsavedChanges, t]);
}
