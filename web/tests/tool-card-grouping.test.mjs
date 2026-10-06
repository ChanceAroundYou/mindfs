// 工具卡分组的契约（2026-10-07）。
//
// 背景：agent 一个回合里连开 100+ 个 edit/read/execute 是常态，逐个渲染
// ToolCallCard 是「打开会话卡」的主要渲染成本（实测单会话 389 张、单 seq 189 张）。
// useSessionStream 的 groupConsecutiveToolCalls 把**连续同类、数量 >= 5**的卡折成
// 一个 tool_group 项，SessionViewer 渲染成一张可展开的组卡。
//
// 这些测试守的每一条都对应一个具体的坏法：
//   1. 阈值失效 → 2 张卡也被折成组，用户多点一次才能看到内容
//   2. 跨 kind 合并 → 「5 个编辑」里混进 read，用户以为读文件改了东西
//   3. ask_user 被折 → 答题卡消失，用户答不了
//   4. 非 tool 项不断组 → 一条 thought 前后的 edit 被当成一组，时间线错位
//   5. 分组丢了 toolCall → 展开组卡是空的
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import ts from "typescript";
import vm from "node:vm";

const SRC = path.resolve(import.meta.dirname, "../src");

function loadModule(relPath) {
  const file = path.resolve(SRC, relPath);
  const compiled = ts.transpileModule(fs.readFileSync(file, "utf8"), {
    compilerOptions: {
      module: ts.ModuleKind.CommonJS,
      target: ts.ScriptTarget.ES2020,
      jsx: ts.JsxEmit.React,
    },
  }).outputText;
  // useSessionStream 顶层 import react / sessionService / i18n；分组逻辑是纯函数，
  // 只桩掉这三处即可，不需要真 React 运行时。
  const sandbox = {
    exports: {},
    module: { exports: {} },
    require: (name) => {
      if (name === "react") {
        return {
          useEffect: () => {},
          useMemo: (fn) => fn(),
          useRef: (value) => ({ current: value }),
          useState: (value) => [value, () => {}],
        };
      }
      if (name.includes("services/session")) return { sessionService: {} };
      if (name.includes("i18n")) return { translateNow: (key) => key };
      throw new Error(`unexpected require: ${name}`);
    },
    console,
  };
  vm.runInNewContext(compiled, sandbox, { filename: file });
  return sandbox.exports;
}

const { groupConsecutiveToolCalls } = loadModule("hooks/useSessionStream.ts");

function tool(id, kind) {
  return { id, type: "tool", toolCall: { callId: id, kind, status: "complete" } };
}

function thought(id) {
  return { id, type: "thought", content: "thinking" };
}

// ── 阈值：>= 5 才折 ──
const sixEdits = groupConsecutiveToolCalls(
  Array.from({ length: 6 }, (_, i) => tool(`e${i}`, "edit")),
);
assert.equal(sixEdits.length, 1, "6 个连续 edit 必须折成 1 个 tool_group");
assert.equal(sixEdits[0].type, "tool_group");
assert.equal(sixEdits[0].kind, "edit");
assert.equal(sixEdits[0].toolCalls.length, 6, "分组必须保留全部 toolCall，否则展开是空的");

const fourEdits = groupConsecutiveToolCalls(
  Array.from({ length: 4 }, (_, i) => tool(`e${i}`, "edit")),
);
assert.equal(fourEdits.length, 4, "4 个连续 edit 低于阈值，必须原样保留");
assert.ok(fourEdits.every((item) => item.type === "tool"));

const fiveEdits = groupConsecutiveToolCalls(
  Array.from({ length: 5 }, (_, i) => tool(`e${i}`, "edit")),
);
assert.equal(fiveEdits.length, 1, "5 个连续 edit 正好到阈值，必须折");

// ── 跨 kind 不合并 ──
const mixed = groupConsecutiveToolCalls([
  ...Array.from({ length: 5 }, (_, i) => tool(`e${i}`, "edit")),
  ...Array.from({ length: 5 }, (_, i) => tool(`r${i}`, "read")),
]);
assert.equal(mixed.length, 2, "edit 段与 read 段必须分成两组");
assert.equal(mixed[0].kind, "edit");
assert.equal(mixed[1].kind, "read");

// ── ask_user 不参与分组 ──
const withAsk = groupConsecutiveToolCalls([
  ...Array.from({ length: 5 }, (_, i) => tool(`a${i}`, "ask_user")),
]);
assert.equal(withAsk.length, 5, "ask_user 必须保持独立（要答题），不折组");
assert.ok(withAsk.every((item) => item.type === "tool"));

// ── 非 tool 项断组 ──
const interrupted = groupConsecutiveToolCalls([
  ...Array.from({ length: 3 }, (_, i) => tool(`e${i}`, "edit")),
  thought("t1"),
  ...Array.from({ length: 3 }, (_, i) => tool(`f${i}`, "edit")),
]);
assert.equal(interrupted.length, 7, "被 thought 打断的两段各 3 张，都不够阈值");
assert.equal(interrupted[3].type, "thought");

// ── 大小写归一：KIND 来自后端，可能是 "Edit" ──
const upper = groupConsecutiveToolCalls(
  Array.from({ length: 6 }, (_, i) => tool(`e${i}`, "Edit")),
);
assert.equal(upper.length, 1, "kind 必须小写归一后再比较");
assert.equal(upper[0].kind, "edit");

// ── 非 tool 项原样透传 ──
const passthrough = groupConsecutiveToolCalls([
  { id: "u1", type: "user_text", content: "hi" },
  { id: "todo1", type: "todo", todoUpdate: {} },
]);
assert.equal(passthrough.length, 2);
assert.equal(passthrough[0].type, "user_text");
assert.equal(passthrough[1].type, "todo");

console.log("tool-card-grouping: ok");
