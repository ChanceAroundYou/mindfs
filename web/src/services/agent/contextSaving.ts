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
  /**
   * Estimated size of the blocks this request committed, sticky excluded.
   * 0 on rows written before 2026-11-21 — the block count is all those rows
   * know.
   */
  new_tokens_est: number;
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
  /**
   * Whose counters these are: "upstream" when the request went to the
   * provider's own API, "relay" when it went through a relay (llmux) that may
   * synthesize them, "" for rows written before the field existed or turns
   * without a sample. Only "relay" justifies the estimate note.
   */
  usage_provenance: "upstream" | "relay" | "";
};

/** 「超长会话」三档结论，由服务端的 EvaluateHygiene 纯函数给出。 */
export type SessionHygieneState = "normal" | "notice" | "hint" | "disabled";

/**
 * 会话卫生（session hygiene）：又久又大的会话提示一次，并在若干轮之后由
 * 服务端自动补一个 harness 自己的压缩命令。`disabled` = 节点关掉了这个特性。
 */
export type SessionHygiene = {
  state: SessionHygieneState;
  enabled: boolean;
  /** 距上次压缩的累计活跃时长（秒）：空闲超过 idle gap 的时间段已被丢弃。 */
  active_seconds: number;
  context_tokens: number;
  /** 判定基准：harness 上报的上下文窗口（缺失时服务端按 200k 算）。 */
  context_window: number;
  /** 「大」的阈值：窗口的百分比。 */
  context_percent: number;
  /** 阈值换算出来的 token 数 = context_window * context_percent / 100。 */
  context_limit: number;
  /** 自动压缩前需要的连续过线回合数。 */
  over_line_turns: number;
  /** 已连续过线的回合数（含刚过线的那一轮）。 */
  turns_over_line: number;
  /** < 0 表示没有倒计时（notice / normal 档）。 */
  turns_until_auto_compact: number;
  auto_compact_due: boolean;
  /** 上一次压缩没生效，已停止自动重试；只能手动压缩。 */
  auto_failed: boolean;
  /** /compact 回合已排上但还没收到 harness 的压缩事件。 */
  compact_pending: boolean;
  /** 上一次压缩收尾时记下的可读原因，成功为空。 */
  compact_error: string;
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
  /**
   * Running totals over the same retained window as `requests`: one turn's fold
   * is small, a session's is what the user is paying to keep. Bytes are exact;
   * the token total only covers `saved_token_samples` rows that carry the
   * kernel's estimate (older rows have none, and bytes/4 is not a substitute).
   */
  total_saved_bytes: number;
  total_saved_tokens: number;
  saved_token_samples: number;
  /** Sum of the known rewrite samples; unknown ones are excluded. */
  total_rewrite_tokens: number;
  /** Number of known samples the total is made of. */
  rewrite_samples: number;
  /** Folded turns whose response carried no usage block. */
  usage_missing: number;
  /**
   * 计费上下文口径的累计：上游 counters（input + cache creation + cache read）
   * 与同一批请求携带的折叠内容。两者的比例才是没有被正文里的非文本字节稀释
   * 的节省率（Codex 的 reasoning 密文会让字节口径把 39% 读成 3%）。
   * 只统计既有 usage 样本、又有 token 估算的行；`context_token_samples` 是行数。
   */
  total_context_tokens: number;
  total_folded_tokens: number;
  context_token_samples: number;
  /** 超长会话提示的状态；折叠省了多少与它无关。 */
  hygiene: SessionHygiene;
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

/**
 * 让服务端立刻补一个 harness 自己的压缩回合（胶囊上的「立即压缩」）。
 *
 * 请求只负责把回合排出去，压缩结果通过正常的会话事件流回到界面 —— 所以这里
 * 拿到 202 就够了，界面靠刷新胶囊看新状态。
 */
export async function compactSession(
  sessionKey: string,
  rootId?: string,
  nodeId?: string,
): Promise<void> {
  const params = new URLSearchParams();
  if (rootId) params.set("root", rootId);
  await protectedJSON<{ status: string }>(
    appURL(`/api/sessions/${encodeURIComponent(sessionKey)}/compact`, params, nodeId),
    { method: "POST" },
  );
}
