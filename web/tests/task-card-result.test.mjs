// 任务卡必须显示「这一轮的结果」，而不是只显示用户最初的需求。
//
// 用户反馈（2026-11-17）：任务列表里看到的和对话里的结果没区别 —— 因为卡片正文
// 一直是 taskFirstInput（第一条用户留言），Router 结算出的结果只存在于对话里。
// 现在结果摘要由后端写到 task.result_summary，共享卡片负责展示；项目看板与
// 跨项目工作台共用这张卡，所以这一处渲染两边都生效。

import assert from "node:assert/strict";
import React from "react";
import { renderToString } from "react-dom/server";
import { test } from "vitest";
import { I18nProvider } from "../src/i18n/index.tsx";
import { TaskCardRows } from "../src/components/task/TaskCardRows.tsx";

const render = (task) =>
  renderToString(
    React.createElement(
      I18nProvider,
      null,
      React.createElement(TaskCardRows, {
        task: { id: "task-1", root_id: "root-1", task_template_id: "", task_template_name: "", status: "waiting_user", ...task },
        sessionKeys: [],
        sessionByKey: {},
        getNodeColor: () => null,
        onOpenSession: () => {},
        onMove: () => {},
        onShowSessionError: () => {},
      }),
    ),
  );

test("the card renders the settled result summary", () => {
  const html = render({ name: "登录接口", result_summary: "接口已经加好了，测试全绿" });
  assert.match(html, /data-testid="task-card-result"/, "the card must have a result block");
  assert.match(html, /接口已经加好了，测试全绿/, "the result summary must be visible on the card");
});

test("a task without a settled result keeps the card unchanged", () => {
  const html = render({ name: "登录接口" });
  assert.doesNotMatch(html, /data-testid="task-card-result"/, "no empty result block should be rendered");
});