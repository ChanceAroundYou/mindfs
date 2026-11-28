import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import ts from "typescript";
import vm from "node:vm";
import { test } from "vitest";

// 2026-11-27 CMAI 实测：尾部同一段 598 字思考块出现两次，且模型/用量统计行消失。
// 持久数据（blocks / exchange jsonl）里没有重复，重复只来自两条前端来源：
//   1) 窗口与缓存各持一份不同对象的同一 seq —— composedExchanges 只按对象同一性去重；
//   2) 已落定的 thinking 由 blocks 渲染（带 turnMeta），流式残留的 seq=0 thought 行
//      内容一字不差却没有 turnMeta —— overlay 只在 mindfs_id 相同时按身份对账。
// 这里钉住内容兜底与回合边界判据。

function loadModule(sourcePath, stubs) {
  const source = fs.readFileSync(sourcePath, "utf8");
  const compiled = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
  }).outputText;
  const sandbox = {
    exports: {},
    module: { exports: {} },
    require(specifier) {
      if (stubs && stubs[specifier]) return stubs[specifier];
      throw new Error(`Unexpected module import: ${specifier}`);
    },
  };
  vm.runInNewContext(compiled, sandbox, { filename: sourcePath });
  return sandbox.exports;
}

const timeline = loadModule(path.resolve(import.meta.dirname, "../src/hooks/sessionTimeline.ts"), {
  "./sessionAssistantTimeline": { buildAssistantTimeline: () => [] },
  "./stableTimelineID": { stableTimelineID: () => "stable" },
  "./sessionBlocks": {
    blocksForSeq: () => undefined,
    blocksTextContent: () => "",
    buildAssistantTimelineFromBlocks: () => [],
    isBlankText: () => false,
    isPlaceholderThought: () => false,
    normalizeToolCall: (value) => value,
  },
});

const format = loadModule(
  path.resolve(import.meta.dirname, "../src/components/session/sessionViewerFormat.ts"),
  {},
);

test("duplicate transient thought is dropped once the same content is persisted", () => {
  const meta = { seq: 237 };
  const items = [
    { id: "block:237:45", type: "thought", content: "同一段 598 字思考", turnMeta: meta },
    { id: "block:237:46", type: "assistant_text", content: "结论", turnMeta: meta },
    { id: "transient-thought", type: "thought", content: "  同一段 598 字思考\n" },
  ];
  const out = timeline.dropDuplicatedTransientThoughts(items);
  assert.deepEqual(out.map((item) => item.id), ["block:237:45", "block:237:46"]);
});

test("a genuinely new transient thought is kept", () => {
  const items = [
    { id: "block:237:45", type: "thought", content: "上一轮思考", turnMeta: { seq: 237 } },
    { id: "transient-thought", type: "thought", content: "这一轮刚流出来的思考" },
  ];
  const out = timeline.dropDuplicatedTransientThoughts(items);
  assert.deepEqual(out.map((item) => item.id), ["block:237:45", "transient-thought"]);
});

test("completed turns own exactly one control-row position, even with a trailing transient row", () => {
  const turn237 = { seq: 237, turnKey: "s237", completed: true };
  const turn239 = { seq: 239, turnKey: "s239", completed: true };
  const running = { seq: 0, turnKey: "t30", completed: false };
  const text = { id: "block:237:46", type: "assistant_text", content: "结论", turnMeta: turn237 };
  const sameTurn = { id: "block:237:47", type: "tool", toolCall: {}, turnMeta: turn237 };
  const nextTurn = { id: "block:239:0", type: "assistant_text", content: "下一轮", turnMeta: turn239 };
  const runningText = { id: "t30:0", type: "assistant_text", content: "运行中", turnMeta: running };
  const transient = { id: "transient-thought", type: "thought", content: "下一轮" };
  const user = { id: "u", type: "user_text", content: "下一问" };
  const items = [text, sameTurn, user, nextTurn, transient, runningText];

  const ends = format.collectCompletedTurnEndIndices(items);
  assert.equal(ends.size, 2, "只有已完成的持久轮次拥有控件行位置");
  assert.equal(ends.get("s237"), 1, "同一轮的最后一行才是收尾");
  assert.equal(ends.get("s239"), 3);
  assert.equal(ends.has("t30"), false, "运行中的瞬时轮次不得出现控件");
  assert.equal(format.collectCompletedTurnEndIndices([text, transient]).get("s237"), 0,
    "尾部瞬时行不改变上一轮的收尾位置");
});