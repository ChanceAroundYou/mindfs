import { test } from "vitest";
import assert from "node:assert/strict";
import { rawReadFileSync } from "./source-map.mjs";

const read = (path) => rawReadFileSync(new URL(path, import.meta.url), "utf8");

const actionBar = read("../src/components/action/ActionBar.tsx");
const chips = read("../src/components/action/ComposerTopChips.tsx");
const memoryIndicator = read("../src/components/agent/AgentMemoryIndicator.tsx");
const useAgentMemory = read("../src/components/agent/useAgentMemory.ts");
const agentMemoryService = read("../src/services/agent/agentMemory.ts");

test("徽标带是流内一行：排在排队列表之前，不再是输入框上的浮层", () => {
  const chipsIndex = actionBar.indexOf("<ComposerTopChips");
  const queuedIndex = actionBar.indexOf("<QueuedMessagesList");
  const inputIndex = actionBar.indexOf('data-onboarding="message-input"');

  assert.ok(chipsIndex >= 0, "ActionBar must render ComposerTopChips");
  assert.ok(queuedIndex >= 0, "ActionBar must render QueuedMessagesList");
  assert.ok(chipsIndex < queuedIndex, "徽标带必须排在排队列表之前，才能被排队消息顶起来");
  assert.ok(chipsIndex < inputIndex, "徽标带必须离开输入框内部");
  assert.doesNotMatch(chips, /position:\s*"absolute"/);
  assert.doesNotMatch(chips, /bottom:\s*"calc\(100%/);
});

test("徽标带不再有 agent 早退守卫，内存徽标对所有 agent 显示", () => {
  assert.doesNotMatch(chips, /return null/);
  assert.doesNotMatch(actionBar, /planModeActive \|\| \(!currentSession && currentRootIsGitRepo/);
  const memoryIndex = chips.indexOf("<AgentMemoryIndicator");
  const planIndex = chips.indexOf("<span>Plan</span>");
  assert.ok(memoryIndex >= 0, "内存徽标必须渲染");
  assert.ok(planIndex >= 0, "Plan 芯片仍然渲染");
  assert.ok(memoryIndex < planIndex, "内存徽标必须左置（排在左组首位）");
});

test("限额徽标也在左组：徽标带整体靠左，不靠 justify-content 分到右端", () => {
  const limitIndex = chips.indexOf("<CodexRateLimitIndicator");
  const memoryIndex = chips.indexOf("<AgentMemoryIndicator");
  assert.ok(limitIndex > memoryIndex, "限额徽标必须排在内存徽标之后、同属左组");
  const leftGroups = chips.match(/display: "flex", alignItems: "center", gap: "6px", minWidth: 0/g) || [];
  assert.equal(leftGroups.length, 1, "徽标带只允许一个左组，限额徽标不得另起右组");
  assert.match(chips, /justifyContent: "flex-start"/);
  assert.doesNotMatch(chips, /justifyContent: "space-between"/);
});

test("内存徽标跟随当前项目所在节点", () => {
  assert.match(chips, /<AgentMemoryIndicator[^>]*nodeId=\{rootNodeId\}/);
  assert.match(useAgentMemory, /nodeId\?: string/);
  assert.match(useAgentMemory, /fetchAgentMemory\(nodeId\)/);
  assert.match(useAgentMemory, /releaseAgentSessions\(nodeId\)/);
  assert.match(agentMemoryService, /appPath\("\/api\/agents\/memory", nodeId\)/);
  assert.match(agentMemoryService, /appPath\("\/api\/agents\/release-idle", nodeId\)/);
  assert.match(memoryIndicator, /useAgentMemory\(props\)/);
});
