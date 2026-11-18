import { protectedJSON } from "../net/api";
import { appURL } from "../net/base";

/** One folded request, as recorded by the loopback proxy. Sizes only. */
export type ContextSavingEntry = {
  ts: number;
  mode: string;
  req_bytes: number;
  folded_bytes: number;
  saved_bytes: number;
  blocks: number;
  tokens_est: number;
  skipped: number;
  archive_errors: number;
  fold_ms: number;
};

/**
 * The saving reported for one conversation. `latest` describes the most recent
 * folded request (a snapshot, not a running total); `requests` counts folded
 * requests in the retained history and `max_saved_bytes` is the largest single
 * request saving seen.
 */
export type SessionContextSaving = {
  latest: ContextSavingEntry;
  requests: number;
  max_saved_bytes: number;
  recent: ContextSavingEntry[] | null;
};

export async function fetchSessionContextSaving(
  sessionKey: string,
  rootId?: string,
  nodeId?: string,
): Promise<SessionContextSaving> {
  const params = new URLSearchParams();
  if (rootId) params.set("root", rootId);
  return protectedJSON<SessionContextSaving>(
    appURL(`/api/sessions/${encodeURIComponent(sessionKey)}/context-saving`, params, nodeId),
  );
}
