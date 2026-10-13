import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";

const read = (path) => readFileSync(new URL(path, import.meta.url), "utf8");

test("AgentSelector delegates its trigger presentation without moving menu state", () => {
  const parent = read("../src/components/agent/AgentSelector.tsx");
  const trigger = read("../src/components/agent/AgentSelectorTrigger.tsx");
  assert.match(parent, /<AgentSelectorTrigger[\s\S]{0,220}?onClick=\{\(\) =>/);
  assert.match(parent, /<AgentSelectorTrigger[\s\S]*?setMenuBodyHeight\(null\)/);
  assert.match(trigger, /export function AgentSelectorTrigger/);
  assert.match(trigger, /<AgentIcon agentName=\{agent\}/);
  assert.match(trigger, /onMouseDown=\{\(event\) => event\.preventDefault\(\)\}/);
  assert.match(trigger, /title=\{buttonTitle\}/);
  assert.match(trigger, /warnUnavailable \? \(/);
  assert.match(trigger, /if \(compact\) event\.currentTarget\.style\.background/);
});
