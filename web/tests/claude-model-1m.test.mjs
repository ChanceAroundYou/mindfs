import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";

const root = path.resolve(import.meta.dirname, "..");
// model 帮助函数已拆到 action/modelUtils（2026-09 ActionBar 拆分），契约随文件走，内容不变。
const actionBar = fs.readFileSync(path.join(root, "src/components/ActionBar.tsx"), "utf8");
const modelUtils = fs.readFileSync(path.join(root, "src/components/action/modelUtils.ts"), "utf8");
const selector = fs.readFileSync(path.join(root, "src/components/AgentSelector.tsx"), "utf8");
const app = fs.readFileSync(path.join(root, "src/App.tsx"), "utf8");
const streamCache = fs.readFileSync(path.join(root, "src/app/useSessionStreamCache.ts"), "utf8");

assert.match(modelUtils, /function modelBaseForAgent\(agentName: string \| undefined, model: string\)/, "modelBaseForAgent must compare Claude aliases on their canonical base");
assert.match(actionBar, /modelBaseForAgent\(selectedAgent\.name, item\.id\) === modelBaseForAgent\(selectedAgent\.name, model\)/, "ActionBar must retain of[1m] when the advertised model is fable");
assert.match(selector, /function claudeModelBase\(model: string\)/, "AgentSelector must compare Claude aliases on their canonical base");
assert.match(actionBar, /if \(explicitModel\) \{\s*setLongContext\(isClaudeAgentName\(nextAgent\) && has1MSuffix\(explicitModel\)\);\s*\}/s, "choosing a concrete model must not retain a stale 1M toggle");

console.log("claude-model-1m.test.mjs: OK");

// Third-party / generic models must NOT be rewritten to op/os alias, but MUST support generic [1m].
assert.match(modelUtils, /function isClaudeAliasModel\(model: string\)/, "modelUtils must guard alias family");
assert.match(modelUtils, /if \(isClaudeAliasModel\(base\)\) \{/, "with1MSuffix must be generic (alias branch + generic branch)");
assert.match(modelUtils, /return enabled \? `\$\{base\}\[1m\]` : base;/, "with1MSuffix must append [1m] for generic models");
assert.match(selector, /function isClaudeAliasModelName\(model: string\)/, "AgentSelector must guard alias family");
assert.match(selector, /if \(!isClaudeAliasModelName\(model\)\) return strip1MSuffix\(model\);/, "AgentSelector claudeModelBase must passthrough non-alias models");
assert.match(selector, /if \(isClaudeAliasModelName\(targetModel\)\)/, "AgentSelector submenu fallback must only alias-match inside family");
assert.match(selector, /strip1MSuffix\(item\.id\) === strip1MSuffix\(targetModel\)/, "AgentSelector must handle generic [1m] fallback");

// Realtime optimistic display must carry model_display_name from backend (user_message push + accepted ack + chunk merge).
assert.match(app, /model_display_name:\s*exchange\?\.model_display_name/, "App must store model_display_name from session.user_message WS push");
// 2026-09 App.tsx 拆分：运行时元信息解析与 chunk 合并搬到 app/useSessionStreamCache.ts，契约随文件走。
assert.match(streamCache, /model_display_name: pickText\("model_display_name"\)/, "App runtime meta must resolve model_display_name like model");
assert.match(streamCache, /runtimeMeta\.model_display_name \|\| last\.model_display_name/, "App chunk merge must carry realtime model_display_name");
assert.match(app, /payload\?\.model_display_name/, "App session.accepted must backfill realtime model_display_name");

console.log("claude-model-1m-realtime-display.test.mjs: OK");
