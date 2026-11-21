import { protectedJSON } from "../net/api";
import { appURL } from "../net/base";

/** One folded request, as recorded by the loopback proxy. Sizes only. */
export type ContextSavingEntry = {
  ts: number;
  mode: string;
  req_bytes: number;
  folded_bytes: number;
  saved_bytes: number;
  /**
   * The same two bodies in estimated tokens (the kernel's estimator, same one
   * behind tokens_est). 0 on rows written before 2026-11-21 — fall back to the
   * byte fields there instead of dividing bytes by four.
   */
  req_tokens_est: number;
  forward_tokens_est: number;
  /** Total folded blocks in this request, sticky ones included. */
  blocks: number;
  /**
   * Blocks this request committed. 0 on rows written before 2026-11-21 —
   * fall back to `blocks` there rather than claiming the request did nothing.
   */
  new_blocks: number;
  tokens_est: number;
  skipped: number;
  archive_errors: number;
  fold_ms: number;
  /**
   * What the fold request paid at full price instead of the 0.1x cache read:
   * input_tokens plus cache_creation_input_tokens. Rows written before
   * 2026-11-20 counted only input_tokens, so their numbers are smaller than the
   * same event would report today. -1 means the turn ended without a usage
   * sample (see usage_source).
   */
  rewrite_tokens: number;
  usage_input: number;
  usage_cache_creation: number;
  usage_cache_read: number;
  /** "response" when the counters came from the upstream usage block. */
  usage_source: "response" | "missing" | "";
};

/**
 * The saving reported for one conversation. `latest` describes the newest
 * folded request (a snapshot, not a running total); `requests` counts folded
 * requests in the retained history and `max_saved_bytes` is the largest single
 * request saving seen. The retained rows themselves are not shipped: the chip
 * only renders this snapshot plus the counts.
 */
export type SessionContextSaving = {
  latest: ContextSavingEntry;
  requests: number;
  max_saved_bytes: number;
  /** Sum of the known rewrite samples; unknown ones are excluded. */
  total_rewrite_tokens: number;
  /** Number of known samples the total is made of. */
  rewrite_samples: number;
  /** Folded turns whose response carried no usage block. */
  usage_missing: number;
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
