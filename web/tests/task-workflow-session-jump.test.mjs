import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "vitest";
import React from "react";
import { renderToString } from "react-dom/server";
import { I18nProvider } from "../src/i18n/index.tsx";
import { WorkflowSessionJump } from "../src/components/task/WorkflowSessionJump.tsx";
import { pickWorkflowSessionKey } from "../src/components/task/runTimelineModel.ts";

// 任务详情页的「跳到工作流会话」。
//
// 为什么需要：工作流节点把活干在各自的 agent 会话里，任务对话只放汇报。用户想
// 看节点实际做了什么、或者接管那个会话，得先进运行视图再逐节点点。这里给一个
// 直达入口：按「哪个节点最值得看」挑一条 session_key，一次点击就跳过去。
//
// 挑法必须是确定性的纯函数（可单测），不能让按钮的落点随数组顺序飘。

const read = (p) => readFileSync(new URL(p, import.meta.url), "utf8");

const nodeRun = (id, status, sessionKey) => ({
  id,
  run_id: "run-1",
  node_id: id,
  attempt: 1,
  status,
  ...(sessionKey ? { session_key: sessionKey } : {}),
});

test("优先跳到正在运行的节点会话", () => {
  const key = pickWorkflowSessionKey([
    nodeRun("done", "success", "sess-done"),
    nodeRun("live", "running", "sess-live"),
    nodeRun("later", "pending", "sess-later"),
  ]);
  assert.equal(key, "sess-live");
});

test("没有运行中节点时优先等待用户处理的节点", () => {
  const key = pickWorkflowSessionKey([
    nodeRun("done", "success", "sess-done"),
    nodeRun("gate", "waiting_user", "sess-gate"),
  ]);
  assert.equal(key, "sess-gate");
});

test("全部结束则取最后一条有会话的节点", () => {
  // ListWorkflowNodeRuns 按 created_at 升序返回，最后一条就是最近那次执行。
  const key = pickWorkflowSessionKey([
    nodeRun("first", "success", "sess-1"),
    nodeRun("second", "fail", "sess-2"),
  ]);
  assert.equal(key, "sess-2");
});

test("没有会话键时返回空串，不返回 undefined 或占位值", () => {
  assert.equal(pickWorkflowSessionKey([]), "");
  assert.equal(pickWorkflowSessionKey([nodeRun("a", "success"), nodeRun("b", "running")]), "");
  // 空白字符串同样是「没有会话」：跳过去只会打开一个空页面。
  assert.equal(pickWorkflowSessionKey([nodeRun("c", "success", "   ")]), "");
  // 末尾没有会话的节点不能把前面真正有会话的节点顶掉（空串也要覆盖）。
  assert.equal(
    pickWorkflowSessionKey([nodeRun("d", "success", "sess-d"), nodeRun("e", "success", "  ")]),
    "sess-d",
  );
});

const render = (props) =>
  renderToString(
    React.createElement(I18nProvider, null, React.createElement(WorkflowSessionJump, props)),
  );

// 页面 locale 由运行环境决定（node 里通常是 en-US），所以断言两种语言都认；
// 关键是「文字只出现在 title/aria-label 里，按钮体里只有图标」。
const titled = (html, zh, en) =>
  new RegExp(`aria-label="${zh}"`).test(html) || new RegExp(`aria-label="${en}"`).test(html);
// 加载中的英文是省略号形式（Loading…/Loading...），按前缀认。
const loadingTitled = (html) => /aria-label="(加载中|Loading)/.test(html);

test("按钮直接打开会话，不需要先展开运行视图", () => {
  const html = render({ sessionKey: "sess-live", loading: false, onOpenSession: () => {} });
  assert.match(html, /data-testid="workflow-session-jump"/, "the jump button must be identifiable");
  assert.ok(titled(html, "工作流会话", "Workflow session"), "the icon-only button must still name its target for assistive tech");
  assert.match(html, /title="(工作流会话|Workflow session)"/, "the icon-only button must explain itself on hover");
  assert.doesNotMatch(html, /disabled/, "a node with a session must be clickable");
});

