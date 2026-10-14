import { useRef, useState } from "react";
import { protectedJSON, ProtectedAPIError } from "../services/net/api";
import { appURL } from "../services/net/base";
import { reportError } from "../services/net/error";
import { alertDialog, confirmDialog, promptDialog } from "../services/dialog";
import type { MessageKey } from "../i18n/index";

export function useFileOperationActions({
  root,
  path,
  onComplete,
  t,
}: {
  root: string;
  path: string;
  onComplete: () => void | Promise<void>;
  t: (key: MessageKey, params?: Record<string, string | number>) => string;
}) {
  const [busy, setBusy] = useState(false);
  const busyRef = useRef(false);

  const submit = async (action: "delete" | "rename") => {
    if (busyRef.current) return;
    let name: string | undefined;
    if (action === "rename") {
      const currentName = path.replace(/\\/g, "/").split("/").pop() || "";
      const input = await promptDialog({ message: t("fileOperation.namePrompt"), defaultValue: currentName });
      if (input === null) return;
      name = input.trim();
      if (!name || name === "." || name === ".." || /[/\\\x00]/.test(name)) {
        alertDialog(t("directory.invalidFileName"));
        return;
      }
      if (name === currentName) return;
    } else if (!await confirmDialog({ message: t("fileOperation.confirmDelete", { name: path }), danger: true })) {
      return;
    }
    busyRef.current = true;
    setBusy(true);
    try {
      await protectedJSON(appURL("/api/file/operation"), {
        method: "POST", headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ root, path, action, name }),
      });
      await onComplete();
    } catch (err) {
      reportError("file.write_failed", err instanceof ProtectedAPIError && err.status === 409
        ? t("directory.fileExists")
        : err instanceof Error ? err.message : t("common.actionFailed", { action: t(`fileOperation.${action}`) }));
    } finally {
      busyRef.current = false;
      setBusy(false);
    }
  };

  return { busy, submit };
}
