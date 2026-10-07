import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";

/**
 * 会话拉取风暴 + 渲染主线程阻塞的契约守卫（`Scope: G-AU`）。
 *
 * 用户症状：「还是会有莫名其妙的卡死和崩溃」。
 *
 * 实测证据（journalctl，2026-10-06 17:33 → 10-07 08:29，持续 18 小时）：
 *   GET /api/sessions/1791268544-ef85840083c5?...  → 404，22–25 次/秒，单小时 5232 次，
 *   累计 7800+ 次，且在 `mindfs` 与 `日程管理` 两个 root 之间交替。
 * 浏览器每域只有 6 条连接，这个循环把它们全占满 → 其它请求（会话列表、文件、
 * WS 重连）全部排队 → 界面卡死 → 标签页被杀 → 自动重载（对应日志里成对的
 * 页面加载 burst）。
 *
 * 机制（两段，缺一不可）：
 *   ① 加载 effect 的依赖里有 `selectedSessionSnapshot` 这个**对象**，而它的身份随
 *      `cacheVersion` 每次 bump 都变（`getSessionSnapshot` 的 dep 数组含 cacheVersion），
 *      流式输出每 30ms bump 一次 → effect 每轮都重跑。
 *   ② `loadedSessionRef` 只在**成功**时置位，404 的会话永远兜不住 → 没有终点。
 *
 * 这一层钉的是形状：这两处都是「静默退化」型改动 —— 测试全绿、短会话看不出问题，
 * 只有长时间挂着标签页 + 有 agent 在流式输出时才炸。
 */

const root = path.resolve(import.meta.dirname, "..");
const read = (rel) => fs.readFileSync(path.join(root, rel), "utf8");
const app = read("src/App.tsx");
const viewer = read("src/components/SessionViewer.tsx");

// ── ① 加载 effect 只能依赖「身份」，不能依赖快照对象 ─────────────────────────
assert.match(
  app,
  /const loadSessionKey = selectedSession\?\.key \|\| selectedSession\?\.session_key \|\| "";/,
  "the load effect must key on the session key string, not the snapshot object",
);
assert.match(
  app,
  /const loadRootID =/,
  "the load effect must key on the root id string",
);
assert.match(
  app,
  /const loadSnapshotHasExchanges = hasSessionExchanges\(/,
  "the load effect must depend on the has-exchanges boolean, not the snapshot object",
);

// 依赖数组里必须出现这三个原始量，且**不能**再出现 selectedSessionSnapshot。
const depsStart = app.indexOf("loadSessionKey,\n    loadRootID,\n    loadSnapshotHasExchanges,");
assert.ok(depsStart > 0, "the load effect dep array must list the three identity primitives");
const loadEffectDeps = app.slice(depsStart, depsStart + 700);
for (const token of ["loadSessionKey,", "loadRootID,", "loadSnapshotHasExchanges,"]) {
  assert.ok(loadEffectDeps.includes(token), `load effect deps must include ${token}`);
}
assert.doesNotMatch(
  loadEffectDeps,
  /selectedSessionSnapshot/,
  "selectedSessionSnapshot must NOT be a load-effect dep — its identity changes on every cacheVersion bump",
);

// ── ② 失败必须留下痕迹，且 404 是终点 ──────────────────────────────────────
assert.match(
  app,
  /const sessionLoadFailureRef = useRef<Record<string, \{ at: number; status: number \}\>>\(\{\}\);/,
  "failed loads must be recorded, otherwise a 404 is indistinguishable from 'not loaded yet'",
);
assert.match(
  app,
  /sessionLoadFailureRef\.current\[cacheKey\] = \{ at: Date\.now\(\), status \};/,
  "restoreActiveSession must record the failure status",
);
assert.match(
  app,
  /const failure = sessionLoadFailureRef\.current\[cacheKey\];/,
  "the load effect must consult the failure record before fetching",
);
assert.match(
  app,
  /failure\.status === 404/,
  "a 404 must be treated as terminal — the session does not exist, retrying is pointless",
);

// 失败记录必须在成功 / 显式标脏时解禁，否则「点重试」永远没反应。
assert.match(
  app,
  /delete sessionLoadFailureRef\.current\[cacheKey\];/,
  "a successful load must clear the failure record",
);
assert.match(
  app,
  /delete sessionLoadFailureRef\.current\[rootSessionKey\(resolvedRoot, resolvedKey\)\];/,
  "markSessionStale is the app's own 'reload this session' signal and must re-enable loading",
);

// ── ③ 内存会话缓存必须有上限 ──────────────────────────────────────────────
// IDB 那份有（500 条 / 200KB），内存这份原先没有 —— 长时间浏览堆到几百 MB。
assert.match(
  app,
  /const SESSION_CACHE_MAX_ENTRIES = 64;/,
  "the in-memory session cache needs an explicit entry bound",
);
assert.match(
  app,
  /const enforceSessionCacheBound = useCallback\(\(\) => \{/,
  "the bound must be enforced by a helper",
);
assert.ok(
  (app.match(/enforceSessionCacheBound\(\);/g) || []).length >= 2,
  "every new-key write into sessionCacheRef must enforce the bound",
);

// ── ④ 渲染主线程：O(n²) 必须消失 ──────────────────────────────────────────
assert.doesNotMatch(
  viewer,
  /timeline\.slice\(0, idx \+ 1\)\.filter\(/,
  "per-item user-message indexing must not slice+filter the whole prefix (O(n^2))",
);
assert.doesNotMatch(
  viewer,
  /previousUserTimestamp\(timeline, idx\)/,
  "per-item previous-user lookup must not rescan backwards (O(n^2))",
);
assert.match(
  viewer,
  /const timelineItemMeta = useMemo\(/,
  "the per-item metadata must be precomputed in one O(n) pass",
);

// ── ⑤ 滚动处理必须节流 ────────────────────────────────────────────────────
assert.match(
  viewer,
  /stickinessFrame = window\.requestAnimationFrame\(/,
  "the scroll handler must be coalesced to one DOM query per frame",
);
assert.doesNotMatch(
  viewer,
  /el\.addEventListener\("scroll", updateStickiness, \{ passive: true \}\);[\s\S]{0,400}querySelectorAll/,
  "the scroll handler must not run querySelectorAll on every scroll event",
);
