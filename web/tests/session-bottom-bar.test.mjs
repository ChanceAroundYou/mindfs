import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { test } from "vitest";

// 2026-11-28 用户要求重做会话尾部（G-SK）：
//   1. 控件只留复制 / fork / 时间，agent 图标移除；
//   2. 每条已完成消息各自保留控件行，不聚合；
//   3. 消息流最后一行：左侧 运行中 > 子代理 > 最新轮控件（互斥），右侧状态栏；随流滚动；
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

test("SessionViewer 尾行：左侧等待/最新控件互斥，右侧状态条", () => {
  const viewer = read("src/components/session/SessionViewer.tsx");
  assert.match(viewer, /<SessionViewerBottomBar/);
  assert.match(viewer, /left=\{\s*showWaiting \?/, "尾行左侧由 showWaiting 决定");
  assert.match(viewer, /<SessionViewerWaitingIndicator/, "运行中分支渲染等待提示");
  assert.match(viewer, /<SessionViewerTurnActionsSlot/, "空闲分支渲染最新轮控件");
  assert.match(viewer, /right=\{\s*<SessionViewerStatusBar/, "状态栏固定在尾行右侧");
  assert.match(
    viewer,
    /showWaiting = hasSessionWaitingIndicator\(\{/,
    "尾行判据必须与指示器共用，避免两套条件漂移",
  );
  assert.match(
    viewer,
    /skipActionsAtIndex=\{showWaiting \? -1 : latestCompletedIndex\}/,
    "空闲时最新轮控件由尾行接管；运行中所有已完成轮保留时间线控件",
  );
  const bar = read("src/components/session/SessionViewerBottomBar.tsx");
  assert.match(bar, /data-testid="session-bottom-bar"/);
  assert.match(bar, /data-testid="session-bottom-left"/);
  assert.match(bar, /data-testid="session-bottom-right"/);
  assert.match(bar, /flexWrap: "nowrap"/, "控件与状态栏必须同一行");
  assert.doesNotMatch(bar, /position: "sticky"|position: "fixed"/, "尾行随消息流滚动，不悬浮");
  const status = read("src/components/session/SessionViewerStatusBar.tsx");
  assert.match(status, /justifyContent: "flex-end"/, "状态栏居右");
  assert.match(status, /flexWrap: "nowrap"/, "状态栏自身不换行、不挤到下一行");
  assert.doesNotMatch(status, /position: "sticky"|position: "fixed"/, "状态栏不固定");
});

test("每条已完成轮自有控件行；只留复制 / fork / 时间", () => {
  const actions = read("src/components/session/SessionViewerTurnActions.tsx");
  assert.doesNotMatch(actions, /AgentIcon/, "agent 图标已移除");
  assert.match(actions, /session\.copyMarkdown/);
  assert.match(actions, /session\.forkFromMessage/);
  assert.match(actions, /data-testid="turn-meta-time"/);
  const timeline = read("src/components/session/SessionViewerTimeline.tsx");
  assert.match(timeline, /lastIndexByTurn\.get\(turnMeta\.turnKey\) === idx/, "每轮在收尾位置画控件");
  assert.match(timeline, /idx !== skipActionsAtIndex/, "空闲时只跳过最新一轮（由尾行接管）");
  assert.match(timeline, /<SessionViewerTurnActions/, "时间线内保留历史轮控件");
  const slot = read("src/components/session/SessionViewerTurnActionsSlot.tsx");
  assert.match(slot, /findLatestCompletedTurnIndex/, "尾行只取最新完成轮");
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