// 任务 #87 第 4 条：它原来是一枚带文字的独立胶囊，和运行视图里的逐节点入口抢
// 位置。现在是一枚图标按钮，复用已有的 WorkflowIcon —— 不新画图标。
test("入口收成一枚复用 WorkflowIcon 的图标按钮，不再是一枚文字胶囊", () => {
  const src = read("../src/components/task/WorkflowSessionJump.tsx");
  assert.match(src, /import \{ WorkflowIcon \} from "\.\/taskIcons"/, "the jump must reuse the existing workflow icon");
  assert.match(src, /<WorkflowIcon/, "the icon must actually be rendered");
  assert.doesNotMatch(
    src,
    /\{label\}\s*<\/button>/,
    "the label must move out of the button body into title/aria-label",
  );
  const html = render({ sessionKey: "sess-live", loading: false, onOpenSession: () => {} });
  // 按钮体必须是「只有图标」：开标签之后紧跟 svg，闭标签之前没有任何文本。
  assert.match(html, /<button[^>]*>(\s*)<svg[\s\S]*?<\/svg><\/button>/, "the button body must contain only the icon");
  assert.doesNotMatch(html, />工作流会话</, "no visible text pill anymore");
});

test("没有可跳会话时按钮禁用并说明原因", () => {
  const html = render({ sessionKey: "", loading: false, onOpenSession: () => {} });
  assert.match(html, /disabled/, "a jump with no session must be disabled");
  assert.ok(titled(html, "暂无工作流会话", "No workflow session"), "the disabled state must explain itself");
});

test("加载中按钮禁用，避免把空结果当成「没有会话」", () => {
  const html = render({ sessionKey: "", loading: true, onOpenSession: () => {} });
  assert.match(html, /disabled/, "loading must not look like an empty session list");
  assert.ok(loadingTitled(html), "loading must say so instead of claiming there is no session");
  assert.doesNotMatch(html, /暂无工作流会话|No workflow session/, "an unfinished fetch is not proof of absence");
});

test("重取期间的旧会话键仍可点，不闪禁用", () => {
  // refreshKey 变化会重拉节点行；session_key 绑定后不再变化，旧键依然有效。
  const html = render({ sessionKey: "sess-live", loading: true, onOpenSession: () => {} });
  assert.doesNotMatch(html, /disabled/, "a known session must stay clickable while refreshing");
});

test("入口挂在运行视图组件里，面板本身不碰运行视图内部实现", () => {
  const entry = read("../src/components/task/RunViewEntry.tsx");
  const panel = read("../src/components/task/TaskDetailPanel.tsx");
  const hook = read("../src/components/task/useRunViewEntry.ts");

  assert.match(entry, /<WorkflowSessionJump/, "RunViewEntry must mount the jump affordance");
  assert.match(entry, /pickWorkflowSessionKey/, "the selection rule stays in the pure model");
  // 取数留在 hook（.ts）：视图层不得 import services。
  assert.match(hook, /pickWorkflowSessionKey|nodeRunsLoading/);
  assert.match(hook, /fetchWorkflowNodeRuns\(rootId, taskId, nodeId\)/, "node runs must be fetched with an explicit nodeId");
  assert.doesNotMatch(panel, /RunTimeline/, "TaskDetailPanel must stay out of the run view internals");
  assert.doesNotMatch(panel, /run-timeline/, "TaskDetailPanel must stay out of the run view internals");
});

test("i18n 键齐全", () => {
  const zh = read("../src/i18n/locales/zh-CN-tasks.ts");
  const en = read("../src/i18n/locales/en-US-tasks.ts");
  for (const key of ["task.runTimeline.openWorkflowSession", "task.runTimeline.noWorkflowSession"]) {
    assert.ok(zh.includes(`"${key}"`), `zh-CN must define ${key}`);
    assert.ok(en.includes(`"${key}"`), `en-US must define ${key}`);
  }
});
