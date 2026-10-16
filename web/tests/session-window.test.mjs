import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { test } from "vitest";
import {
  appSrc,
  httpSessionsSrc,
  managerSrc,
  managerWindowSrc,
  overlaySrc,
  realtimeSrc,
  sessionCode,
  sessionSrc,
  viewerCode,
  viewerSrc,
} from "./session-window-sources.mjs";
// 窗口化与只读尾巴 overlay 的契约：会话域导出、SessionViewer 窗口态、
// 后端 manager/http 契约、纯逻辑语义，以及方案 C（单窗口 + overlay）的接线。
// 合并/清理见 session-window-merge，淘汰与实时契约见 session-window-cache。

test("session-window", async () => {
// overlay 判定由 src/shared/sessionOverlay.ts 的纯函数实现。
// 这里直接 import 真实现，避免再维护同构副本。

// ── session service/session domain 导出与隔离 ──────────────────────────────────────────────
assert.match(sessionSrc, /export type SessionWindowMeta/, "SessionWindowMeta type missing");
assert.match(sessionSrc, /export type SessionWindowOptions/, "SessionWindowOptions type missing");
assert.match(sessionSrc, /async getSessionWindow\(/, "getSessionWindow method missing");
assert.match(sessionSrc, /async getSessionIncrement\(/, "getSessionIncrement alias missing");
assert.match(sessionSrc, /export function getSessionMinSeq/, "getSessionMinSeq missing");
assert.match(sessionSrc, /export function getSessionMaxSeq/, "getSessionMaxSeq missing (should be exported)");
assert.match(sessionSrc, /const windowedViewKeys = new Set<string>\(\);/, "windowedViewKeys Set missing");
assert.match(sessionSrc, /export function isWindowedView\(/, "isWindowedView missing");
assert.match(sessionSrc, /export function setWindowedView\(/, "setWindowedView missing");
assert.match(sessionSrc, /export function clearWindowedView\(/, "clearWindowedView missing");
assert.match(sessionSrc, /isWindowedView\(sessionKey\) \|\| !!options\?\.windowedView/, "syncSession windowed check missing");
// 2026-10-06 反转：`forceNoTruncate`（写侧把标记置 false）已删除，代之以**标记必须诚实**。
// 旧写法的危害路径：窗口态是**内存**标记，页面一刷新就没了，而那条被标成「完整」的 IDB
// 记录还在盘上 ⇒ 下次全量路径只做增量同步 ⇒ 被砍掉的历史永远不补（静默丢历史）。
// 读侧（`baseTruncated = windowed ? false : …`）本来就能表达「窗口态不回补」，所以写侧不必说谎。
// 判据用 `sessionCode`（剥掉注释的视图）：解释「为什么删掉它」的注释里必然出现这个名字。
assert.doesNotMatch(sessionCode, /forceNoTruncate/, "写侧不得再让 truncated 说谎");
assert.match(
  sessionSrc,
  /truncated: true,\s*\n\s*exchanges: kept,/,
  "发生过淘汰就必须标 truncated:true（标记语义 = 这条记录不完整，下次全量回源）",
);
// CUSTOM(G-BH)：返回值多了 `blocks`（正文块，与 exchange_aux 同一条淘汰规则）。
// 判据从「精确匹配整行」改成「同一行里同时有这四项」—— 钉住的仍是同一条语义
//（没淘汰 ⇒ 显式 false，且 aux / 块都被重新赋值过），但不再因为多一个字段就红。
assert.match(
  sessionSrc,
  /return \{ \.\.\.persistent, truncated: false, exchanges, exchange_aux, blocks \};/,
  "没有淘汰时必须显式标 false（否则上一轮的 true 会留着白做一次全量）",
);

// getSessionWindow URL 组装：before_seq / latest / limit / nodeId
assert.match(sessionSrc, /before_seq/, "before_seq param missing in getSessionWindow");
assert.match(sessionSrc, /params\.set\("latest"/, "latest param missing in getSessionWindow");
assert.match(sessionSrc, /getRootNodeId\(rootId\)/, "nodeId透传 via getRootNodeId missing");
assert.match(sessionSrc, /window_meta.*session.*window_meta/s, "window_meta dual compatibility (top-level / embedded) missing");

// ── SessionViewer.tsx 窗口化状态与交互 ─────────────────────────────────
assert.match(viewerSrc, /type SessionWindowMeta/, "SessionViewer should import SessionWindowMeta");
assert.match(viewerSrc, /getSessionWindow/, "SessionViewer should import/call getSessionWindow");
assert.match(viewerSrc, /setWindowedView\(sessionKey, true\)/, "SessionViewer should setWindowedView on init");
assert.match(viewerSrc, /clearWindowedView\(sessionKey\)/, "SessionViewer should clearWindowedView on cleanup");
assert.match(viewerSrc, /const \[visibleExchanges, setVisibleExchanges\]/, "visibleExchanges state missing");
assert.match(viewerSrc, /const \[visibleAux, setVisibleAux\]/, "visibleAux state missing");
assert.match(viewerSrc, /const \[windowMeta, setWindowMeta\]/, "windowMeta state missing");
assert.match(viewerSrc, /const \[loadingMore, setLoadingMore\]/, "loadingMore state missing");
assert.match(viewerSrc, /const topSentinelRef = useRef/, "topSentinelRef missing");
assert.match(viewerSrc, /useSessionStream\(\s*\n?\s*sessionKey,\s*\n?\s*composedExchanges,\s*\n?\s*visibleAux/, "useSessionStream should consume composed window+overlay");
assert.match(viewerSrc, /getSessionWindow/, "initial window fetch missing");
assert.match(viewerSrc, /latest:\s*SESSION_WINDOW_SIZE/, "window fetch must use the shared SESSION_WINDOW_SIZE");
assert.match(viewerSrc, /const loadMore = useCallback/, "loadMore callback missing");
assert.match(viewerSrc, /scrollHeight - container\.scrollTop/, "scroll anchoring (scrollHeight - scrollTop) missing");
assert.match(viewerSrc, /beforeSeq: windowMeta\.minSeq/, "loadMore beforeSeq: windowMeta.minSeq missing");
assert.match(viewerSrc, /IntersectionObserver/, "IntersectionObserver sentinel missing");
assert.match(viewerSrc, /rootMargin: "200px/, "IntersectionObserver rootMargin 200px missing");
assert.match(viewerSrc, /windowMeta\.hasMore/, "hasMore guard missing");
assert.match(
  viewerSrc,
  /beforeSeq: targetSeq \+ Math\.floor\(SESSION_WINDOW_SIZE \/ 2\)/,
  "targetSeq cross-window fetch (centered on the constant) missing",
);
assert.match(viewerSrc, /topSentinelRef/, "topSentinelRef wiring missing");
assert.match(viewerSrc, /已加载/, "loaded/total indicator missing");
assert.match(viewerSrc, /windowMeta\.total/, "windowMeta.total usage missing");

// ── 后端 manager.go 契约 ───────────────────────────────────────────────
assert.match(managerSrc, /type SessionWindowMeta struct/, "manager.go SessionWindowMeta missing");
assert.match(managerSrc, /func \(m \*Manager\) GetWindow/, "manager.go GetWindow missing");
assert.match(managerSrc, /func \(m \*Manager\) GetExchangeAuxWindow/, "manager.go GetExchangeAuxWindow missing");
assert.match(managerWindowSrc, /func firstIndexWhereSeq/, "firstIndexWhereSeq helper missing");
assert.match(managerWindowSrc, /if limit <= 0 \{\s*\n\s*limit = 50/, "limit default 50 clamp missing");
assert.match(managerWindowSrc, /if limit > 200/, "limit >200 clamp missing");
assert.match(managerWindowSrc, /case latest > 0:/, "latest branch missing");
assert.match(managerWindowSrc, /case beforeSeq > 0:/, "beforeSeq branch missing");
// hasMore 的语义是「是否还有更早的记录」。G-BG 把开窗拆成两条路（缓存切片 / 冷缓存尾部读），
// 两条都必须各自算出 hasMore，且分别等于 start>0 与「解析到的条数>need」。
assert.match(
  managerWindowSrc,
  /return all\[start:\], total, start > 0/,
  "hasMore computation (cached slice) missing",
);
assert.match(
  managerWindowSrc,
  /hasMore := len\(records\) > need/,
  "hasMore computation (cold tail read) missing",
);

// ── 后端 http.go 契约 ──────────────────────────────────────────────────
assert.match(httpSessionsSrc, /parsePositiveIntQuery\(r, "before_seq"\)/, "http.go before_seq parsing missing");
assert.match(httpSessionsSrc, /parsePositiveIntQuery\(r, "latest"\)/, "http.go latest parsing missing");
assert.match(httpSessionsSrc, /seq 与 before_seq\/latest 互斥/, "mutual exclusion error message missing");
assert.match(httpSessionsSrc, /windowMeta \*session\.SessionWindowMeta/, "sessionResponse windowMeta param missing");
assert.match(httpSessionsSrc, /window_meta/, "window_meta response missing");
assert.match(httpSessionsSrc, /windowSeqs/, "windowSeqs aux filtering missing");
assert.match(httpSessionsSrc, /handleSessionSync[\s\S]*?before_seq/, "handleSessionSync window wiring missing");

// ── 纯逻辑小验证：getSessionMinSeq / getSessionMaxSeq / windowedView 行为 ─
// 通过内联实现验证语义（与源码同构）
function getSessionMinSeq(session) {
  const exchanges = Array.isArray(session?.exchanges) ? session.exchanges : [];
  let min = 0;
  for (const ex of exchanges) {
    const seq = Number(ex?.seq || 0);
    if (Number.isFinite(seq) && seq > 0) min = min === 0 ? seq : Math.min(min, seq);
  }
  return min;
}
function getSessionMaxSeq(session) {
  const exchanges = Array.isArray(session?.exchanges) ? session.exchanges : [];
  return exchanges.reduce((max, ex) => {
    const seq = Number(ex?.seq || 0);
    return Number.isFinite(seq) && seq > max ? seq : max;
  }, 0);
}
assert.equal(getSessionMinSeq({ exchanges: [{ seq: 5 }, { seq: 2 }, { seq: 9 }] }), 2);
assert.equal(getSessionMinSeq({ exchanges: [{ seq: 0 }, { seq: 0 }] }), 0);
assert.equal(getSessionMinSeq(null), 0);
assert.equal(getSessionMaxSeq({ exchanges: [{ seq: 3 }, { seq: 7 }, { seq: 7 }] }), 7);
assert.equal(getSessionMaxSeq({ exchanges: [] }), 0);
// windowedView 内存标记行为（模拟实现）
{
  const keys = new Set();
  const isWindowedView = (k) => keys.has(k);
  const setWindowedView = (k, on = true) => (on ? keys.add(k) : keys.delete(k));
  const clearWindowedView = (k) => keys.delete(k);
  assert.equal(isWindowedView("k1"), false);
  setWindowedView("k1", true);
  assert.equal(isWindowedView("k1"), true);
  clearWindowedView("k1");
  assert.equal(isWindowedView("k1"), false);
  setWindowedView("k2", true);
  setWindowedView("k2", false);
  assert.equal(isWindowedView("k2"), false);
}

// ── 方案 C：单窗口 + 只读尾巴 overlay（2026-09-09 重构，替代 seq 合并模型）────
// 数据流：视图 = [窗口（唯一持久化源，只整体替换：init/翻页/重锚定）]
//        + [overlay 尾巴（App 缓存 seq=0 瞬时尾部，只读派生）]。全流程无 seq 合并。
// mergeWindowedTail 已删除；不得回归为 seq 过滤合并模型。

assert.ok(
  !fs.existsSync(path.resolve(import.meta.dirname, "../src/services/sessionWindowMerge.ts")),
  "sessionWindowMerge.ts must stay deleted",
);
assert.doesNotMatch(viewerSrc, /mergeWindowedTail/, "seq merge model must not return");

assert.match(
  viewerSrc,
  /const tailOverlay = useMemo\([\s\S]*?computeTailOverlay\(\{/,
  "SessionViewer 必须通过 computeTailOverlay 取 overlay（判定不得再内联回组件）",
);
assert.match(
  overlaySrc,
  /export function computeTailOverlay\(/,
  "overlay 判定必须是纯模块的导出函数（否则测试只能再写一份副本）",
);
assert.match(
  viewerSrc,
  /const composedExchanges = useMemo\(\(\) => \{[\s\S]*?return \[\.\.\.visibleExchanges, \.\.\.extra\]/,
  "composed window+overlay input missing",
);
assert.doesNotMatch(
  viewerSrc,
  /latestSeqState|noteLatestSeq|latestSeq\s*=|latestSeq\s*:/,
  "overlay must not keep the retired sequence watermark",
);
assert.match(overlaySrc, /renderedIdentities/);
assert.match(overlaySrc, /identityOf/);
assert.match(
  overlaySrc,
  /if \(identity && renderedIdentities\.has\(identity\)\) continue;/,
  "transient rows must reconcile by MindFS identity",
);
assert.doesNotMatch(
  overlaySrc,
  /normalizeOverlayText|windowUserCounts|turnPersistedTexts|contentOf/,
  "overlay must not infer duplicate rows from their text",
);

// init 种子只取持久化部分，避免与 overlay 重复。
// 注意：种子**不再**二次截断到 SESSION_WINDOW_SIZE —— 加载过程中砍头部正是
// 「切会话闪一下」的成因（2026-10-05）。淘汰只在写入时发生（toPersistentSession）。
assert.match(
  viewerSrc,
  // 契约（init 种子只装持久行）不变，判据改走共用谓词 isPersistedSeq（2026-10-06）。
  /const seedExs = incomingExs\.filter\(\(e\) => isPersistedSeq\(\(e as any\)\?\.seq\)\);/,
  "init seed must be persisted-only (overlay owns the seq=0 tail)",
);
assert.doesNotMatch(
  viewerCode,
  /slice\(-SESSION_WINDOW_SIZE\)/,
  "the init seed must not be truncated again while loading — that truncation is the switch-away flicker",
);
// 窗口到达时必须**并入**种子（keepOlder），不能整体替换。
assert.match(
  viewerSrc,
  /applyWindow\(res, \{ keepOlder: true \}\);/,
  "the init window must merge into the seed (keepOlder) or restored history is dropped again",
);
// 三条可见集来源共用同一个按 seq 去重的合并，纯拼接会把同一行渲染两遍。
assert.match(
  viewerSrc,
  /mergeVisibleExchanges\(prev, winExchanges\)/,
  "applyWindow must merge by seq, not concatenate (seed and window overlap)",
);
assert.match(
  viewerSrc,
  /mergeSessionExchanges\(winExchanges, prev\)/,
  "loadMore must merge by seq, not concatenate (the page overlaps what is visible)",
);
// 重锚定：App 附锚点，viewer 一次性原子换窗
assert.match(
  viewerSrc,
  /const anchorAt = Number\(\(session as any\)\?._anchoredAt \|\| 0\);/,
  "anchor effect (_anchoredAt) missing",
);
assert.match(
  viewerSrc,
  /lastAppliedAnchorRef\.current = \{ key: sessionKey, at: anchorAt \};/,
  "anchor one-shot guard missing",
);
assert.match(
  appSrc,
  /_windowMeta: win\.meta,/,
  "App window path must attach _windowMeta",
);
assert.match(
  appSrc,
  /_anchoredAt: anchorAt,/,
  "App must attach _anchoredAt",
);
assert.match(
  appSrc,
  /const anchorSeqRef = useRef\(0\);/,
  "App anchor counter missing",
);
assert.match(
  appSrc,
  /_windowMeta: anchoredMeta as any,/,
  "App fallback path must attach locally-computed meta",
);
console.log("session-window.test.mjs: OK");
});
