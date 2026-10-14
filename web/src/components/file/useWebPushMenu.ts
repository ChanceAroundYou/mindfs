import { useCallback, useEffect, useState } from "react";
import {
  getWebPushStatus,
  sendWebPushTest,
  subscribeWebPush,
  unsubscribeWebPush,
  webPushReasonLabel,
  type WebPushStatus,
} from "../../services/webPush";
import { useI18n } from "../../i18n/index";

export function useWebPushMenu() {
  const { t } = useI18n();
  const [status, setStatus] = useState<WebPushStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const [expanded, setExpanded] = useState(false);

  const refresh = useCallback(async () => {
    try {
      setStatus(await getWebPushStatus());
    } catch (error) {
      setStatus(null);
      setMessage(error instanceof Error ? error.message : t("fileTree.notificationStatusFailed"));
    }
  }, [t]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const run = async (action: "subscribe" | "unsubscribe" | "test") => {
    if (busy) return;
    setBusy(true);
    setMessage("");
    try {
      if (action === "subscribe") {
        setStatus(await subscribeWebPush());
      } else if (action === "unsubscribe") {
        setStatus(await unsubscribeWebPush());
      } else {
        await sendWebPushTest();
        setMessage(t("fileTree.notificationSent"));
        await refresh();
      }
    } catch (error) {
      setMessage(error instanceof Error ? error.message : t("fileTree.notificationActionFailed"));
      await refresh();
    } finally {
      setBusy(false);
    }
  };

  return { status, busy, message, expanded, setExpanded, run, disabledReason: webPushReasonLabel(status?.reason) };
}
