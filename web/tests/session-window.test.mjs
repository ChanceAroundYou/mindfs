import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { test } from "vitest";

test("session-window", async () => {
// overlay 判定由 src/shared/sessionOverlay.ts 的纯函数实现。
// 这里直接 import 真实现，避免再维护同构副本。

const { computeTailOverlay } = await import("../src/shared/sessionOverlay.ts");

const sessionServiceDir = path.resolve(import.meta.dirname, "../src/services/session");
const sharedSessionTypesPath = path.resolve(import.meta.dirname, "../src/shared/sessionTypes.ts");
const sessionViewerPath = path.resolve(import.meta.dirname, "../src/components/session/SessionViewer.tsx");
const sessionViewerProjectionPath = path.resolve(import.meta.dirname, "../src/components/session/useSessionViewerExchangeProjection.ts");
const sessionWindowingPath = path.resolve(import.meta.dirname, "../src/components/session/useSessionWindowing.ts");
const targetSeqScrollPath = path.resolve(import.meta.dirname, "../src/components/session/useTargetSeqScroll.ts");
const viewerControlsPath = path.resolve(import.meta.dirname, "../src/components/session/SessionViewerFloatingControls.tsx");
const managerPath = path.resolve(import.meta.dirname, "../../server/internal/session/manager.go");
const managerWindowPath = path.resolve(import.meta.dirname, "../../server/internal/session/manager_window_reads.go");
const httpPath = path.resolve(import.meta.dirname, "../../server/internal/api/http.go");
const httpSessionsPath = path.resolve(import.meta.dirname, "../../server/internal/api/http_sessions.go");

const sessionSrc = [
  ...fs.readdirSync(sessionServiceDir)
    .filter((name) => name.endsWith(".ts"))
    .map((name) => fs.readFileSync(path.join(sessionServiceDir, name), "utf8")),
  fs.readFileSync(sharedSessionTypesPath, "utf8"),
].join("\n");
const viewerSrc = [sessionViewerPath, sessionViewerProjectionPath, sessionWindowingPath, targetSeqScrollPath, viewerControlsPath].map((file) => fs.readFileSync(file, "utf8")).join("\n");
// 剥掉注释后的代码视图：注释里提到某个坏写法是正常的（那通常正是在解释它为什么被删）。
const stripComments = (src) => src.replace(/\/\*[\s\S]*?\*\/|\/\/.*$/gm, "");
const sessionCode = stripComments(sessionSrc);
const viewerCode = stripComments(viewerSrc);

// 淘汰探针：喂 exchanges 进去，拿保留的 exchanges 出来。
function buildEvictFrom(toPersistentSession) {
  return (exchanges) =>
    toPersistentSession({ key: "probe", exchanges, exchange_aux: {} }).exchanges;
}
const managerSrc = fs.readFileSync(managerPath, "utf8");
const managerWindowSrc = fs.readFileSync(managerWindowPath, "utf8");
const httpSrc = fs.readFileSync(httpPath, "utf8");
const httpSessionsSrc = fs.readFileSync(httpSessionsPath, "utf8");
const httpSessionContract = `${httpSrc}\n${httpSessionsSrc}`;

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
const appDir = path.resolve(import.meta.dirname, "../src/app");
const appSrc = fs.readdirSync(appDir).filter((name) => /\.tsx?$/.test(name)).map((name) => fs.readFileSync(path.join(appDir, name), "utf8")).join("\n");
// 2026-09 App.tsx 拆分：WS 事件处理器整块搬到 app/useRealtimeEvents.ts，契约随文件走。
// done 后的重锚定（F2）与 meta.updated 新 key 的 replace 重拉（F3）都在这批 handler 里，
// 故这几条断言改读新文件；仍在 App 里的（_windowMeta 附加、锚点计数、reportError 冷却、
// SESSION_WINDOW_SIZE）继续读 appSrc。
const realtimeSrc = ["useRealtimeEvents.ts", "realtimeWsHandlers.ts", "realtimeWsSessionMeta.ts"].map((name) => fs.readFileSync(path.resolve(import.meta.dirname, "../src/app", name), "utf8")).join("\n");
assert.ok(
  !fs.existsSync(path.resolve(import.meta.dirname, "../src/services/sessionWindowMerge.ts")),
  "sessionWindowMerge.ts must stay deleted",
);
assert.doesNotMatch(viewerSrc, /mergeWindowedTail/, "seq merge model must not return");
// overlay 尾巴派生 + 组合输入
// 判定已搬到纯模块：组件只负责接线，判定本身在 sessionOverlay.ts
const overlaySrc = fs.readFileSync(
  path.resolve(import.meta.dirname, "../src/shared/sessionOverlay.ts"),
  "utf8",
);
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

// ── 切会话闪一下（2026-10-05 发现，2026-10-06 定根因）────────────────────
// 症状：切回**运行中**的会话时，先从缓存渲染出完整对话 → 塌到最后一条用户 prompt
// → 再一点点把 assistant 补回来（assistant 越多越明显）。已结束的会话怎么切都不闪。
//
// 根因：加载时要知道「本地在途内容（seq=0）不能丢」，而服务端**从不**下发它们。
// 这条规则曾被写了四遍，其中三遍都去服务端回包找瞬时行 —— 恒取空数组，于是每次
// 加载都把在途内容丢一次。现在收敛成 composeLoadedExchanges 一个定义。
//
// 行为断言在上面的 composeLoadedExchanges 那组；这里只钉「两个分支都走同一条规则」
// 以及「旧写法已彻底消失」。
assert.equal(
  (stripComments(appSrc).match(/composeLoadedExchanges\(/g) || []).length,
  2,
  "restoreActiveSession 的窗口分支与 fallback 分支都必须走同一个组装规则",
);
// 反向断言：旧写法必须彻底消失。断言形状在这里是恰当的 —— 我们禁的是一个**写法**，
// 而它造成的可见缺陷已由上面的行为断言守住。
assert.doesNotMatch(
  appSrc,
  /hasPendingTurn/,
  "hasPendingTurn 恒为 false（服务端回包不带 seq=0）—— 这个守卫必须删掉，不能再有第二份实现",
);
assert.doesNotMatch(
  appSrc,
  /transientTail\s*=\s*winExs\.filter/,
  "瞬时行不能取自服务端窗口回包 —— 那里一个 seq=0 都没有",
);
// aux 必须一并并入：exchanges 保住历史而 exchange_aux 只剩窗口那份时，历史行上的
// 工具卡会凭空消失（按 seq 覆盖，不是拼接 —— 拼接会把同一张卡渲染两遍）。
assert.match(
  appSrc,
  /exchange_aux: \{\s*\n\s*\.\.\.\(\(cachedBeforeSync as any\)\?\.exchange_aux \|\| \{\}\),\s*\n\s*\.\.\.\(sess\.exchange_aux \|\| \{\}\),\s*\n\s*\},/,
  "exchange_aux must merge per-seq too, or restored history loses its tool cards",
);
assert.match(
  sessionSrc,
  /exchanges: mergeSessionExchanges\(baseExchanges, incomingExchanges\),/,
  "appendSessionDelta must use the seq-deduping merge, not concatenation",
);

// 合并函数本体**直接跑**：三条可见集来源（缓存 / 服务端窗口 / loadMore 页）互相
// 重叠，纯拼接会把同一行渲染两遍（实测 2026-10-05：切会话后历史里每条消息出现两次）。
// 这条一旦坏了症状是「内容重复」，源码正则看不出来 —— 所以断言行为，不断言形状。
//
// task-35 拆分后 `mergeSessionExchanges` 住在 services/sessionExchangeMerge.ts
// （session.ts 只是 re-export），所以走 Vitest 的动态 import 而不是 vm 导出 ——
// 与文件顶部 `computeTailOverlay` 同一个先例：真实实现，不同构副本。
const sessionMod = await import("../src/services/sessionExchangeMerge.ts");
const mergeSessionExchanges = sessionMod.mergeSessionExchanges;
assert.equal(typeof mergeSessionExchanges, "function", "mergeSessionExchanges must be exported");

// 重叠：缓存 1..5，窗口 4..8 → 1..8 各一行，不重不漏
const overlapped = mergeSessionExchanges(
  [1, 2, 3, 4, 5].map((seq) => ({ seq, content: "old" })),
  [4, 5, 6, 7, 8].map((seq) => ({ seq, content: "new" })),
);
// 注意用 JSON 比而不是 deepEqual：vm 里造出来的对象原型来自另一个 realm，
// deepEqual 的原型检查会因此判它们「结构相同但不相等」。
assert.equal(
  JSON.stringify(overlapped.map((ex) => ex.seq)),
  JSON.stringify([1, 2, 3, 4, 5, 6, 7, 8]),
  "overlapping sources must merge into one row per seq",
);
assert.equal(
  overlapped.filter((ex) => ex.seq === 4).length,
  1,
  "a seq present in both sources must render once, not twice",
);
// 同 seq 冲突：后到者赢（窗口那份更新）
assert.equal(overlapped.find((ex) => ex.seq === 4).content, "new");

// seq=0 的瞬时行必须被丢掉 —— 它们归 overlay 管，混进窗口会与 overlay 重复渲染
assert.equal(
  JSON.stringify(
    mergeSessionExchanges([{ seq: 0, content: "live" }, { seq: 1, content: "a" }], []).map(
      (ex) => ex.seq,
    ),
  ),
  JSON.stringify([1]),
  "seq=0 transient rows must be dropped (overlay owns them)",
);

// 空/垃圾输入不抛
assert.equal(mergeSessionExchanges(null, null).length, 0);
assert.equal(mergeSessionExchanges(undefined, [{ seq: 3 }]).length, 1);

// 乱序输入必须按 seq 升序返回（调用方不自己维护顺序）
assert.equal(
  JSON.stringify(mergeSessionExchanges([], [{ seq: 9 }, { seq: 2 }, { seq: 5 }]).map((e) => e.seq)),
  JSON.stringify([2, 5, 9]),
  "the merge must sort by seq — callers rely on order",
);

// ── composeLoadedExchanges：加载时的唯一组装规则 ───────────────────────────
// 这条规则曾经被写了 4 遍（restoreActiveSession 的窗口/fallback 两处 + 各自的
// resumeCursor 块 + handleSyncSession），其中 3 遍都去**服务端回包**找瞬时行 ——
// 服务端窗口回包只含已落盘行，于是每次加载都把在途内容丢一次。
// 实测 2026-10-06：切回运行中的会话，缓存 376 条（其中 361 条 seq=0），
// 加载后缓存被写成 15 条，界面塌到最后一条用户 prompt，再等直播流补回来（4~6 秒）。
// 断言行为，因为「丢了多少」源码正则看不出来。
const composeLoadedExchanges = sessionMod.composeLoadedExchanges;
assert.equal(
  typeof composeLoadedExchanges,
  "function",
  "composeLoadedExchanges must be exported — it is the single load-time rule",
);

// ① 只有服务端行（冷缓存）：原样返回（在途与否都一样）
for (const inFlight of [true, false]) {
  assert.equal(
    JSON.stringify(
      composeLoadedExchanges([1, 2, 3].map((seq) => ({ seq })), [], inFlight).map((e) => e.seq),
    ),
    JSON.stringify([1, 2, 3]),
    `cold cache must return the server rows (inFlight=${inFlight})`,
  );
}

// ② 在途（会话正在跑）：服务端只回已落盘行，缓存里的 seq=0 必须**全部接回来**
//    （这是「切回运行中会话塌陷」的根因断言 —— 少接一条就是一次可见的塌陷）
{
  const server = [1, 2, 3].map((seq) => ({ seq }));
  const cached = [1, 2, 3].map((seq) => ({ seq })).concat([
    { seq: 0, role: "user", content: "刚发出还没落库" },
    { seq: 0, role: "agent", content: "正在流式输出的正文" },
  ]);
  const out = composeLoadedExchanges(server, cached, true);
  assert.equal(out.length, 5, "the in-flight rows must survive the load (root cause)");
  assert.equal(out.filter((e) => e.seq === 0).length, 2, "both transient rows kept");
  assert.equal(
    JSON.stringify(out.filter((e) => e.seq > 0).map((e) => e.seq)),
    JSON.stringify([1, 2, 3]),
    "persisted rows stay sorted and deduped",
  );
}

// ③ **不在途（会话已结束 / 刚 compact 过）：seq=0 必须全部丢掉**
//    这是另一半 —— 服务端已经把那一轮落盘、或 compact 把历史整个重置了，
//    本地那份 seq=0 从此再也对不上。不清就会跨多次 compact 无声堆积
//    （实测一个会话堆到 1100+ 条：tool=700、thought=360），
//    一旦被渲染出来就是「同一段正文出现两遍、而且每切一次越多」。
{
  const server = [1, 2, 3].map((seq) => ({ seq }));
  const cached = [1, 2, 3].map((seq) => ({ seq })).concat([
    { seq: 0, role: "agent", content: "上一轮的残留" },
    { seq: 0, role: "tool", content: "上一轮的残留工具卡" },
  ]);
  const out = composeLoadedExchanges(server, cached, false);
  assert.equal(out.length, 3, "no in-flight turn ⇒ every seq=0 row is stale residue");
  assert.equal(out.filter((e) => e.seq === 0).length, 0, "stale transients must be dropped");
  assert.equal(
    JSON.stringify(out.map((e) => e.seq)),
    JSON.stringify([1, 2, 3]),
    "only the persisted rows survive",
  );
}

// ④ 缓存更全（用户翻过历史）：缓存里比窗口老的持久行不能丢
{
  const out = composeLoadedExchanges(
    [{ seq: 8 }, { seq: 9 }],
    [1, 8, 9].map((seq) => ({ seq })).concat([{ seq: 0, content: "x" }]),
    true,
  );
  assert.equal(
    JSON.stringify(out.filter((e) => e.seq > 0).map((e) => e.seq)),
    JSON.stringify([1, 8, 9]),
    "older persisted rows from the cache must be kept (load is monotonic)",
  );
  assert.equal(out.filter((e) => e.seq === 0).length, 1);
}

// ⑤ 同一个瞬时对象两侧都有 → 只留一份（按对象同一性）
{
  const shared = { seq: 0, content: "同一条" };
  const out = composeLoadedExchanges([{ seq: 1 }, shared], [{ seq: 1 }, shared], true);
  assert.equal(out.length, 2, "the same transient object must not be duplicated");
}

// ⑥【2026-10-06 反转】在途 + 缓存里跨过 compact → **一条都不许丢**
//    旧行为是「只保留最后一次 compact 之后的瞬时行」，依据是「compact 是服务端历史的重置点」。
//    核对服务端后该依据为假：交换 JSONL 是 append-only（唯一 `os.Remove` 在两处
//    `DeleteSession`），compact 只写一条 `CompactNotice` aux。而 compact 是**轮内**事件，
//    按它切会丢掉本轮 compact 之前那些还没落盘的内容（服务端没有替代品）。
//    陈旧瞬时行由另外两处负责：一轮结束 / reset 的 `dropTransientExchanges`，
//    以及加载时 `inFlight=false`。见 docs/message-mechanisms.md 冲突⑨、tests 的【红·⑨b/⑨c】。
{
  const cached = [
    { seq: 1 },
    { seq: 0, role: "agent", content: "compact 之前的旧内容" },
    { seq: 0, role: "tool", content: "compact 之前的旧工具卡" },
    { seq: 0, role: "compact", content: "" },
    { seq: 0, role: "agent", content: "compact 之后的新内容" },
  ];
  const out = composeLoadedExchanges([{ seq: 1 }], cached, true);
  const transients = out.filter((e) => e.seq === 0);
  assert.equal(
    transients.length,
    4,
    "compact 不是服务端历史的重置点 ⇒ 在途时不能按它切掉瞬时行",
  );
  assert.equal(
    JSON.stringify(transients.map((e) => String(e.content || ""))),
    JSON.stringify(["compact 之前的旧内容", "compact 之前的旧工具卡", "", "compact 之后的新内容"]),
    "瞬时行必须保持缓存顺序（数组顺序即时间序）",
  );
}

// ⑦ 没有 compact 行时不受影响（全部保留 —— 不能把正常情况也砍了）
{
  const cached = [
    { seq: 0, role: "agent", content: "a" },
    { seq: 0, role: "tool", content: "b" },
  ];
  const out = composeLoadedExchanges([{ seq: 1 }], cached, true);
  assert.equal(out.filter((e) => e.seq === 0).length, 2, "没有 compact 边界就不该砍任何东西");
}

// ⑧ 空/垃圾输入不抛
assert.equal(composeLoadedExchanges(null, null, true).length, 0);
assert.equal(composeLoadedExchanges(undefined, undefined, false).length, 0);

// ── 正文拼接不再需要重放适配层（mergeStreamedText 已删除）──────────────────
// 曾被 mergeStreamedText 挡住的形态：服务端把在途回合的内容重推一遍，客户端
// 无条件拼接 → 同一段正文在**行内**变成 `aabb`（「每切一次越多」）。
//
// 根因不在拼接，在**协议**：上游那版是「带 event_cursor 续流」，靠客户端缓存与
// 服务端游标严格同步来保证「每片只投一次」。这条不变量维持不住（游标恒为空，
// 实际每次切会话都是全量重发），于是才需要在拼接处用子串包含去猜「这片是不是重发」。
//
// 现在协议改成**快照重建**：会话挂载一律先清瞬时尾巴（dropTransientExchanges），
// 再照单应用整批事件。重放不再是「增量」，因此拼接就是拼接 —— 无条件的
// `String(last.content) + String(content)` 即正确。
//
// 这条钉子守的是「别把适配层加回来」：一旦有人重新引入按内容猜重发的逻辑，
// 说明协议又退化成了增量续流，那时真正该修的是协议，不是拼接。
assert.equal(sessionMod.mergeStreamedText, undefined, "mergeStreamedText 是重投递适配层，随游标协议一起删除，不得回潮");

// ── dropTransientExchanges：快照重建前的清理出口 ──────────────────────────
// 判据是共用的 isTransientExchange：**seq 为空即瞬时**（2026-10-06 起用户行不再例外）。
// 曾经 user 行例外（怕刚发出去的消息闪一下），但那条例外让「同一条行该不该留」有两把尺
// （`mergeSessionExchanges` 只收 seq>0），去留取决于先跑哪条路径 —— 冲突①。
// 现在两边都丢：丢的那一刻它已有替代品（用户行在回合开始就落盘；reset 在窗口装好之后），
// 而保留它的代价是认领漏掉时与服务端持久行同时渲染。回归护栏：E2E 的
// 「用户消息在时间线里恰好出现 1 次」。
{
  const drop = sessionMod.dropTransientExchanges;
  assert.equal(typeof drop, "function", "dropTransientExchanges must be exported");
  assert.equal(typeof sessionMod.isTransientExchange, "function", "isTransientExchange 是唯一判据，必须导出");
  // ① seq=0 的 assistant/tool/thought 全清，seq>0 的持久行全留
  const session = {
    key: "s",
    exchanges: [
      { seq: 1, role: "user" },
      { seq: 0, role: "agent" },
      { seq: 2, role: "agent" },
      { seq: 0, role: "tool" },
    ],
  };
  const dropped = drop(session);
  assert.equal(
    JSON.stringify(dropped.exchanges.map((e) => e.seq)),
    JSON.stringify([1, 2]),
    "dropTransientExchanges must remove every seq=0 non-user row",
  );
  // ② seq=0 的乐观 user 行**也一起清**（与 mergeSessionExchanges 同判；见上面那段）
  {
    const withEcho = drop({ key: "s", exchanges: [{ seq: 0, role: "user" }, { seq: 0, role: "agent" }] });
    assert.equal(withEcho.exchanges.length, 0, "seq=0 的用户行不再是例外");
    assert.equal(drop({ key: "s", exchanges: [{ seq: 0, role: "user" }] }).exchanges.length, 0);
    // ③ 谓词本身：大小写无关；缺字段、非数字 seq 都归到「瞬时」
    assert.equal(sessionMod.isTransientExchange({ seq: 0, role: "user" }), true);
    assert.equal(sessionMod.isTransientExchange({ seq: 0, role: "User" }), true);
  }
  assert.equal(sessionMod.isTransientExchange({ seq: 3, role: "agent" }), false);
  assert.equal(sessionMod.isTransientExchange({ role: "agent" }), true, "缺 seq 视为瞬时");
  assert.equal(sessionMod.isTransientExchange({ seq: "x", role: "agent" }), true, "非数字 seq 视为瞬时");
  assert.equal(sessionMod.isTransientExchange(null), true);
  // ④ 没有瞬时行时原样返回（同一引用，避免无意义的重渲染）
  const clean = { key: "s", exchanges: [{ seq: 1 }] };
  assert.equal(drop(clean), clean, "no-op must return the same reference");
  assert.equal(drop(null), null);
}

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
const zhSrc = fs.readFileSync(path.resolve(import.meta.dirname, "../src/i18n/locales/zh-CN.ts"), "utf8");
const enSrc = fs.readFileSync(path.resolve(import.meta.dirname, "../src/i18n/locales/en-US.ts"), "utf8");
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

console.log("session-window.test.mjs: OK");
});
