import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const session = readFileSync(new URL("../src/services/session.ts", import.meta.url), "utf8");
const persistBody = session.slice(
  session.indexOf("function toPersistentSession("),
  session.indexOf("function stripAnchorBookkeeping("),
);
const stripBody = session.slice(
  session.indexOf("function stripAnchorBookkeeping("),
  session.indexOf("export function isWindowedView("),
);

assert.ok(persistBody.length > 0, "toPersistentSession should be found in session.ts");
assert.ok(stripBody.length > 0, "stripAnchorBookkeeping should be found in session.ts");

// _windowMeta/_anchoredAt 是 SessionViewer 的重锚定簿记，只对「当前会话实例」有意义。
// 一旦随 IndexedDB 落盘，冷启动时 lastAppliedAnchorRef 从 {key:null, at:-1} 开始，
// 陈旧的 _anchoredAt 会通过守卫被当成新锚点应用，而 applyWindow 是把 session.exchanges
// 原样当窗口渲染的 —— 于是持久化下来的旧 exchange 集被当成当前窗口显示，
// 表现为刷新后旧内容出现在当前位置（2026-09-12 症状 3）。
assert.match(
  persistBody,
  /const persistent = stripAnchorBookkeeping\(session\)/,
  "persisting a session should first strip the viewer's anchor bookkeeping",
);
assert.doesNotMatch(
  persistBody,
  /\.\.\.session/,
  "toPersistentSession must spread the stripped object, not the raw session",
);
assert.match(
  stripBody,
  /\{\s*_windowMeta,\s*_anchoredAt,\s*\.\.\.rest\s*\}/,
  "the two anchor bookkeeping fields should be dropped via rest destructuring",
);
assert.match(
  stripBody,
  /return rest as Session/,
  "the stripped object should be returned",
);
assert.match(
  stripBody,
  /return session;/,
  "when neither field is present the session should pass through unchanged",
);

// 只剥这两个字段：_nodeId 仍要保留，SessionViewer 取窗口时用它做节点路由。
// 用「一刀切去掉所有 _ 前缀字段」的写法会连带删掉 _nodeId，属于回归。
assert.doesNotMatch(
  stripBody,
  /startsWith\("_"\)/,
  "stripping must not blanket-remove every underscore-prefixed field (_nodeId is still needed)",
);
