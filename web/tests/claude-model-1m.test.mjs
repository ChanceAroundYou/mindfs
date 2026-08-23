import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";

const root = path.resolve(import.meta.dirname, "..");
const actionBar = fs.readFileSync(path.join(root, "src/components/ActionBar.tsx"), "utf8");
const selector = fs.readFileSync(path.join(root, "src/components/AgentSelector.tsx"), "utf8");

assert.match(actionBar, /function modelBaseForAgent\(agentName: string \| undefined, model: string\)/, "ActionBar must compare Claude aliases on their canonical base");
assert.match(actionBar, /modelBaseForAgent\(selectedAgent\.name, item\.id\) === modelBaseForAgent\(selectedAgent\.name, model\)/, "ActionBar must retain of[1m] when the advertised model is fable");
assert.match(selector, /function claudeModelBase\(model: string\)/, "AgentSelector must compare Claude aliases on their canonical base");
assert.match(actionBar, /if \(explicitModel\) \{\s*setLongContext\(isClaudeAgentName\(nextAgent\) && has1MSuffix\(explicitModel\)\);\s*\}/s, "choosing a concrete model must not retain a stale 1M toggle");

console.log("claude-model-1m.test.mjs: OK");
