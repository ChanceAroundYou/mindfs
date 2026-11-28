import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import ts from "typescript";
import vm from "node:vm";
import { test } from "vitest";

// 思考块尾部残留 + 页脚被带走（2026-11-28，fn `CMAI / most申报优化` seq 670）。
//
// 现场：回合 10:47 已结束，10:51 截图仍是「正文 → 控件行 → 思考过程(1400 字符)」。
// 权威块流按 pos 排是思考(264) 在正文(265–267) 之前，所以截图只能是**流式瞬时行**
// 没被收掉：
//   · `handleAssistantMessage` 在消息结束时才发 thinking 块（未走 delta 时正文
//     早已由 delta 追加），实时时间线因此天然是「正文 → 思考」；
//   · 浏览器只连激活节点的一条 WS，查看远端（fn）会话时 `session.done` 根本收不到；
//   · 唯一跨节点完成通道是 5s `useReplyingSessionPoll`，它当时只看完成音/未读，
//     选中会话的 true→false 被 `continue` 跳过，从未用持久化窗口重锚。
// 于是瞬时思考行永久留在尾部，task-137（G-SK）拆出的控件行按「完整轮次最后一条
// 正文」定位，正好停在这块思考**上方**——截图里「页脚被思考块带走」的观感。
//
// 修法（G-SL）：轮询检测到选中会话完成时，走与 WS done 相同的重锚序列：
// restoreActiveSession（nodeId 显式）→ 选中态换新缓存 → viewer 按 _anchoredAt
// 原子换窗。`pending=false` 的组装规则不接回 seq=0，瞬时尾巴随之退役。

function loadModule(relPath, exportNames, requireMap = {}) {
  const sourcePath = path.resolve(import.meta.dirname, "..", relPath);
  const source = fs.readFileSync(sourcePath, "utf8");
  const compiled = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
  }).outputText;
  const sandbox = {
    exports: {},
    module: { exports: {} },
    require: (id) => {
      for (const [needle, mod] of Object.entries(requireMap)) {
        if (id.includes(needle)) return mod;
      }
      return {};
    },
  };
  vm.runInNewContext(compiled, sandbox, { filename: sourcePath });
  for (const name of exportNames) {
    assert.equal(typeof sandbox.exports[name], "function", `${relPath} must export ${name}`);
  }
  return sandbox.exports;
}

const scope = loadModule("src/shared/scope.ts", ["scopeSessionKey", "parseSessionScopeKey"]);
const pending = loadModule(
  "src/shared/pendingCompletion.ts",
  ["selectCompletedSelectedSession"],
  { scope },
);
const merge = loadModule(
  "src/services/session/sessionExchangeMerge.ts",
  ["composeLoadedExchanges"],
);
const format = loadModule(
  "src/components/session/sessionViewerFormat.ts",
  ["collectCompletedTurnEndIndices"],
);

const { scopeSessionKey } = scope;
const { selectCompletedSelectedSession } = pending;
const { composeLoadedExchanges } = merge;
const { collectCompletedTurnEndIndices } = format;

// 模块在 vm 的独立 realm 里执行，返回对象的原型与宿主不同；spread 到本 realm 再比。
const sameScope = (value) => (value ? { ...value } : value);

const NODE = "fn";
const ROOT = "CMAI";
const KEY = "1795694836-79c10cf6ca5d";
const completedKey = scopeSessionKey(NODE, ROOT, KEY);

test("完成键只匹配选中会话，且节点身份两侧都有时必须一致", () => {
  assert.deepEqual(
    sameScope(selectCompletedSelectedSession([completedKey], { key: KEY, _nodeId: NODE })),
    { nodeId: NODE, rootId: ROOT, sessionKey: KEY },
  );
  // 远端会话：选中项带 _nodeId，命中后调用方才能把 nodeId 传给重锚。
  assert.deepEqual(
    sameScope(selectCompletedSelectedSession([completedKey], { session_key: KEY, _nodeId: NODE })),
    { nodeId: NODE, rootId: ROOT, sessionKey: KEY },
  );
  // 同名会话键但不同节点：不能拿别的节点的完成去重锚当前视图。
  assert.equal(
    selectCompletedSelectedSession([completedKey], { key: KEY, _nodeId: "local" }),
    null,
  );
  // 选中项没有节点身份（老数据）时退化为按会话键匹配。
  assert.deepEqual(
    sameScope(selectCompletedSelectedSession([completedKey], { key: KEY })),
    { nodeId: NODE, rootId: ROOT, sessionKey: KEY },
  );
  // 别人的会话跑完与当前视图无关。
  assert.equal(
    selectCompletedSelectedSession(
      [scopeSessionKey(NODE, ROOT, "1795694836-000000000000")],
      { key: KEY, _nodeId: NODE },
    ),
    null,
  );
  // 空键/未选中：不触发。
  assert.equal(selectCompletedSelectedSession([completedKey], null), null);
  assert.equal(selectCompletedSelectedSession([completedKey], { key: "" }), null);
});

