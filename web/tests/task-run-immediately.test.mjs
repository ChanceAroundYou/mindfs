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

// 「立即执行」的落点：任务新建面板与任务详情面板都把它放进输入框右下角的
// 控件行（PromptEditor 的 extraControls），新建侧是开关、详情侧是派发动作。
// 这里钉源码契约、组件输出与纯函数行为，几何由 Playwright 实测。

const read = (p) => readFileSync(new URL(p, import.meta.url), "utf8");
const render = (node) => renderToString(React.createElement(I18nProvider, null, node));
const inputProps = {
  body: "",
  onBodyChange: () => {},
  choice: "empty",
  versionId: "",
  onWorkflowChange: () => {},
  onSend: () => {},
};

test("共用控件：开关是真实 checkbox，动作是一颗按钮，外观只有一套", () => {
  const control = read("../src/components/task/RunImmediatelyControl.tsx");

  assert.match(control, /export type RunImmediatelyControlProps = \{[\s\S]{0,600}?mode: "toggle" \| "action"/);
  // 开关：真实 checkbox（读屏与键盘拿到的仍是开关），值由 active 驱动。
  assert.match(control, /<input[\s\S]{0,200}?type="checkbox"[\s\S]{0,200}?checked=\{active\}/);
  assert.match(control, /aria-label=\{label\}/);
  // 动作：按钮，派发在飞时置灰并写「执行中…」。
  assert.match(control, /<button[\s\S]{0,300}?disabled=\{inactive\}/);
  assert.match(control, /running \? t\("task\.startingNow"\) : label/);
  // 两处共用同一颗胶囊：只有 pillStyle 一份外观，且与「立即执行」共用图标。
  assert.equal((control.match(/function pillStyle/g) || []).length, 1);
  assert.match(control, /<RunNowIcon \/>/);
  assert.match(control, /data-testid="run-immediately-toggle"/);
  assert.match(control, /data-testid="run-immediately-action"/);
});

test("新建面板：开关进了输入框右下角控件行，不再手搓勾选框", () => {
  const panel = read("../src/components/app/TaskInlineEditPanel.tsx");

  assert.match(panel, /extraControls=\{\(\n\s+<RunImmediatelyControl/);
  assert.match(panel, /mode="toggle"[\s\S]{0,200}?active=\{taskInlineEdit\.startImmediately\}/);
  assert.match(panel, /onToggle=\{\(next\) => setTaskInlineEdit/);
  assert.match(panel, /extraControlsExtraInset=\{84\}/);
  // 名字那一行只剩输入框：旧的内联 checkbox 与它的样式块都删掉了。
  assert.doesNotMatch(panel, /type="checkbox"/);
  assert.doesNotMatch(panel, /accentColor: "var\(--accent-color\)", cursor/);
});

test("详情面板：排队留言的「立即执行」从气泡页脚搬到输入框控件行", () => {
  const view = read("../src/components/task/ConversationView.tsx");
  const input = read("../src/components/task/ActivityInput.tsx");
  const message = read("../src/components/task/ActivityMessage.tsx");

  // 气泡页脚只留「待执行」状态胶囊，不再自带派发按钮。
  assert.match(message, /activity\.status === "queued" \? \([\s\S]{0,200}?t\("task\.queued"\)/);
  assert.doesNotMatch(message, /onRunQueued|runQueuedButtonStyle|task\.startImmediately/);

  // 连接层把排队留言折算成输入框的一个动作入口。
  assert.match(view, /const queuedActivity = queuedUserActivity\(activities\)/);
  assert.match(view, /onRunQueued=\{queuedActivity && !disabled \?/);
  assert.doesNotMatch(view, /onRunQueued=\{disabled \? undefined/, "旧的「按 disabled 判断」入口已经搬走");

  // 输入框把它渲染成共用控件的 action 形态，并与工作流选择器同一行。
  assert.match(input, /onRunQueued\?: \(\) => void;/);
  assert.match(input, /<RunImmediatelyControl mode="action" running=\{runningQueued\} onClick=\{onRunQueued\} \/>/);
  assert.match(input, /<WorkflowSelector/);
  assert.match(input, /extraControlsExtraInset=\{onRunQueued \? 84 : 0\}/);
});

test("组件输出：开关读当前值、动作只在有排队留言时出现、气泡页脚不再带按钮", () => {
  const off = render(React.createElement(RunImmediatelyControl, { mode: "toggle", active: false }));
  const on = render(React.createElement(RunImmediatelyControl, { mode: "toggle", active: true }));
  assert.match(off, /data-testid="run-immediately-toggle"/);
  assert.match(on, /type="checkbox"[^>]*checked/);
  assert.doesNotMatch(off, /type="checkbox"[^>]*checked/);
  assert.match(render(React.createElement(RunImmediatelyControl, { mode: "action" })), /data-testid="run-immediately-action"/);

  // 输入框：没有排队留言时控件行里没有它，有才出现。
  const without = render(React.createElement(ActivityInput, inputProps));
  assert.doesNotMatch(without, /run-immediately/);
  const withQueued = render(
    React.createElement(ActivityInput, { ...inputProps, onRunQueued: () => {} }),
  );
  assert.match(withQueued, /data-testid="run-immediately-action"/);

  // 排队留言的气泡只留状态胶囊。
  const bubble = render(
    React.createElement(ActivityMessage, {
      activity: { id: "a1", type: "user_message", status: "queued", body: "hi", created_at: "2026-11-21T00:00:00Z" },
    }),
  );
  assert.match(bubble, /待执行|Queued/);
  assert.doesNotMatch(bubble, /run-immediately|<button/);
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

test("两种语言都有开关提示文案", () => {
  const zh = read("../src/i18n/locales/zh-CN-tasks.ts");
  const en = read("../src/i18n/locales/en-US-tasks.ts");
  for (const src of [zh, en]) {
    assert.match(src, /"task\.startImmediately": "/);
    assert.match(src, /"task\.startImmediatelyHint": "/);
    assert.match(src, /"task\.startingNow": "/);
  }
  assert.match(zh, /"task\.startImmediatelyHint": "建完任务就立刻派发首条留言"/);
});
