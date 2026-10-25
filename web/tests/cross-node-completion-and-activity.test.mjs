import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import ts from "typescript";
import vm from "node:vm";
import { test } from "vitest";

// 跨节点完成信号 + 全量重拉的时间继承（G-KA 二轮，2026-10-25）。
//
// 用户报的两个现象都出在同一条链上：
//   ① 「跨节点的完成没有徽标也没有提示音」—— 浏览器只连激活节点那一条 WS，远端节点的
//      session.done 收不到；完成音与未读徽标当时只挂在那个 handler 里。
//   ② 「执行完成灭灯后时间不刷新成『刚刚』，而是退回上一句对话的时间（例如 12m）」
//      —— session.done 后 replace 全量重拉，mergeSessionItems([], next) 从空数组起算，
//      把流式期间推进的 activity_at 整条丢了；服务端 updated_at 是**最后一条对话消息**
//      的时间，agent 连跑 12 分钟工具就一直停在 12 分钟前。
//
// 两处都抽成纯函数，这里直接跑逻辑而不是匹配源码正则。

const ROOT = path.resolve(import.meta.dirname, "..");

function loadTs(relPath, stubs = {}) {
  const sourcePath = path.resolve(ROOT, relPath);
  const code = ts.transpileModule(fs.readFileSync(sourcePath, "utf8"), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
  }).outputText;
  const sandbox = {
    exports: {},
    module: { exports: {} },
    require: (id) => (stubs[id] ? stubs[id] : {}),
  };
  vm.runInNewContext(code, sandbox, { filename: sourcePath });
  return sandbox.exports;
}

const scope = loadTs("src/shared/scope.ts");
const { newlyCompletedPendingKeys } = loadTs("src/shared/pendingCompletion.ts", {
  "./scope": scope,
});
const merge = loadTs("src/services/session/sessionListMerge.ts", {
  "../../shared/sessionActivityAt": loadTs("src/shared/sessionActivityAt.ts"),
});

// vm 沙箱里造出来的 Array/Object 原型与本 realm 不同，deepEqual 会因此报
// 「same structure but not reference-equal」。断言前统一过一遍 JSON。
const plain = (v) => JSON.parse(JSON.stringify(v));

// ── 跨节点完成差分 ─────────────────────────────────────────────────

test("远端节点 true→false 判为刚完成；仍在跑的、失败的、激活节点的都不算", () => {
  const previous = {
    "pc::proj::s1": true, // 远端跑完了
    "fn::proj::s2": true, // 远端还在跑
    "home::proj::s3": true, // 激活节点：session.done 已经响过
    "pc::proj::s4": true, // 本轮 pc 请求失败，真相表保留旧值
  };
  const next = {
    "fn::proj::s2": true,
    "home::proj::s3": false,
    "pc::proj::s4": true,
  };
  const done = newlyCompletedPendingKeys(previous, next, ["pc", "fn", "home"], "home");
  assert.deepEqual(plain(done), ["pc::proj::s1"]);
});

test("激活节点不重复触发（否则 WS + 轮询会连响两遍）", () => {
  const previous = { "home::proj::s1": true };
  const next = {};
  assert.deepEqual(plain(newlyCompletedPendingKeys(previous, next, ["home"], "home")), []);
  // 换个激活节点后同一条就该算了
  assert.deepEqual(plain(newlyCompletedPendingKeys(previous, next, ["home"], "pc")), ["home::proj::s1"]);
});

test("本轮没问到的节点一律不算：失败被误判成完成会凭空点亮未读", () => {
  const previous = { "pc::proj::s1": true, "fn::proj::s2": true };
  const next = { "fn::proj::s2": true };
  // 只问到了 fn：pc 那条「消失」是因为没问，不是因为跑完了
  assert.deepEqual(plain(newlyCompletedPendingKeys(previous, next, ["fn"], "home")), []);
});

test("历史空 nodeId 键不算：反推不出节点，标未读会落到错误的键上", () => {
  const previous = { "proj::s1": true };
  const next = {};
  assert.deepEqual(plain(newlyCompletedPendingKeys(previous, next, ["pc"], "home")), []);
});

test("本来就没在跑的键不会凭空判完成", () => {
  const previous = { "pc::proj::s1": false };
  assert.deepEqual(plain(newlyCompletedPendingKeys(previous, {}, ["pc"], "home")), []);
});

