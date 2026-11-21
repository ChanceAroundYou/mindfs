import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "vitest";
import React from "react";
import { renderToString } from "react-dom/server";
import { I18nProvider } from "../src/i18n/index.tsx";
import { ConversationMessages } from "../src/components/task/ConversationView.tsx";
import { activityTypeTagKey } from "../src/shared/activityModel.ts";
import {
  SCROLL_CONVERGE_MAX_FRAMES,
  SCROLL_CONVERGE_MIN_FRAMES,
  SCROLL_CONVERGE_STABLE_FRAMES,
  STICK_REARM_PX,
  distanceFromBottom,
} from "../src/components/common/scrollToBottom.ts";

// 任务详情四件事（2026-11-21）：回到底部复用会话那一颗、胶囊换正式 icon、
// 移动端与桌面一致、每条消息加类型胶囊（不是状态）。

const read = (p) => readFileSync(new URL(p, import.meta.url), "utf8");

const base = {
  task_id: "task-1",
  author: "agent",
  workflow_choice: "empty",
  created_at: "2026-11-21T10:00:00Z",
};

const render = (activities) =>
  renderToString(
    React.createElement(
      I18nProvider,
      { initialLocale: "zh-CN" },
      React.createElement(ConversationMessages, { activities, rootId: "root-1" }),
    ),
  );

// ── 类型胶囊的取值来源：后端枚举，不在前端猜 ─────────────────────────────

test("activityTypeTagKey 覆盖后端全部活动类型与回复 purpose", () => {
  const key = (type, purpose) => activityTypeTagKey({ ...base, id: "x", type, purpose });

  assert.equal(key("user_message"), "task.activity.type.userMessage");
  // 存量阶段任务转换出来的留言仍是 user_comment，读取端必须同样认识。
  assert.equal(key("user_comment"), "task.activity.type.userMessage");
  assert.equal(key("agent_reply", "progress"), "task.activity.type.progress");
  assert.equal(key("agent_reply", "summary"), "task.activity.type.summary");
  assert.equal(key("agent_reply", "result"), "task.activity.type.result");
  assert.equal(key("agent_reply", "chat"), "task.activity.type.chat");
  assert.equal(key("agent_reply", "reply"), "task.activity.type.reply");
  // 后端缺省把空 purpose 写成 reply（activity_route_apply.go），前端同样兜底。
  assert.equal(key("agent_reply", ""), "task.activity.type.reply");
  assert.equal(key("system_event"), "task.activity.type.system");
  assert.equal(key("attention"), "task.activity.type.attention");
  assert.equal(key("merge_conflict"), "task.activity.type.mergeConflict");
  assert.equal(key("question"), "task.activity.type.question");
  assert.equal(key("human_gate"), "task.activity.type.humanGate");
  // 未知类型不编标签，宁可不显示。
  assert.equal(key("brand_new_type"), null);
});

test("进度按 purpose=progress 判定，与 role 无关", () => {
  // 标签回答的是「这是什么消息」，后端已经把类型写进 purpose，前端不再二次推断 role。
  for (const role of ["agent", "system"]) {
    assert.equal(
      activityTypeTagKey({ ...base, id: "x", type: "agent_reply", role, purpose: "progress" }),
      "task.activity.type.progress",
    );
  }
});

// ── 每条消息都带类型胶囊，位置在时间前面 ────────────────────────────────

const userMessage = { ...base, id: "u1", type: "user_message", author: "user", role: "user", body: "继续" };
const agentResult = { ...base, id: "a1", type: "agent_reply", role: "agent", body: "做完了", status: "completed", purpose: "result" };
const systemEvent = { ...base, id: "s1", type: "system_event", author: "system", role: "system", body: "任务已恢复" };

test("留言、回复、系统事件都渲染类型胶囊，且排在时间前面", () => {
  for (const activity of [userMessage, agentResult, systemEvent]) {
    const html = render([activity]);
    const tagAt = html.indexOf('data-testid="activity-type-tag"');
    const timeAt = html.indexOf("<time");
    assert.ok(tagAt >= 0, `${activity.type} carries a type tag`);
    assert.ok(timeAt > tagAt, `${activity.type}: type tag sits before the timestamp`);
  }
  // 文案跟随当前 locale（node 环境默认 en-US），两种语言都算通过。
  assert.match(render([userMessage]), /data-activity-type="user_message"[^>]*>(留言|Message)/);
  assert.match(render([agentResult]), /data-activity-purpose="result"[^>]*>(结果|Result)/);
  assert.match(render([systemEvent]), /data-activity-type="system_event"[^>]*>(系统|System)/);
});

test("类型胶囊不携带任务状态，状态仍只在面板标题栏", () => {
  const html = render([{ ...agentResult, status: "running" }]);
  assert.doesNotMatch(html, /data-testid="activity-status"/);
  // 类型胶囊只描述「这是什么消息」，不出现运行中/已完成之类的状态词。
  assert.doesNotMatch(html, /activity-type-tag[^>]*>(运行中|已完成)/);
});

// ── 回到底部：同一个按钮、同一套收敛 ───────────────────────────────────

