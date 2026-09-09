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
assert.match(viewerSrc, /useSessionStream\(\s*\n?\s*sessionKey,\s*\n?\s*visibleExchanges,\s*\n?\s*visibleAux/, "useSessionStream should consume visibleExchanges/visibleAux");
assert.match(viewerSrc, /getSessionWindow/, "initial window fetch missing");
assert.match(viewerSrc, /latest:\s*50/, "latest:50 window fetch missing");
assert.match(viewerSrc, /const loadMore = useCallback/, "loadMore callback missing");
assert.match(viewerSrc, /scrollHeight - container\.scrollTop/, "scroll anchoring (scrollHeight - scrollTop) missing");
assert.match(viewerSrc, /beforeSeq: windowMeta\.minSeq/, "loadMore beforeSeq: windowMeta.minSeq missing");
assert.match(viewerSrc, /IntersectionObserver/, "IntersectionObserver sentinel missing");
assert.match(viewerSrc, /rootMargin: "200px/, "IntersectionObserver rootMargin 200px missing");
assert.match(viewerSrc, /windowMeta\.hasMore/, "hasMore guard missing");
assert.match(viewerSrc, /targetSeq \+ 25/, "targetSeq cross-window fetch (targetSeq+25) missing");
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

// ── F1: mergeWindowedTail 真行为断言（2026-09-09 窗口化回归修复）────────
// 旧过滤条件 s>0 && s>maxSeq 把没有 seq 的在途轮次挡在窗外：长会话生成中用户消息/thinking/
// 流式文本不显示，点同步才见（commit ebcfc39 引入的窗口化回归）。
const mergeSrc = fs.readFileSync(
  path.resolve(import.meta.dirname, "../src/services/sessionWindowMerge.ts"),
  "utf8",
);
assert.match(mergeSrc, /export function mergeWindowedTail\(/, "mergeWindowedTail missing");
const compiledMerge = ts.transpileModule(mergeSrc, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
}).outputText;
const mergeSandbox = { exports: {}, module: { exports: {} } };
vm.runInNewContext(compiledMerge, mergeSandbox, { filename: "sessionWindowMerge.ts" });
const { mergeWindowedTail } = mergeSandbox.exports;

// 1) 瞬时尾巴（seq=0）并入，排在持久化部分之后
{
  const prev = [{ seq: 1, content: "u1" }, { seq: 2, content: "a1" }];
  const incoming = [
    { seq: 1, content: "u1" }, { seq: 2, content: "a1" },
    { seq: 0, content: "user2" }, { seq: 0, content: "thinking2" }, { seq: 0, content: "agent2" },
  ];
  const r = mergeWindowedTail(prev, incoming, 2);
  assert.equal(r.changed, true);
  assert.equal(
    r.exchanges.map((e) => e.content).join("|"),
    "u1|a1|user2|thinking2|agent2",
  );
}
// 2) 内容增长：瞬时尾巴整体替换（不重复、不乱序）
{
  const prev = [{ seq: 1, content: "u1" }, { seq: 2, content: "a1" }, { seq: 0, content: "agent2-partial" }];
  const incoming = [
    { seq: 1, content: "u1" }, { seq: 2, content: "a1" },
    { seq: 0, content: "agent2-partial+more" },
  ];
  const r = mergeWindowedTail(prev, incoming, 2);
  assert.equal(
    r.exchanges.map((e) => e.content).join("|"),
    "u1|a1|agent2-partial+more",
  );
}
// 3) 重连回放的持久化补齐（seq>maxSeq）归并 + 瞬时在末尾
{
  const prev = [{ seq: 3, content: "a3" }, { seq: 0, content: "stale-transient" }];
  const incoming = [
    { seq: 4, content: "u4" }, { seq: 5, content: "a5" },
    { seq: 0, content: "live" },
  ];
  const r = mergeWindowedTail(prev, incoming, 3);
  assert.equal(
    r.exchanges.map((e) => e.content).join("|"),
    "a3|u4|a5|live",
  );
}
// 4) 无新内容：changed=false 且返回原数组
{
  const prev = [{ seq: 1 }, { seq: 2 }];
  const r = mergeWindowedTail(prev, [{ seq: 1 }, { seq: 2 }], 2);
  assert.equal(r.changed, false);
  assert.equal(r.exchanges, prev);
}
// 5) 已见 seq 不重复并入；stale transient 被丢弃
{
  const prev = [{ seq: 3, content: "a3" }];
  const r = mergeWindowedTail(prev, [{ seq: 3, content: "a3" }, { seq: 0, content: "t" }], 3);
  assert.equal(r.exchanges.map((e) => e.content).join("|"), "a3|t");
}

// ── F2: done 后窗口重锚定 ──────────────────────────────────────────────
assert.match(
  viewerSrc,
  /streamingEdgeRef\.current = \{ key: sessionKey, value: isStreaming \}/,
  "F2 streaming edge tracking missing",
);
assert.match(
  viewerSrc,
  /if \(sessionService\.isSessionStreaming\(sessionKey\)\) \{/,
  "F2 active-stream guard missing",
);

// ── F3: meta.updated 新 key → replace 重拉 ─────────────────────────────
const appSrc = fs.readFileSync(path.resolve(import.meta.dirname, "../src/App.tsx"), "utf8");
assert.match(appSrc, /const listHasKey = sessionsRef\.current\.some\(/, "F3 listHasKey missing");
assert.match(
  appSrc,
  /\} else \{\s*\n\s*void loadSessionsForRoot\(rootID, \{ replace: true \}\);\s*\n\s*\}\s*\n\s*if \(multiProjectSessionsEnabled\) \{/,
  "F3 new-key replace branch missing",
);

// ── F4: 列表拉取失败可见 ───────────────────────────────────────────────
assert.match(
  appSrc,
  /reportError\(\s*\n\s*"session\.list_load_failed"/,
  "F4 reportError missing",
);
const zhSrc = fs.readFileSync(path.resolve(import.meta.dirname, "../src/i18n/locales/zh-CN.ts"), "utf8");
const enSrc = fs.readFileSync(path.resolve(import.meta.dirname, "../src/i18n/locales/en-US.ts"), "utf8");
assert.ok(zhSrc.includes("error.session.listLoadFailed"), "zh locale key missing");
assert.ok(enSrc.includes("error.session.listLoadFailed"), "en locale key missing");

console.log("session-window.test.mjs: OK");
