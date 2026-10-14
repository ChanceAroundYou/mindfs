import { useCallback, useEffect, useState } from "react";
import { dialogService, type DialogRequest } from "../services/dialog";
import { useBackLayer } from "./useBackLayer";

export function useDialogHost() {
  const [request, setRequest] = useState<DialogRequest | null>(null);
  const [draft, setDraft] = useState("");

  useEffect(() => dialogService.subscribe((next) => {
    setRequest(next);
    setDraft(next?.kind === "prompt" ? next.defaultValue || "" : "");
  }), []);

  const dismiss = useCallback(() => dialogService.dismiss(), []);
  const resolve = useCallback((value: boolean | string | null) => dialogService.resolve(value), []);
  useBackLayer(!!request, dismiss);

  useEffect(() => {
    if (!request) return undefined;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        dismiss();
      } else if (event.key === "Enter" && request.kind === "confirm") {
        event.preventDefault();
        resolve(true);
      }
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [dismiss, request, resolve]);

  return { request, draft, setDraft, dismiss, resolve };
}
