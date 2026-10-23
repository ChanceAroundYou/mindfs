import { test } from "vitest";
import assert from "node:assert/strict";
import { rawReadFileSync } from "./source-map.mjs";

const read = (path) => rawReadFileSync(new URL(path, import.meta.url), "utf8");

const actionBar = read("../src/components/action/ActionBar.tsx");
const chips = read("../src/components/action/ComposerTopChips.tsx");
const memoryIndicator = read("../src/components/agent/AgentMemoryIndicator.tsx");
const codexIndicator = read("../src/components/agent/CodexRateLimitIndicator.tsx");
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

test("胶囊字号统一且比正文小一档", () => {
  // 徽标带里内存 / 限额 / Plan / worktree 四类胶囊必须同字号，且严格小于正文
  // 输入字号 —— 否则一排彩色胶囊比用户正在写的内容还抢眼。
  const composerStyles = read("../src/components/action/composerStyles.ts");
  const declared = composerStyles.match(/export const COMPOSER_CHIP_FONT_SIZE = (\d+);/);
  assert.ok(declared, "COMPOSER_CHIP_FONT_SIZE 必须在 composerStyles 里单点声明");
  const inputSize = Number(composerStyles.match(/export const INPUT_FONT_SIZE = (\d+);/)?.[1]);
  assert.ok(
    Number(declared[1]) < inputSize,
    `胶囊字号 ${declared[1]}px 必须小于正文 ${inputSize}px`,
  );
  // 三处胶囊文件都不许再写死字号（写死一处就漂移一档）。
  for (const [name, src] of [["ComposerTopChips", chips], ["AgentMemoryIndicator", memoryIndicator], ["CodexRateLimitIndicator", codexIndicator]]) {
    assert.ok(src.includes("COMPOSER_CHIP_FONT_SIZE"), `${name} 必须引用共享字号`);
    assert.doesNotMatch(src, /fontSize: "\d+px", fontWeight: 700, lineHeight: 1/, `${name} 不得再写死胶囊字号`);
  }
});
