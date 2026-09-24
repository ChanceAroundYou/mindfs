import { useEffect, useRef, useState } from "react";
import { type WSStatus } from "./types";

function wsStatusDisplayDelay(status: WSStatus): number {
  return status === "reconnecting" ? 800 : 0;
}

/** WS 状态展示：reconnecting 先按 connecting 显示 800ms，避免闪烁。 */
export function useDisplayStatus(status: WSStatus): WSStatus {
  const [displayStatus, setDisplayStatus] = useState<WSStatus>(status);
  const reconnectDisplayTimerRef = useRef<number | null>(null);

  useEffect(() => {
    if (reconnectDisplayTimerRef.current) {
      window.clearTimeout(reconnectDisplayTimerRef.current);
      reconnectDisplayTimerRef.current = null;
    }
    const delay = wsStatusDisplayDelay(status);
    if (delay === 0) {
      setDisplayStatus(status);
      return;
    }
    setDisplayStatus("connecting");
    reconnectDisplayTimerRef.current = window.setTimeout(() => {
      reconnectDisplayTimerRef.current = null;
      setDisplayStatus(status);
    }, delay);
    return () => {
      if (reconnectDisplayTimerRef.current) {
        window.clearTimeout(reconnectDisplayTimerRef.current);
        reconnectDisplayTimerRef.current = null;
      }
    };
  }, [status]);

  return displayStatus;
}
