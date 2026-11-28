import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { test } from "vitest";

// 2026-11-28 用户要求重做会话尾部（G-SK）：
//   1. 控件只留复制 / fork / 时间，agent 图标移除；
//   2. 控件与「正在思考 / 子代理运行中」共用底部左侧一个槽位，互斥，运行中 > 子代理 > 控件；
//   3. 状态栏常驻、居右，与控件同一行；
//   4. 底部关联文件面板彻底移除，只留左侧栏入口。
const root = path.resolve(import.meta.dirname, "..");
const read = (rel) => fs.readFileSync(path.join(root, rel), "utf8");

const { findLatestCompletedTurnIndex } = await import(
  "../src/components/session/sessionViewerFormat.ts"
);
const { hasSessionWaitingIndicator } = await import(
  "../src/components/session/SessionViewerWaitingIndicator.tsx"
);

const turn = (turnKey, completed) => ({
  id: turnKey,
  type: "assistant_text",
  content: turnKey,
  turnMeta: { turnKey, completed, content: turnKey, seq: 1 },
});

test("底部控件槽只认最新一个已完整结束的轮次", () => {
  const items = [
    turn("s1", true),
    { id: "u", type: "user_text", content: "下一问" },
    turn("s2", true),
    { id: "transient-thought", type: "thought", content: "运行中" },
    turn("s3", false),
  ];
  assert.equal(findLatestCompletedTurnIndex(items), 2);
  assert.equal(findLatestCompletedTurnIndex([]), -1);
  assert.equal(findLatestCompletedTurnIndex([turn("s9", false)]), -1);
});

test("等待提示三档共用同一判据，运行中优先于子代理", () => {
  const base = { isAwaiting: false, isStreaming: false };
  assert.equal(hasSessionWaitingIndicator(base), false);
  assert.equal(hasSessionWaitingIndicator({ ...base, isStreaming: true }), true);
  assert.equal(hasSessionWaitingIndicator({ ...base, isAwaiting: true }), true);
  assert.equal(hasSessionWaitingIndicator({ ...base, isDelegating: true }), true);
  assert.equal(hasSessionWaitingIndicator({ ...base, isAwaitingAnswer: true }), true);
  // 指示器内部把「空心档」压到自跑之下：delegating/awaiting 只在既未等待也未流式时成立。
  const indicator = read("src/components/session/SessionViewerWaitingIndicator.tsx");
  assert.match(
    indicator,
    /const delegating = !isAwaiting && !isStreaming && !!isDelegating;/,
    "running 必须压过 subagent",
  );
  assert.match(
    indicator,
    /const awaitingAnswer = !isAwaiting && !isStreaming && !!isAwaitingAnswer;/,
    "running 必须压过 awaiting-answer",
  );
});

test("SessionViewer 把等待提示与控件放进同一个左侧槽位，状态栏固定右侧", () => {
  const viewer = read("src/components/session/SessionViewer.tsx");
  assert.match(viewer, /<SessionViewerBottomBar/);
  assert.match(viewer, /left=\{\s*showWaiting \?/, "左槽由 showWaiting 决定");
  assert.match(viewer, /<SessionViewerWaitingIndicator/, "运行中分支渲染等待提示");
  assert.match(viewer, /<SessionViewerTurnActionsSlot/, "空闲分支渲染上一轮控件");
  assert.match(viewer, /right=\{\s*<SessionViewerStatusBar/, "状态栏固定在右槽");
  assert.match(
    viewer,
    /showWaiting = hasSessionWaitingIndicator\(\{/,
    "槽位判据必须与指示器共用，避免两套条件漂移",
  );
  const bar = read("src/components/session/SessionViewerBottomBar.tsx");
  assert.match(bar, /data-testid="session-bottom-bar"/);
  assert.match(bar, /data-testid="session-bottom-left"/);
  assert.match(bar, /data-testid="session-bottom-right"/);
  assert.match(bar, /flexWrap: "nowrap"/, "控件与状态栏必须同一行");
  const status = read("src/components/session/SessionViewerStatusBar.tsx");
  assert.match(status, /justifyContent: "flex-end"/, "状态栏居右");
  assert.match(status, /flexWrap: "nowrap"/, "状态栏自身不换行、不挤到下一行");
});

test("控件行只留复制 / fork / 时间，时间线不再渲染控件", () => {
  const actions = read("src/components/session/SessionViewerTurnActions.tsx");
  assert.doesNotMatch(actions, /AgentIcon/, "agent 图标已移除");
  assert.match(actions, /session\.copyMarkdown/);
  assert.match(actions, /session\.forkFromMessage/);
  assert.match(actions, /data-testid="turn-meta-time"/);
  const timeline = read("src/components/session/SessionViewerTimeline.tsx");
  assert.doesNotMatch(timeline, /SessionViewerTurnActions/);
  assert.doesNotMatch(timeline, /lastIndexByTurn/);
});

test("底部关联文件面板彻底移除，只剩左侧栏入口", () => {
  const viewer = read("src/components/session/SessionViewer.tsx");
  assert.doesNotMatch(viewer, /SessionViewerRelatedFiles/);
  assert.doesNotMatch(viewer, /useRelatedFiles/);
  assert.equal(
    fs.existsSync(path.join(root, "src/components/session/SessionViewerRelatedFiles.tsx")),
    false,
  );
  assert.equal(
    fs.existsSync(path.join(root, "src/components/session/useRelatedFiles.ts")),
    false,
  );
  // 左侧栏入口（文件树 related 页签）仍在。
  const tabs = read("src/components/file/FileTreeProjectTabs.tsx");
  assert.match(tabs, /fileTree\.relatedFiles/);
});