import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import ts from "typescript";
import vm from "node:vm";

const sessionServicePath = path.resolve(import.meta.dirname, "../src/services/session.ts");
const sessionViewerPath = path.resolve(import.meta.dirname, "../src/components/SessionViewer.tsx");
const managerPath = path.resolve(import.meta.dirname, "../../server/internal/session/manager.go");
const httpPath = path.resolve(import.meta.dirname, "../../server/internal/api/http.go");

const sessionSrc = fs.readFileSync(sessionServicePath, "utf8");
const viewerSrc = fs.readFileSync(sessionViewerPath, "utf8");
const managerSrc = fs.readFileSync(managerPath, "utf8");
const httpSrc = fs.readFileSync(httpPath, "utf8");

// ── session.ts 导出与隔离 ──────────────────────────────────────────────
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
assert.match(sessionSrc, /forceNoTruncate/, "forceNoTruncate wiring missing");

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
assert.match(managerSrc, /func \(m \*Manager\) CountExchanges/, "manager.go CountExchanges missing");
assert.match(managerSrc, /func firstIndexWhereSeq/, "firstIndexWhereSeq helper missing");
assert.match(managerSrc, /if limit <= 0 \{\s*\n\s*limit = 50/, "limit default 50 clamp missing");
assert.match(managerSrc, /if limit > 200/, "limit >200 clamp missing");
assert.match(managerSrc, /case latest > 0:/, "latest branch missing");
assert.match(managerSrc, /case beforeSeq > 0:/, "beforeSeq branch missing");
assert.match(managerSrc, /hasMore = start > 0/, "hasMore computation missing");

// ── 后端 http.go 契约 ──────────────────────────────────────────────────
assert.match(httpSrc, /parsePositiveIntQuery\(r, "before_seq"\)/, "http.go before_seq parsing missing");
assert.match(httpSrc, /parsePositiveIntQuery\(r, "latest"\)/, "http.go latest parsing missing");
assert.match(httpSrc, /seq 与 before_seq\/latest 互斥/, "mutual exclusion error message missing");
assert.match(httpSrc, /windowMeta \*session\.SessionWindowMeta/, "sessionResponse windowMeta param missing");
assert.match(httpSrc, /window_meta/, "window_meta response missing");
assert.match(httpSrc, /windowSeqs/, "windowSeqs aux filtering missing");
assert.match(httpSrc, /handleSessionSync[\s\S]*?before_seq/, "handleSessionSync window wiring missing");

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
const appSrc = fs.readFileSync(path.resolve(import.meta.dirname, "../src/App.tsx"), "utf8");
assert.ok(
  !fs.existsSync(path.resolve(import.meta.dirname, "../src/services/sessionWindowMerge.ts")),
  "sessionWindowMerge.ts must stay deleted",
);
assert.doesNotMatch(viewerSrc, /mergeWindowedTail/, "seq merge model must not return");
// overlay 尾巴派生 + 组合输入
assert.match(
  viewerSrc,
  /const tailOverlay = useMemo\(\(\) => \{[\s\S]*?const exs = Array\.isArray\(session\?\.exchanges\)/,
  "tail overlay derivation from cache missing",
);
assert.match(
  viewerSrc,
  /const composedExchanges = useMemo\(\s*\n\s*\(\) => \[\.\.\.visibleExchanges, \.\.\.tailOverlay\],/,
  "composed window+overlay input missing",
);
// 判定不再依赖 windowMeta（loadMore/targetSeq 会把它覆盖成旧窗口的 meta）：
// 已持久化条目按 latestSeq（只增、按会话键绑定）判定；seq=0 的用户条目按内容与窗口计数消抵；
// seq=0 的直播正文/思考默认保留，但窗口最新持久化行已包含它（去空白判定）时让位——否则
// 同一轮会在 ask 卡上下各渲染一块（实测 2026-09-17）。
assert.match(
  viewerSrc,
  /const latestSeq = latestSeqState\.key === sessionKey \? latestSeqState\.max : 0;/,
  "per-session latestSeq derivation missing",
);
assert.match(
  viewerSrc,
  /noteLatestSeq\(sessionKey, res\.meta\);/,
  "applyWindow must report the latest-window maxSeq (loadMore/targetSeq must not)",
);
assert.match(
  viewerSrc,
  /const \[latestSeqState, setLatestSeqState\] = useState<\{ key: string; max: number \}>/,
  "latestSeq must be monotonic and keyed by session",
);
assert.match(
  viewerSrc,
  /const visibleSeqSet = useMemo\(\(\) => \{/,
  "visible window seq set missing",
);
assert.match(
  viewerSrc,
  /const windowUserCounts = useMemo\(\(\) => \{/,
  "window user content counts (for overlay de-dup) missing",
);
assert.match(
  viewerSrc,
  /const covered = windowUserCounts\.get\(content\) \|\| 0;/,
  "seq=0 user entry must be offset by the window's same-content count",
);
assert.doesNotMatch(
  viewerSrc,
  /staleCount|windowMeta\.maxSeq - cacheMaxSeq/,
  "positional stale trim must not return (it ate live streaming items)",
);
// seq=0 的直播正文也要能对账：窗口最新持久化行已包含它 → 让位（去空白后判定）
assert.match(
  viewerSrc,
  /const windowTailTexts = useMemo\(\(\) => \{/,
  "window tail texts (for live-copy reconciliation) missing",
);
assert.match(
  viewerSrc,
  /renderedPersistedTexts\.some\(\(text\) => text\.includes\(transientText\)\)/,
  "seq=0 live text must yield once the rendered persisted text already contains it",
);
assert.match(
  viewerSrc,
  /const renderedPersistedTexts = windowTailTexts\.slice\(\);/,
  "the corpus must also include the persisted rows the overlay itself renders",
);
assert.match(
  viewerSrc,
  /function normalizeOverlayText\(value: string\): string \{/,
  "whitespace-insensitive overlay comparison helper missing",
);
// init 种子只取持久化部分，避免与 overlay 重复
assert.match(
  viewerSrc,
  /const persistedSeed = incomingExs\.filter\(\(e\) => Number\(\(e as any\)\?\.seq \|\| 0\) > 0\);/,
  "init seed must be persisted-only (overlay owns the seq=0 tail)",
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
assert.match(
  appSrc,
  /getReplayTargetsForRoot\(rootID\)\.includes\(sessionKey\) &&\s*\n\s*!sessionService\.isSessionStreaming\(sessionKey\)\s*\n\s*\) \{\s*\n\s*void reloadSessionForReplay\(rootID, sessionKey\);/,
  "F2 done-path re-anchor (viewing-sessions only, guarded) missing",
);
assert.doesNotMatch(viewerSrc, /streamingEdgeRef/, "F2 must not re-anchor inside SessionViewer (would duplicate the persisted turn)");

// ── F3: meta.updated 新 key → replace 重拉 ─────────────────────────────
assert.match(appSrc, /const listHasKey = sessionsRef\.current\.some\(/, "F3 listHasKey missing");
assert.match(
  appSrc,
  /\} else \{\s*\n\s*void loadSessionsForRoot\(rootID, \{ replace: true \}\);\s*\n\s*\}\s*\n\s*if \(multiProjectSessionsEnabled\) \{/,
  "F3 new-key replace branch missing",
);

// ── F4: 列表拉取失败可见（10s 冷却防刷屏）──────────────────────────────
assert.match(
  appSrc,
  /if \(Date\.now\(\) - listLoadErrorAtRef\.current > 10000\) \{\s*\n\s*listLoadErrorAtRef\.current = Date\.now\(\);\s*\n\s*reportError\(\s*\n\s*"session\.list_load_failed"/,
  "F4 reportError with 10s cooldown missing",
);
const zhSrc = fs.readFileSync(path.resolve(import.meta.dirname, "../src/i18n/locales/zh-CN.ts"), "utf8");
const enSrc = fs.readFileSync(path.resolve(import.meta.dirname, "../src/i18n/locales/en-US.ts"), "utf8");
assert.ok(zhSrc.includes("error.session.listLoadFailed"), "zh locale key missing");
assert.ok(enSrc.includes("error.session.listLoadFailed"), "en locale key missing");

// ── 聊天窗口尺寸：单一常量，杜绝散落 magic number ────────────────────────
assert.match(
  sessionSrc,
  /export const SESSION_WINDOW_SIZE = 20;/,
  "shared chat window size constant missing",
);
assert.match(viewerSrc, /latest:\s*SESSION_WINDOW_SIZE/, "viewer init fetch must use the constant");
assert.match(viewerSrc, /limit:\s*SESSION_WINDOW_SIZE/, "viewer page step must use the constant");
assert.match(
  viewerSrc,
  /persistedSeed\.slice\(-SESSION_WINDOW_SIZE\)/,
  "viewer first-frame seed must use the constant",
);
assert.match(
  appSrc,
  /latest:\s*SESSION_WINDOW_SIZE,/,
  "App restoreActiveSession must use the constant",
);

// ── overlay 判定（与 SessionViewer.tailOverlay 同构）：窗口是权威持久化源 ───
// ①窗口已含的 seq 不重复渲染 ②实时流式项永不丢 ③陈旧乐观用户拷贝被丢弃
// ④真正的重复发言仍显示 ⑤刚发出(seq>latestSeq)的条目保留 ⑥未锚定时不灌历史
function buildTailOverlay(cacheExchanges, visibleExchanges, latestSeq) {
  const visibleSeqSet = new Set();
  const windowUserCounts = new Map();
  for (const ex of visibleExchanges) {
    const seq = Number(ex?.seq || 0);
    if (seq > 0) visibleSeqSet.add(seq);
    if (String(ex?.role || "").toLowerCase() === "user") {
      const content = String(ex?.content || "");
      windowUserCounts.set(content, (windowUserCounts.get(content) || 0) + 1);
    }
  }
  const consumed = new Map();
  const windowTailTexts = [];
  for (const ex of visibleExchanges
    .filter((e) => Number(e?.seq || 0) > 0)
    .slice(-3)) {
    const text = String(ex?.content || "").replace(/\s+/g, "");
    if (text) windowTailTexts.push(text);
  }
  // 已落盘但还没进窗口的条目由 overlay 自己渲染 —— 它们也算「已经显示过的一份」
  const renderedPersistedTexts = windowTailTexts.slice();
  for (const ex of cacheExchanges) {
    const seq = Number(ex?.seq || 0);
    if (seq <= 0) continue;
    if (visibleSeqSet.has(seq)) continue;
    if (latestSeq === 0 || seq <= latestSeq) continue;
    const text = String(ex?.content || "").replace(/\s+/g, "");
    if (text) renderedPersistedTexts.push(text);
  }
  const out = [];
  for (const ex of cacheExchanges) {
    const seq = Number(ex?.seq || 0);
    if (seq > 0) {
      if (visibleSeqSet.has(seq)) continue;
      if (latestSeq === 0 || seq <= latestSeq) continue;
      out.push(ex);
      continue;
    }
    if (String(ex?.role || "").toLowerCase() === "user") {
      const content = String(ex?.content || "");
      const covered = windowUserCounts.get(content) || 0;
      const used = consumed.get(content) || 0;
      if (used < covered) {
        consumed.set(content, used + 1);
        continue;
      }
    }
    const transientText = String(ex?.content || "").replace(/\s+/g, "");
    if (
      transientText.length >= 32 &&
      renderedPersistedTexts.some((text) => text.includes(transientText))
    ) {
      continue;
    }
    out.push(ex);
  }
  return out;
}
const win = [
  { role: "user", content: "A", seq: 9 },
  { role: "assistant", content: "R9", seq: 10 },
];
assert.equal(
  buildTailOverlay([{ role: "user", content: "A", seq: 9 }], win, 10).length,
  0,
  "① cache entry already present in the window must not re-render",
);
assert.equal(
  buildTailOverlay([{ role: "assistant", content: "streaming..." }], win, 10).length,
  1,
  "② live streaming items must never be trimmed",
);
assert.equal(
  buildTailOverlay([{ role: "user", content: "A" }], win, 10).length,
  0,
  "③ stale optimistic user copy must be dropped once the window has the same content",
);
assert.equal(
  buildTailOverlay(
    [
      { role: "user", content: "继续" },
      { role: "user", content: "继续" },
    ],
    [{ role: "user", content: "继续", seq: 9 }],
    9,
  ).length,
  1,
  "④ a genuinely repeated message must still show",
);
assert.equal(
  buildTailOverlay([{ role: "user", content: "新", seq: 11 }], win, 10).length,
  1,
  "⑤ just-sent message beyond the window's latest seq must stay visible",
);
assert.equal(
  buildTailOverlay([{ role: "user", content: "旧", seq: 3 }], win, 0).length,
  0,
  "⑥ before anchoring, cached persisted entries must not flood the overlay",
);

// ⑦⑧⑨ 缺口回归（2026-09-17）：同一轮的直播拷贝与落盘正文只差空白，必须让位。
// 服务端 appendResponseChunk 在相邻文本块之间补 "\n\n"，流式 chunk 不带。
const askTurnText =
  "竞品对比页的信息我核对了一下：仓库里的竞品档案只把 Moiré 当作一种「传统模态」记录，没有香港的具体机型。先查一下：";
const askWin = [
  { role: "user", content: "推进竞品对比", seq: 21 },
  {
    role: "assistant",
    content:
      "竞品对比页的信息我核对了一下：仓库里的竞品档案只把 Moiré 当作一种「传统模态」记录，\n\n没有香港的具体机型。先查一下：\n\n明白了——布局不动。",
    seq: 22,
  },
];
assert.equal(
  buildTailOverlay([{ role: "agent", content: askTurnText }], askWin, 22).length,
  0,
  "⑦ live copy of a turn already persisted in the window must yield (ask 上下各一块的根因)",
);
assert.equal(
  buildTailOverlay(
    [
      {
        role: "agent",
        content: `${askTurnText}下面是窗口里还没有的一段全新输出，仍在流式。`,
      },
    ],
    askWin,
    22,
  ).length,
  1,
  "⑧ live text the window does not contain yet must stay visible",
);
assert.equal(
  buildTailOverlay([{ role: "agent", content: "好的" }], askWin, 22).length,
  1,
  "⑨ short fragments must not be reconciled away (真·合法重复发言)",
);

// ⑩ 实测 2026-09-17 20:16（PC 节点「为 MindFS 添加登录功能」）：刚落盘那一行还没被重锚定
// 拉进窗口（latestSeq 仍是上一轮），由 overlay 的 seq>0 分支渲染；seq=0 的直播拷贝必须照样
// 让位，否则整轮渲染两次——ask 卡上下各一整轮。
const liveTurnText =
  "看完了现有链路，先把关键事实说清楚（避免重复造轮子）：- 现在**没有**任何登录。";
const persistedRow = {
  role: "agent",
  seq: 2,
  // 落盘正文：服务端 appendResponseChunk 在文本块之间补了 "\n\n"，直播 chunk 不带
  content:
    "I'll explore the codebase first to understand what's already there before proposing anything.\n\n" +
    "看完了现有链路，先把关键事实说清楚（避免重复造轮子）：\n\n" +
    "- 现在**没有**任何登录。",
};
assert.equal(
  buildTailOverlay(
    [
      { role: "user", content: "请给 mindfs 加上登录功能", seq: 1 },
      persistedRow,
      { role: "agent", content: liveTurnText },
    ],
    // 窗口还停在上一轮：不含 seq=2
    [{ role: "user", content: "请给 mindfs 加上登录功能", seq: 1 }],
    1,
  ).length,
  1,
  "⑩ a persisted row rendered by the overlay itself must also claim the live copy",
);

console.log("session-window.test.mjs: OK");