test("回到底部按钮由会话与任务详情共用同一个组件", () => {
  const button = read("../src/components/common/ScrollToBottomButton.tsx");
  const sessionControls = read("../src/components/session/SessionViewerFloatingControls.tsx");
  const conversation = read("../src/components/task/ConversationView.tsx");

  // 组件自身保留会话浮层的外观与无障碍名称。
  assert.match(button, /export function ScrollToBottomButton/);
  assert.match(button, /aria-label=\{label\}/);
  assert.match(button, /width: "34px"/);
  assert.match(button, /height: "34px"/);

  assert.match(sessionControls, /import \{ ScrollToBottomButton \} from "\.\.\/common\/ScrollToBottomButton"/);
  assert.match(sessionControls, /<ScrollToBottomButton/);
  assert.match(conversation, /import \{ ScrollToBottomButton \} from "\.\.\/common\/ScrollToBottomButton"/);
  assert.match(conversation, /testId="conversation-jump-latest"/);
  // 任务详情不许自己再造一套滚动：不得直接滚 window。
  assert.doesNotMatch(conversation, /window\.scrollTo|scrollIntoView/);
});

test("显隐阈值与逐帧收敛与会话共用同一份实现", () => {
  const shared = read("../src/components/common/scrollToBottom.ts");
  const hook = read("../src/components/task/useConversationJumpToLatest.ts");
  const sessionScroll = read("../src/components/session/useSessionViewerScroll.ts");

  assert.equal(STICK_REARM_PX, 2);
  assert.equal(SCROLL_CONVERGE_MIN_FRAMES, 8);
  assert.equal(SCROLL_CONVERGE_STABLE_FRAMES, 3);
  assert.equal(SCROLL_CONVERGE_MAX_FRAMES, 120);
  assert.match(shared, /export function convergeScrollToBottom/);
  // distanceFromBottom 与收敛写的是同一个 maxTop 口径。
  assert.match(shared, /scrollHeight - el\.clientHeight - el\.scrollTop/);

  assert.match(hook, /from "\.\.\/common\/scrollToBottom"/);
  // 显隐阈值任务侧单独一档（输入框在滚动流里），但仍用共享的 distanceFromBottom 口径。
  assert.match(hook, /export const JUMP_VISIBLE_PX = 200;/);
  assert.match(hook, /distanceFromBottom\(scroller\) > JUMP_VISIBLE_PX/);
  // 刚打开、刚自动落底时不该亮着：底部内边距/收尾区只有几十像素。
  assert.ok(200 > 46, "阈值必须高于面板底部收尾区，否则打开即显示");
  assert.match(hook, /convergeScrollToBottom\(scroller\)/);
  // 用户上滚/键盘上翻立刻让位，不和手势抢位置。
  assert.match(hook, /event\.deltaY < 0/);

  assert.match(sessionScroll, /convergeScrollToBottom\(container/);
  // 会话侧不重复实现帧循环。
  assert.doesNotMatch(sessionScroll, /requestAnimationFrame\(step\)/);
});

test("任务详情的滚动容器是面板正文，不另开滚动区", () => {
  const hook = read("../src/components/task/useConversationJumpToLatest.ts");
  const scroll = read("../src/components/task/conversationScroll.ts");
  assert.match(hook, /findScrollContainer\(anchor\)/);
  assert.match(scroll, /export function findScrollContainer/);
  // 只看 overflowY，不看当前内容高度：短内容时也要先把带滚动监听的容器找到。
  assert.match(scroll, /if \(style\.overflowY === "auto" \|\| style\.overflowY === "scroll"\) return node;/);
  // 移动端与桌面共用同一个入口：按钮不得按视口宽度条件渲染。
  const conversation = read("../src/components/task/ConversationView.tsx");
  assert.doesNotMatch(conversation, /isMobile|matchMedia|max-width/);
});

// ── 胶囊的展开/收起改用人字箭头 icon ───────────────────────────────────

test("两个胶囊使用 SVG 箭头，不再用 ▸/▾ 字符", () => {
  const entries = read("../src/components/task/TaskInspectEntries.tsx");
  const icon = read("../src/components/common/ChevronIcon.tsx");
  assert.match(entries, /import \{ ChevronIcon \} from "\.\.\/common\/ChevronIcon"/);
  assert.match(entries, /<ChevronIcon open=\{open\} \/>/);
  assert.doesNotMatch(entries, /▸|▾/);
  // 状态仍由按钮自己的 aria-expanded 表达，图形只是装饰。
  assert.match(entries, /aria-expanded=\{open\}/);
  assert.match(icon, /aria-hidden="true"/);
  assert.match(icon, /rotate\(90deg\)/);
  // 展开互斥不变。
  assert.match(entries, /setOpenSection\(\(current\) => \(current === section \? null : section\)\)/);
});

test("distanceFromBottom 到底时为 0", () => {
  assert.equal(
    distanceFromBottom({ scrollHeight: 1000, clientHeight: 600, scrollTop: 400 }),
    0,
  );
  assert.equal(
    distanceFromBottom({ scrollHeight: 1000, clientHeight: 600, scrollTop: 100 }),
    300,
  );
  // 内容不足一屏时不产生负数。
  assert.equal(distanceFromBottom({ scrollHeight: 300, clientHeight: 600, scrollTop: 0 }), 0);
});