// ── session key 反解 ───────────────────────────────────────────────

test("会话复合键能反解回三段，会话 key 自带 :: 也不截断", () => {
  assert.deepEqual(plain(scope.parseSessionScopeKey("pc::proj::s1")), {
    nodeId: "pc",
    rootId: "proj",
    sessionKey: "s1",
  });
  assert.deepEqual(plain(scope.parseSessionScopeKey("pc::proj::a::b")), {
    nodeId: "pc",
    rootId: "proj",
    sessionKey: "a::b",
  });
  // 历史单节点格式：2 段，节点段为空
  assert.deepEqual(plain(scope.parseSessionScopeKey("proj::s1")), {
    nodeId: "",
    rootId: "proj",
    sessionKey: "s1",
  });
  assert.deepEqual(plain(scope.parseSessionScopeKey("")), { nodeId: "", rootId: "", sessionKey: "" });
});

// ── 全量重拉的时间继承 ─────────────────────────────────────────────

test("replace 重拉继承 activity_at：灭灯瞬间时间仍是「刚刚」", () => {
  const previous = [
    {
      key: "s1",
      root_id: "proj",
      _nodeId: "pc",
      updated_at: "2026-10-25T10:00:00.000Z", // 最后一条对话消息：10:00
      activity_at: "2026-10-25T10:12:00.000Z", // 流式跑到 10:12
    },
  ];
  // 服务端全量重拉回来的还是那条 updated_at（12 分钟前）
  const incoming = [
    { key: "s1", root_id: "proj", _nodeId: "pc", updated_at: "2026-10-25T10:00:00.000Z" },
  ];
  const inherited = merge.inheritClientActivity(previous, incoming);
  assert.equal(inherited[0].activity_at, "2026-10-25T10:12:00.000Z", "不能把流式时间戳丢掉");
  // 走一遍正式归并，activity_at 仍在（它比 updated_at 新）
  const merged = merge.mergeSessionItems([], inherited);
  assert.equal(merged[0].activity_at, "2026-10-25T10:12:00.000Z");
});

test("服务端时间追上后继承值被清掉，不会永远停在「刚刚」", () => {
  const previous = [
    { key: "s1", root_id: "proj", _nodeId: "pc", updated_at: "2026-10-25T10:00:00.000Z", activity_at: "2026-10-25T10:12:00.000Z" },
  ];
  const incoming = [
    // 这一轮真的把最后一条消息落库了
    { key: "s1", root_id: "proj", _nodeId: "pc", updated_at: "2026-10-25T10:13:00.000Z" },
  ];
  const merged = merge.mergeSessionItems([], merge.inheritClientActivity(previous, incoming));
  assert.equal(merged[0].activity_at, undefined, "updated_at 追上后覆盖层必须作废");
});

test("继承按 _nodeId::key 作用域：跨节点同名会话不互相继承", () => {
  const previous = [
    { key: "s1", root_id: "proj", _nodeId: "pc", updated_at: "2026-10-25T10:00:00.000Z", activity_at: "2026-10-25T10:12:00.000Z" },
  ];
  const incoming = [
    { key: "s1", root_id: "proj", _nodeId: "home", updated_at: "2026-10-25T10:00:00.000Z" },
  ];
  assert.equal(
    merge.inheritClientActivity(previous, incoming)[0].activity_at,
    undefined,
    "home 的同名会话不得继承 pc 的活动时间",
  );
});

test("多项目列表（默认视图）也走同一条继承，且不做并集", () => {
  const source = fs.readFileSync(
    path.resolve(ROOT, "src/app/useMultiProjectSessions.ts"),
    "utf8",
  );
  assert.match(
    source,
    /inheritClientActivity\(/,
    "多项目分组同样从空数组重建，不继承就会在完成灭灯后把时间打回 12m",
  );
  // mergeSessionItems(prevGroup.sessions, …) 是并集语义：服务端本轮已不返回的会话
  // （删掉/归档）会被从旧分组里复活。继承只能借用活动时间，不能借用成员。
  assert.doesNotMatch(
    source,
    /mergeSessionItems\(\s*prev(Group|ious)?\.?sessions/,
    "分组继承不得用并集归并，否则已删除的会话会复活",
  );
});
