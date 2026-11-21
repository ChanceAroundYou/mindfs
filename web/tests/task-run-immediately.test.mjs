import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import React from "react";
import { renderToString } from "react-dom/server";
import { test } from "vitest";
import { I18nProvider } from "../src/i18n/index.tsx";
import { ActivityInput } from "../src/components/task/ActivityInput.tsx";
import { ActivityMessage } from "../src/components/task/ActivityMessage.tsx";
import { RunImmediatelyControl } from "../src/components/task/RunImmediatelyControl.tsx";
import { queuedUserActivity } from "../src/shared/activityModel.ts";

// 「立即执行」的落点与形态：
//  - 新建任务面板：输入框右下角控件行里的一个纯图标方形开关，外观与发送键同一套
//    （composerSendButtonStyle 的 panel 形态），旁边是「选择工作流」。
//  - 任务详情面板：发送框里不再有它；排队留言自己的页脚才带派发按钮。
// 这里钉源码契约与组件输出，几何由 Playwright 实测。

const read = (p) => readFileSync(new URL(p, import.meta.url), "utf8");
const render = (node) => renderToString(React.createElement(I18nProvider, null, node));
const inputProps = {
  body: "",
  onBodyChange: () => {},
  rootId: "/tmp/project",
  choice: "empty",
  versionId: "",
  onWorkflowChange: () => {},
  onSend: () => {},
};

