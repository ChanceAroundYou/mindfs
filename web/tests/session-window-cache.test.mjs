import assert from "node:assert/strict";
import { test } from "vitest";
import {
  appSrc,
  buildEvictFrom,
  enSrc,
  realtimeSrc,
  sessionCode,
  sessionSrc,
  viewerCode,
  viewerSrc,
  zhSrc,
} from "./session-window-sources.mjs";
// 缓存淘汰方向、实时链路契约（R4 probe / F2 重锚定 / F3 重拉 / F4 失败可见）、
// 窗口尺寸常量与身份 overlay 行为。

test("session-window-cache", async () => {
// overlay 判定由 src/shared/sessionOverlay.ts 的纯函数实现，直接 import 真实现。
const { computeTailOverlay } = await import("../src/shared/sessionOverlay.ts");

// ── 缓存淘汰只从头部，永不动尾部（用户 2026-10-05 定的规则）──────────────
// 淘汰**可以**发生（超预算），但必须满足：
//   ① 淘汰只发生在**写入**时（toPersistentSession），不在加载过程中
//   ② 淘汰方向是从**头部**（最旧）砍，尾部最新的一条必须永远活着
// 曾经的写法反了：从尾部往前扫文本、记下超预算条数，再 slice(0, len - n) ——
// 砍掉的正是最新的那几条（超预算的通常是最长的助手回答），症状是「会话越长，
// 开得越久，尾部丢得越多」。这两条断言钉住方向。
assert.match(
  sessionCode,
  /const kept = exchanges\.slice\(keptFrom\);/,
  "the write-path cache must keep a *suffix* of exchanges (evict from the head)",
);
assert.doesNotMatch(
  sessionCode,
  /slice\(-extraSliced\)|tail\.length - extraSliced/,
  "head-eviction slicing is the fix — dropping from the tail is what lost the newest messages",
);
// 扫描下界必须是**独立的** floorStart，不能复用会被改写的 keptFrom：一旦
// keptFrom=len-1，条件 `i >= keptFrom` 立刻为假、循环只跑一轮，只剩最新那一条。
assert.match(
  sessionCode,
  /const floorStart = Math\.max\(0, exchanges\.length - SESSION_CACHE_MAX_EXCHANGES\);/,
  "the scan floor must be an independent bound, not the mutated keep-cursor",
);
assert.doesNotMatch(
  sessionCode,
  /i >= keptFrom/,
  "looping against the mutated cursor silently keeps only the newest row",
);
// 被淘汰的头部行不该留下孤立 aux（重新加载时那批工具卡会凭空多出来）。
assert.match(
  sessionCode,
  /if \(keptSeqs\.has\(Number\(seq\)\)\) keptAux\[seq\] = items;/,
  "aux for evicted head rows must be dropped with them",
);

// 淘汰器**直接跑**一遍五个边界情形。这段逻辑出过错两次（一次只留最新一条、
// 一次单条超预算时整份都留不下），形状断言看不出来，所以断言行为。
//
// task-35 拆分后 `toPersistentSession` 从模块私有变成了 services/sessionCacheStore.ts
// 的具名导出（不再需要 transpile 时补 export 的探针），直接 import 真实现。
const evictProbe = await import("../src/services/sessionCacheStore.ts");
const evict = buildEvictFrom(evictProbe.toPersistentSession);
assert.equal(typeof evict, "function", "toPersistentSession must be reachable for the eviction probe");

const mk = (n, bytes) => Array.from({ length: n }, (_, i) => ({ seq: i + 1, content: "x".repeat(bytes) }));
const seqOf = (kept) => kept.map((e) => e.seq);

// ① 超文本预算：保留**尾部** ~200 条，头部被淘汰
{
  const kept = evict(mk(500, 1024));
  assert.equal(kept.length, 200, "must keep as many 1KB rows as the 200KB budget allows");
  assert.equal(seqOf(kept)[0], 301, "eviction must come off the head");
  assert.equal(seqOf(kept).at(-1), 500, "the newest row must always survive");
}
// ② 超条数预算：条数封顶，仍保尾部
{
  const kept = evict(mk(600, 10));
  assert.equal(kept.length, 500, "the row-count cap still applies");
  assert.equal(seqOf(kept)[0], 101, "eviction must come off the head");
  assert.equal(seqOf(kept).at(-1), 600);
}
// ③ 单条就超预算：一条都不留（留了就是白留一份超预算的尾巴）
{
  const kept = evict(mk(5, 300 * 1024));
  assert.equal(kept.length, 0, "a single oversized row must not be kept — full refetch is the answer");
}
// ④ 未超预算：一条都不丢
{
  const kept = evict(mk(10, 1024));
  assert.equal(kept.length, 10, "under budget nothing may be evicted");
  assert.equal(seqOf(kept)[0], 1);
}
// ⑤ 空输入不抛
assert.equal(evict([]).length, 0);

// ── R4: probe 放宽（8s + 连续 2 次失败才强断，收消息清零）────────────────
assert.match(
  sessionSrc,
  /private readonly probeTimeoutMs = 8000;/,
  "probeTimeoutMs must be relaxed to 8s (2s one-strike caused 1005 churn)",
);
assert.match(
  sessionSrc,
  /private consecutiveProbeFailures = 0;/,
  "consecutive probe failure counter missing",
);
assert.match(
  sessionSrc,
  /this\.consecutiveProbeFailures >= this\.maxConsecutiveProbeFailures[\s\S]*?this\.reconnectNow\(\);/,
  "reconnect only after 2 consecutive probe failures",
);
assert.match(
  sessionSrc,
  /this\.consecutiveProbeFailures = 0;\s*\n\s*if \(this\.activeProbeId\) \{\s*\n\s*this\.clearProbe\(\);/,
  "handleMessage must reset probe failures on any message",
);

// ── F2: done 后重锚定（App 级，走既有 restoreActiveSession 路径）────────
// 注意：不能只在 SessionViewer 视图内重拉窗口——applyWindow 替换可视数据后，App 缓存里
// 残留的 seq=0 瞬时轮次会被合并 effect 重新追加 → 最后一轮显示两次。必须在 App 层经
// restoreActiveSession 替换缓存（服务端窗口无 seq=0 时 localTransient 不回填，瞬时被清）。
//
// 2026-10-06 改写断言：这道重锚定原来还有第二个条件 `!isSessionStreaming(sessionKey)`，
// 而它在监听者里**恒为假**（`emit` 先于状态机更新），于是正常回合的重锚定恒被跳过、
// 尾巴却已被清 ⇒ 正在完成的正文当场消失（真机复现：时间线 137 项 → 2 项）。
// 契约改为「重锚定的决策点唯一（handleSessionStreamDone 内），且会重锚定时**不预清**尾巴」，
// 见 docs/session-streaming-rework.md §6.5。
assert.match(
  realtimeSrc,
  /getReplayTargetsForRoot\(rootID\)\.includes\(sessionKey\)/,
  "F2 done-path re-anchor must stay gated on the replay-target set (查看中的会话才重锚定)",
);
assert.match(
  realtimeSrc,
  /void reloadSessionForReplay\(rootID, sessionKey\);/,
  "F2 done-path re-anchor missing",
);
assert.match(
  realtimeSrc,
  /willReanchor\s*\?\s*base\s*:\s*dropTransientExchanges\(base\)/,
  "F2 must not pre-clear the transient tail when a re-anchor is coming",
);
assert.doesNotMatch(
  realtimeSrc,
  /!sessionService\.isSessionStreaming\(/,
  "F2 must not gate the re-anchor on isSessionStreaming (it reads the pre-dispatch state ⇒ always true)",
);
assert.doesNotMatch(viewerSrc, /streamingEdgeRef/, "F2 must not re-anchor inside SessionViewer (would duplicate the persisted turn)");

// ── F3: meta.updated 新 key → replace 重拉 ─────────────────────────────
assert.match(realtimeSrc, /const listHasKey = sessionsRef\.current\.some\(/, "F3 listHasKey missing");
assert.match(
  realtimeSrc,
  /\} else \{\s*\n\s*void loadSessionsForRoot\(rootID, \{ replace: true \}\);\s*\n\s*\}\s*\n\s*if \(multiProjectSessionsEnabled\) \{/,
  "F3 new-key replace branch missing",
);

// ── F4: 列表拉取失败可见（10s 冷却防刷屏）──────────────────────────────
assert.match(
  appSrc,
  /if \(Date\.now\(\) - listLoadErrorAtRef\.current > 10000\) \{\s*\n\s*listLoadErrorAtRef\.current = Date\.now\(\);\s*\n\s*reportError\(\s*\n\s*"session\.list_load_failed"/,
  "F4 reportError with 10s cooldown missing",
);
assert.ok(zhSrc.includes("error.session.listLoadFailed"), "zh locale key missing");
assert.ok(enSrc.includes("error.session.listLoadFailed"), "en locale key missing");

// ── 聊天窗口尺寸：单一常量，杜绝散落 magic number ────────────────────────
// 2026-10-07：20 → 8。首屏载荷的 87% 是 exchange_aux（工具卡），窗口缩小直接砍
// 「打开会话」的载荷；代价是上翻同样历史要多几次 loadMore，而打开会话是高频路径。
assert.match(
  sessionSrc,
  /export const SESSION_WINDOW_SIZE = 8;/,
  "shared chat window size constant missing",
);
assert.match(viewerSrc, /latest:\s*SESSION_WINDOW_SIZE/, "viewer init fetch must use the constant");
assert.match(viewerSrc, /limit:\s*SESSION_WINDOW_SIZE/, "viewer page step must use the constant");
// 首帧种子**不再**截断到 SESSION_WINDOW_SIZE —— 这条断言原本钉住的正是那个 bug。
// 它把「加载过程中砍掉缓存头部」制度化了：切会话时先塌成尾部 20 条，再被服务端
// 窗口整体替换一次，用户看到「完整对话 → 最后一条用户消息 → 回答慢慢补回来」。
// 常量仍然管**服务端取窗**的尺寸（下面两条 latest/limit），但不再管本地种子。
assert.doesNotMatch(
  viewerCode,
  /slice\(-SESSION_WINDOW_SIZE\)/,
  "the first-frame seed must not be truncated — the constant governs the server fetch, not local eviction",
);
assert.doesNotMatch(
  viewerCode,
  /const seedExs = persistedSeed/,
  "no second truncation step for the seed; eviction belongs to the write path only",
);
assert.match(
  appSrc,
  /latest:\s*SESSION_WINDOW_SIZE,/,
  "App restoreActiveSession must use the constant",
);

// ── 身份 overlay 行为（persisted 按 seq，transient 按角色类别 + mindfs_id）──
const visibleUser = { role: "user", content: "相同正文", seq: 9, mindfs_id: "turn-9" };
const visibleAgent = { role: "agent", content: "相同正文", seq: 10, mindfs_id: "turn-9" };
const liveCopy = { role: "assistant", content: "不同正文", mindfs_id: "turn-9" };
const liveRepeat = { role: "agent", content: "相同正文", mindfs_id: "turn-10" };
const noIdentity = { role: "agent", content: "相同正文" };
assert.deepEqual(
  computeTailOverlay({
    exchanges: [visibleUser, visibleAgent, liveCopy, liveRepeat, noIdentity],
    visibleExchanges: [visibleUser, visibleAgent],
  }),
  [liveRepeat, noIdentity],
  "only a matching role category and mindfs_id may suppress a transient row",
);
assert.deepEqual(
  computeTailOverlay({
    exchanges: [{ role: "user", content: "old", seq: 3, mindfs_id: "turn-3" }],
    visibleExchanges: [],
  }).map((item) => item.seq),
  [3],
  "persisted rows absent from the current window must remain renderable",
);
console.log("session-window-cache.test.mjs: OK");
});
