import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import ts from "typescript";
import vm from "node:vm";

const sourcePath = path.resolve(import.meta.dirname, "../src/services/sessionListMerge.ts");
const source = fs.readFileSync(sourcePath, "utf8");
const compiled = ts.transpileModule(source, {
  compilerOptions: {
    module: ts.ModuleKind.CommonJS,
    target: ts.ScriptTarget.ES2020,
  },
}).outputText;

const sandbox = {
  exports: {},
  module: { exports: {} },
};
vm.runInNewContext(compiled, sandbox, { filename: sourcePath });

const {
  applyPinnedSnapshotToSessions,
  mergeSessionItems,
  orderProjectGroupsByNode,
  projectActivityTime,
  sessionActivityAt,
} = sandbox.exports;

const merged = mergeSessionItems(
  [
    {
      key: "old",
      session_key: "old",
      root_id: "root",
      updated_at: "2026-07-30T10:00:00.000Z",
    },
    {
      key: "new",
      session_key: "new",
      root_id: "root",
      updated_at: "2026-07-30T12:00:00.000Z",
    },
  ],
  [
    {
      key: "pin-a",
      session_key: "pin-a",
      root_id: "root",
      updated_at: "2026-07-30T09:00:00.000Z",
      pinned_at: "2026-07-30T12:30:00.000Z",
    },
    {
      key: "pin-b",
      session_key: "pin-b",
      root_id: "root",
      updated_at: "2026-07-30T11:00:00.000Z",
      pinned_at: "2026-07-30T12:45:00.000Z",
    },
  ],
);

assert.equal(JSON.stringify(merged.map((item) => item.key)), JSON.stringify(["pin-b", "pin-a", "new", "old"]));

const unpinned = applyPinnedSnapshotToSessions(merged, "root", ["pin-a"]);

assert.equal(unpinned.find((item) => item.key === "pin-b")?.pinned_at, undefined);
assert.equal(JSON.stringify(unpinned.map((item) => item.key)), JSON.stringify(["pin-a", "new", "pin-b", "old"]));

// A live activity overlay must beat persisted timestamps inside both pin groups,
// while pins only divide the groups and do not freeze their internal order.
const activeOrder = mergeSessionItems(
  [
    { key: "pinned-old", updated_at: "2026-07-30T10:00:00.000Z", pinned_at: "2026-07-30T13:00:00.000Z" },
    { key: "pinned-live", updated_at: "2026-07-30T09:00:00.000Z", activity_at: "2026-07-30T14:00:00.000Z", pinned_at: "2026-07-30T12:00:00.000Z" },
    { key: "plain-old", updated_at: "2026-07-30T11:00:00.000Z" },
    { key: "plain-live", updated_at: "2026-07-30T08:00:00.000Z", activity_at: "2026-07-30T15:00:00.000Z" },
  ],
  [],
);
assert.equal(
  JSON.stringify(activeOrder.map((item) => item.key)),
  JSON.stringify(["pinned-live", "pinned-old", "plain-live", "plain-old"]),
);
assert.equal(sessionActivityAt(activeOrder[0]), "2026-07-30T14:00:00.000Z");

// A stale list refresh cannot move a live session backwards. Once persisted
// updated_at catches up, the temporary activity field is discarded.
const refreshedLive = mergeSessionItems(activeOrder, [
  { key: "plain-live", updated_at: "2026-07-30T08:00:00.000Z" },
]).find((item) => item.key === "plain-live");
assert.ok(refreshedLive);
assert.equal(refreshedLive.activity_at, "2026-07-30T15:00:00.000Z");
const persistedLive = mergeSessionItems([refreshedLive], [
  { key: "plain-live", updated_at: "2026-07-30T15:00:00.000Z" },
])[0];
assert.equal(persistedLive.activity_at, undefined);

assert.equal(
  projectActivityTime({
    latestSessionTime: "2026-07-30T10:00:00.000Z",
    activity_at: "2026-07-30T12:00:00.000Z",
    sessions: [
      { updated_at: "2026-07-30T13:00:00.000Z" },
      { updated_at: "2026-07-30T11:00:00.000Z", activity_at: "2026-07-30T14:00:00.000Z" },
    ],
  }),
  Date.parse("2026-07-30T14:00:00.000Z"),
  "project activity must include the freshest live child-session activity",
);

