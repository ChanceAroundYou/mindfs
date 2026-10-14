import { useCallback } from "react";
import { isPersistedSeq, mergeSessionExchanges } from "../../services/session";
import type { Session } from "../../shared/sessionTypes";

export function useSessionViewerExchangeProjection() {
  const mergeVisibleExchanges = useCallback((previous: unknown[], incoming: unknown[]) => (
    mergeSessionExchanges(previous as Session["exchanges"], incoming as Session["exchanges"])
  ), []);
  const keepPersistedExchanges = useCallback((exchanges: unknown[]) => (
    exchanges.filter((exchange) => isPersistedSeq((exchange as { seq?: unknown })?.seq))
  ), []);
  return { mergeVisibleExchanges, keepPersistedExchanges };
}
