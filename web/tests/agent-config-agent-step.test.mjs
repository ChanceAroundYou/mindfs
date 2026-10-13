import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";

const read = (path) => readFileSync(new URL(path, import.meta.url), "utf8");

test("agent config flow delegates agent selection without moving flow state", () => {
  const parent = read("../src/components/file/AgentConfigPopover.tsx");
  const step = read("../src/components/file/AgentConfigAgentStep.tsx");
  assert.match(parent, /<AgentConfigAgentStep[\s\S]{0,400}?onChooseAgent=\{onChooseAgent\}/);
  assert.match(parent, /step === "agent" \? \([\s\S]*?\) : step === "confirm"/);
  assert.match(step, /agents\.length === 0/);
  assert.match(step, /flow !== "switch" \|\| !onRestartAgent/);
  assert.match(step, /void onRestartAgent\(agent\.name\)/);
  assert.match(step, /onSelect=\{onChooseAgent\}/);
});