test("开关只有一种形态：纯图标方形，与发送键共用 composerSendButtonStyle", () => {
  const control = read("../src/components/task/RunImmediatelyControl.tsx");

  // 不再有 toggle/action 两态，也没有另一套胶囊外观。
  assert.doesNotMatch(control, /mode: "toggle" \| "action"/);
  assert.doesNotMatch(control, /function pillStyle/);
  assert.doesNotMatch(control, /data-testid="run-immediately-action"/);

  // 外观直接取自发送键那套：panel 形态 + 主题色，禁用时同一函数的灰底。
  assert.match(control, /composerSendButtonStyle\(\{/);
  assert.match(control, /variant: "panel"/);
  assert.match(control, /enabled: active && !disabled/);

  // 仍是真实 checkbox（读屏与键盘拿到的还是开关），只是文字被藏起来。
  assert.match(control, /<input[\s\S]{0,200}?type="checkbox"[\s\S]{0,200}?checked=\{active\}/);
  assert.match(control, /const label = t\("task\.startImmediately"\)/);
  assert.match(control, /aria-label=\{label\}/);
  assert.match(control, /const hint = t\("task\.startImmediatelyHint"\)/);
  assert.match(control, /title=\{hint\}/);
  assert.match(control, /data-testid="run-immediately-toggle"/);
  // 图标而不是文字。
  assert.match(control, /<RunNowIcon \/>/);
  assert.doesNotMatch(control, />\s*\{label\}\s*</, "按钮里不再渲染文字标签");
});

test("新建面板：开关 + 选择工作流同在控件行，详情面板只有选择工作流", () => {
  const panel = read("../src/components/app/TaskInlineEditPanel.tsx");
  const input = read("../src/components/task/ActivityInput.tsx");

  assert.match(panel, /extraControls=\{\(\n\s+<>/);
  assert.match(panel, /<RunImmediatelyControl[\s\S]{0,300}?active=\{taskInlineEdit\.startImmediately\}/);
  assert.match(panel, /onToggle=\{\(next\) => setTaskInlineEdit/);
  assert.match(panel, /<WorkflowSelector[\s\S]{0,300}?choice=\{taskInlineEdit\.workflowChoice\}/);
  assert.match(panel, /extraControlsExtraInset=\{128\}/);
  // 名字那一行只剩输入框：旧的内联 checkbox 已删。
  assert.doesNotMatch(panel, /type="checkbox"/);

  // 详情面板：发送框不带「立即执行」，只保留选择工作流。
  assert.doesNotMatch(input, /RunImmediatelyControl/);
  assert.match(input, /<WorkflowSelector/);
  assert.match(input, /extraControlsExtraInset=\{0\}/);
});

test("排队留言的派发入口在气泡页脚，不在发送框", () => {
  const view = read("../src/components/task/ConversationView.tsx");
  const input = read("../src/components/task/ActivityInput.tsx");
  const message = read("../src/components/task/ActivityMessage.tsx");

  // 气泡页脚：状态胶囊 + 有回调时才出现的派发按钮。
  assert.match(message, /activity\.status === "queued" \? \([\s\S]{0,200}?t\("task\.queued"\)/);
  assert.match(message, /data-testid="activity-run-queued"/);
  assert.match(message, /onRunQueued\(activity\)/);
  assert.match(message, /runningQueued \? t\("task\.startingNow"\) : t\("task\.startImmediately"\)/);

  // 连接层只在真有排队留言、任务未终态时把回调传下去。
  assert.match(view, /const queuedActivity = queuedUserActivity\(activities\)/);
  assert.match(view, /onRunQueued=\{queuedActivity && !disabled \? runQueuedFromMessage : undefined\}/);
  assert.doesNotMatch(input, /onRunQueued/);
});

test("组件输出：开关是方形图标键，排队气泡按有无回调决定是否带按钮", () => {
  const off = render(React.createElement(RunImmediatelyControl, { active: false, onToggle: () => {} }));
  const on = render(React.createElement(RunImmediatelyControl, { active: true, onToggle: () => {} }));
  for (const html of [off, on]) {
    assert.match(html, /data-testid="run-immediately-toggle"/);
    // 与发送键同一套几何：28×28、圆角 8px、方形。
    assert.match(html, /width:28px;height:28px;border-radius:8px/);
    assert.doesNotMatch(html, /立即执行<\/|建完任务就/, "按钮里没有可见文字");
  }
  assert.match(on, /type="checkbox"[^>]*checked/);
  assert.doesNotMatch(off, /type="checkbox"[^>]*checked/);
  // 开启态实心 accent 底，关闭态是同一函数的灰底（不是另一套样式）。
  assert.match(on, /background:var\(--accent-color\)/);
  assert.match(off, /background:rgba\(100, 116, 139, 0\.22\)/);

  // 详情输入框：只有工作流选择器。
  const without = render(React.createElement(ActivityInput, inputProps));
  assert.match(without, /data-testid="workflow-selector"/);
  assert.doesNotMatch(without, /run-immediately/);

  // 排队气泡：默认不带按钮，传了回调才带。
  const queuedActivity = { id: "a1", type: "user_message", status: "queued", body: "hi", created_at: "2026-11-21T00:00:00Z" };
  const bubble = render(React.createElement(ActivityMessage, { activity: queuedActivity }));
  assert.match(bubble, /待执行|Queued/);
  assert.doesNotMatch(bubble, /activity-run-queued/);
  const bubbleWithButton = render(
    React.createElement(ActivityMessage, { activity: queuedActivity, onRunQueued: () => {} }),
  );
  assert.match(bubbleWithButton, /data-testid="activity-run-queued"/);
  const runningBubble = render(
    React.createElement(ActivityMessage, { activity: queuedActivity, onRunQueued: () => {}, runningQueued: true }),
  );
  assert.match(runningBubble, /data-testid="activity-run-queued"[^>]*disabled/);
});

test("只有真的存在排队留言时才给入口", () => {
  const queued = { id: "a1", type: "user_message", status: "queued" };
  assert.equal(queuedUserActivity([queued])?.id, "a1");
  // 转换出来的历史留言同样算排队。
  assert.equal(queuedUserActivity([{ id: "a2", type: "user_comment", status: "queued" }])?.id, "a2");
  // 已派发 / 别的类型 / 空列表都不算。
  assert.equal(queuedUserActivity([{ id: "a3", type: "user_message", status: "running" }]), null);
  assert.equal(queuedUserActivity([{ id: "a4", type: "agent_reply", status: "queued" }]), null);
  assert.equal(queuedUserActivity([]), null);
});

test("两种语言都有开关与派发文案", () => {
  const zh = read("../src/i18n/locales/zh-CN-tasks.ts");
  const en = read("../src/i18n/locales/en-US-tasks.ts");
  for (const src of [zh, en]) {
    assert.match(src, /"task\.startImmediately": "/);
    assert.match(src, /"task\.startImmediatelyHint": "/);
    assert.match(src, /"task\.startingNow": "/);
  }
  assert.match(zh, /"task\.startImmediatelyHint": "建完任务就立刻派发首条留言"/);
});