test("pending=false 重锚组装必须丢掉 seq=0 的瞬时思考尾巴", () => {
  const persistedText = { seq: 670, role: "agent", content: "\n本轮全仓审核完成" };
  const persistedThought = { seq: 670, role: "thought", content: "思考：1400 字" };
  const transientThought = { seq: 0, role: "thought", content: "思考：1400 字" };
  const server = [persistedThought, persistedText];
  const cached = [{ seq: 669, role: "agent", content: "上一轮" }, transientThought];

  const reanchored = composeLoadedExchanges(server, cached, false);
  assert.equal(
    reanchored.some((ex) => Number(ex?.seq || 0) === 0),
    false,
    "回合已结束（pending=false）时不得把 seq=0 瞬时行接回——那正是尾部残留的思考块",
  );
  assert.equal(reanchored.some((ex) => ex?.role === "thought"), false, "窗口里没有瞬时思考");
  assert.equal(reanchored.some((ex) => ex?.seq === 670 && ex?.role === "agent"), true);

  // 反向守卫：在途（pending=true）时必须接回瞬时行，否则流式内容会消失。
  const inFlight = composeLoadedExchanges(server, cached, true);
  assert.equal(inFlight.some((ex) => ex?.role === "thought" && ex?.seq === 0), true);
});

test("控件行归属仍钉在完整轮次的最后一条正文，不被瞬时思考行带偏", () => {
  const items = [
    { type: "thought", content: "思考", turnMeta: { turnKey: "s670", completed: true } },
    { type: "assistant", content: "正文", turnMeta: { turnKey: "s670", completed: true } },
    // 流式残留：没有 turnMeta，也没有回合身份。
    { type: "thought", content: "思考尾巴" },
  ];
  const ends = collectCompletedTurnEndIndices(items);
  assert.equal(ends.get("s670"), 1, "控件行必须落在这轮最后一条正文之后");
  assert.notEqual(ends.get("s670"), 2, "瞬时思考行不能成为控件行的锚点");
});

test("轮询的完成分支必须调用选中会话重锚回调", () => {
  const poll = fs.readFileSync(
    path.resolve(import.meta.dirname, "..", "src/app/useReplyingSessionPoll.ts"),
    "utf8",
  );
  assert.match(
    poll,
    /const selectedCompleted = selectCompletedSelectedSession\(\s*completedAnyNode,/,
    "必须用「不排除激活节点」的完成差分去匹配选中会话",
  );
  assert.match(
    poll,
    /reanchorSelectedRef\.current\?\.\(selectedCompleted\)/,
    "匹配到选中会话后必须触发重锚（只放音/标未读会永久停在瞬时尾巴）",
  );
  // 回调走 ref，避免内联箭头每次渲染换引用把 5s 轮询重启成死循环。
  assert.match(poll, /onSelectedSessionCompleted\?: \(completed: \{ nodeId: string; rootId: string; sessionKey: string \}\) => void;/);
  assert.match(poll, /const reanchorSelectedRef = useRef\(onSelectedSessionCompleted\);/);
});

test("重锚序列与 WS done 一致，并透传显式节点身份", () => {
  const lifecycle = fs.readFileSync(
    path.resolve(import.meta.dirname, "..", "src/app/useSessionLifecycle.ts"),
    "utf8",
  );
  assert.match(lifecycle, /const reanchorSession = useCallback\(/);
  assert.match(lifecycle, /loadedSessionRef\.current\[cacheKey\] = true;/);
  assert.match(lifecycle, /clearSessionStale\(rootID, sessionKey\);/);
  assert.match(lifecycle, /setSelectedSession\(\(prev\) =>/);
  assert.match(lifecycle, /reanchorSession,\s*\n\s*syncSessionHeaderFromListItem/, "必须从 lifecycle 暴露");

  // 远端 root 在同名项目下会撞名：节点身份显式传入，不能只靠 root→node 猜测。
  assert.match(lifecycle, /nodeID\?: string \| null,/);
  assert.match(lifecycle, /nodeId: resolvedNodeId,/);
  assert.ok(
    (lifecycle.match(/nodeId: resolvedNodeId,/g) || []).length >= 2,
    "窗口与全量两条加载分支都要用显式节点",
  );
  assert.ok(
    (lifecycle.match(/_nodeId: resolvedNodeId,/g) || []).length >= 2,
    "缓存/回包都要带节点身份，后续 toSessionItem 才不会丢",
  );
});

test("层间接线：轮询回调 → reanchorSession，且 nodeId 原样传递", () => {
  const layer = fs.readFileSync(
    path.resolve(import.meta.dirname, "..", "src/app/useAppSessionLayer.ts"),
    "utf8",
  );
  assert.match(layer, /restoreActiveSession, reanchorSession, syncSessionHeaderFromListItem/);
  assert.match(layer, /onSelectedSessionCompleted: handleSelectedSessionCompleted/);
  assert.match(
    layer,
    /void reanchorSession\(completed\.rootId, completed\.sessionKey, completed\.nodeId\)/,
    "轮询观察到的节点就是权威节点，不能再由 root 反推",
  );

  const reload = fs.readFileSync(
    path.resolve(import.meta.dirname, "..", "src/app/useAppMultiProjectReload.ts"),
    "utf8",
  );
  assert.ok(
    (reload.match(/onSelectedSessionCompleted/g) || []).length >= 3,
    "参数声明、类型与向 useReplyingSessionPoll 的透传三处都要有",
  );
});