const sessionListSource = fs.readFileSync(
  path.resolve(import.meta.dirname, "../src/components/session/SessionList.tsx"),
  "utf8",
);
assert.match(
  sessionListSource,
  /orderProjectGroupsByNode\(/,
  "SessionList must use the tested node-first project ordering",
);

const projectGroups = [
  { rootId: "home-plain-old", _nodeId: "home", latestSessionTime: "2026-07-30T05:00:00.000Z", sessions: [] },
  { rootId: "pc-newest", _nodeId: "pc", _pinned: true, latestSessionTime: "2026-07-30T22:00:00.000Z", sessions: [] },
  { rootId: "home-pin-old", _nodeId: "home", _pinned: true, latestSessionTime: "2026-07-30T10:00:00.000Z", sessions: [] },
  { rootId: "fn-newest", _nodeId: "fn", latestSessionTime: "2026-07-30T23:00:00.000Z", sessions: [] },
  { rootId: "home-plain-new", _nodeId: "home", latestSessionTime: "2026-07-30T15:00:00.000Z", sessions: [] },
  { rootId: "pc-plain", _nodeId: "pc", latestSessionTime: "2026-07-30T21:00:00.000Z", sessions: [] },
  { rootId: "home-pin-live", _nodeId: "home", _pinned: true, latestSessionTime: "2026-07-30T09:00:00.000Z", activity_at: "2026-07-30T12:00:00.000Z", sessions: [] },
];
const orderedProjects = orderProjectGroupsByNode(
  projectGroups,
  ["home", "pc", "fn"],
  (group) => group._nodeId,
  (group) => !!group._pinned,
  (left, right) => left.rootId.localeCompare(right.rootId),
);
assert.equal(
  JSON.stringify(orderedProjects.map((group) => group.rootId)),
  JSON.stringify(["home-pin-live", "home-pin-old", "home-plain-new", "home-plain-old", "pc-newest", "pc-plain", "fn-newest"]),
  "node order is the outer partition; pin and latest activity only sort projects inside each node",
);

// A missing node id must not let an unknown group sort across known node buckets.
const withUnknownNode = orderProjectGroupsByNode(
  [
    { rootId: "pc", _nodeId: "pc", latestSessionTime: "2026-07-30T10:00:00.000Z", sessions: [] },
    { rootId: "unknown", _nodeId: "new-node", latestSessionTime: "2026-07-30T23:00:00.000Z", sessions: [] },
    { rootId: "home", _nodeId: "home", latestSessionTime: "2026-07-30T01:00:00.000Z", sessions: [] },
  ],
  ["home", "pc"],
  (group) => group._nodeId,
  () => false,
  (left, right) => left.rootId.localeCompare(right.rootId),
);
assert.equal(
  JSON.stringify(withUnknownNode.map((group) => group.rootId)),
  JSON.stringify(["home", "pc", "unknown"]),
  "unknown nodes append stably after configured nodes instead of competing by recency",
);

const realtimeSource = fs.readFileSync(
  path.resolve(import.meta.dirname, "../src/app/useRealtimeEvents.ts"),
  "utf8",
);
const streamHandler = realtimeSource.slice(
  realtimeSource.indexOf("const handleSessionStream = (payload: any) =>"),
  realtimeSource.indexOf("const markStreamPending =", realtimeSource.indexOf("const handleSessionStream = (payload: any) =>")),
);
assert.match(
  streamHandler,
  /event\?\.type && event\.type !== "message_done"[\s\S]*?activity_at: activityAt/,
  "stream events must advance sidebar activity before a completed reply persists updated_at",
);
assert.match(
  streamHandler,
  /const activityTime = Math\.floor\(eventTime \/ 100\) \* 100/,
  "activity updates must be throttled to 100 ms while remaining responsive per stream block",
);
assert.ok(
  streamHandler.indexOf("activity_at: activityAt") < streamHandler.indexOf("if (wsNid && curNid && wsNid !== curNid)"),
  "a streamed project must receive its activity update before filtering content to the active node",
);

// 跨节点同 key 会话不得互相覆盖（C3：归并键需按 _nodeId 作用域）
const crossNode = mergeSessionItems(
  [{ key: "s1", session_key: "s1", root_id: "proj", _nodeId: "local", updated_at: "2026-07-30T10:00:00.000Z" }],
  [{ key: "s1", session_key: "s1", root_id: "proj", _nodeId: "pc", updated_at: "2026-07-30T11:00:00.000Z" }],
);
assert.equal(crossNode.length, 2, "local/pc 同 key 会话应各自保留，不得互相覆盖");
assert.ok(crossNode.some((item) => item._nodeId === "local"));
assert.ok(crossNode.some((item) => item._nodeId === "pc"));

// 展开子会话会把同一批子会话再拉一遍。归并后列表长度必须不变 —— 一旦变大，
// 说明同一会话并存了两条（重复的 React key），收起时就会留下一堆孤儿 DOM
// （实测展开 131 行只收到 55 行）。这条守的是「子会话节点归属必须打标」这条契约：
// 调用方漏传 _nodeId 时子会话会归到 `::key`，与既有条目的 `local::key` 并存。
const batch = Array.from({ length: 50 }, (_, i) => ({
  key: `child-${i}`,
  session_key: `child-${i}`,
  root_id: "proj",
  _nodeId: "local",
  updated_at: "2026-09-29T10:00:00.000Z",
}));
const existing = [
  { key: "p", session_key: "p", root_id: "proj", _nodeId: "local", updated_at: "2026-09-29T09:00:00.000Z" },
  ...batch,
];
const afterExpand = mergeSessionItems(existing, batch);
assert.equal(afterExpand.length, 51, "同节点同 key 重复拉回不得让列表变长");

// 反面：漏打 _nodeId 时子会话确实会翻倍（这正是 App.tsx loadChildSessionsForParent
// 踩过的坑）。锁住这个行为，任何人再把节点打标删掉，测试立刻红。
const unstamped = batch.map(({ _nodeId, ...rest }) => rest);
const duplicated = mergeSessionItems(existing, unstamped);
assert.equal(
  duplicated.length,
  101,
  "未打 _nodeId 的子会话会与既有条目并存成重复 —— 调用方必须补节点归属",
);

// 主列表的每一处拉取都必须打 _nodeId。漏任何一处，它并进 sessions 时就会与
// 已打标的条目并存成重复的 React key（子会话那条就是这么坏的：展开 131 行
// 只收到 55 行）。这里逐个点名守。
const appSource2 = fs.readFileSync(
  path.resolve(import.meta.dirname, "../src/App.tsx"),
  "utf8",
);
for (const [what, needle] of [
  ["子会话（loadChildSessionsForParent）", "toSessionItem(rootID, { ...(item as any), _nodeId: childNodeId })"],
  ["外部导入后刷新（refreshSessionsAfterExternalImport）", "_nodeId: (item as any)._nodeId || listNodeId"],
]) {
  assert.ok(
    appSource2.includes(needle),
    `${what} 拉到的会话必须打 _nodeId，否则与已打标条目并存成重复 key`,
  );
}
assert.ok(
  !/\.map\(\(item\) => toSessionItem\(rootID, item\)\)/.test(appSource2),
  "不应再有裸 toSessionItem(rootID, item) —— 那是不打 _nodeId 的写法",
);

// 直接守住 App.tsx 里那处打标：子会话从 HTTP 拉回，响应体不带 _nodeId，
// 漏了就会在分组里并存出重复 key（收起时收不掉）。这里读源码断言它还在。
const appSource = fs.readFileSync(
  path.resolve(import.meta.dirname, "../src/App.tsx"),
  "utf8",
);
const childLoader = appSource.slice(
  appSource.indexOf("const loadChildSessionsForParent"),
  appSource.indexOf("const loadChildSessionsForParent") + 2400,
);
assert.ok(
  /toSessionItem\(rootID, \{ \.\.\.\(item as any\), _nodeId: childNodeId \}\)/.test(childLoader),
  "loadChildSessionsForParent 必须给子会话打 _nodeId，否则同一会话并存成重复 key、收起时留下孤儿 DOM",
);
