import assert from "node:assert/strict";
import { test } from "vitest";
import { readFileSync } from "./source-map-fs.mjs";

// 发送时丢字：编辑器内容经 onChange 异步进父 state，快打后立刻发送时
// save/handle 闭包拿到的是旧值。契约：发送入口必须同步从编辑器读最新文本。

const handlers = readFileSync(new URL("../src/components/action/useActionBarHandlers.ts", import.meta.url), "utf8");
const promptEditor = readFileSync(new URL("../src/components/editor/PromptEditor.tsx", import.meta.url), "utf8");
const stageEditor = readFileSync(new URL("../src/components/task/StageEditor.tsx", import.meta.url), "utf8");
const detailHook = readFileSync(new URL("../src/components/task/useTaskDetailPanel.ts", import.meta.url), "utf8");
const composer = readFileSync(new URL("../src/app/useTaskComposer.ts", import.meta.url), "utf8");
const handleType = readFileSync(new URL("../src/shared/actionBarTypes.ts", import.meta.url), "utf8");

test("handleSend reads the latest editor text instead of lagging state", () => {
  assert.match(
    handlers,
    /getSerializedText/,
    "handleSend must flush the latest text from the editor imperatively",
  );
});

test("PromptEditor funnels button click and Enter through one flush-and-send", () => {
  assert.match(
    promptEditor,
    /getSerializedText\(\) \?\? value/,
    "flush must read the latest text from the editor, falling back to the parent value",
  );
  assert.match(
    promptEditor,
    /onSend\?\.\(latest\)/,
    "flush must pass the latest text to onSend",
  );
  const flushCalls = promptEditor.match(/flushAndSend\(\)/g) || [];
  assert.ok(
    flushCalls.length >= 2,
    "both the send button and Enter must go through the shared flush-and-send",
  );
  assert.doesNotMatch(
    promptEditor,
    /onSend\?\.\(\)/,
    "no send entry may call onSend without the flushed text",
  );
  assert.match(
    stageEditor,
    /onSend\?: \(text: string\) => void/,
    "StageEditor must forward the flushed text type",
  );
});

test("stage/inline saves use the flushed text instead of closure state", () => {
  assert.match(
    detailHook,
    /saveStage = async \(index: number, prompt: string\)/,
    "saveStage must take the flushed prompt as a required argument",
  );
  assert.match(
    composer,
    /saveTaskInlineEdit = useCallback\(async \(prompt\?: string\)|saveTaskInlineEdit = useCallback\(async \(prompt: string\)/,
    "saveTaskInlineEdit must accept the flushed prompt",
  );
  assert.match(
    handleType,
    /getSerializedText: \(\) => string/,
    "TokenEditorHandle must guarantee getSerializedText",
  );
});
