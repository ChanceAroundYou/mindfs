import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { test } from "vitest";

// 阶段模型已整体替换为对话 + WorkflowRun：历史任务在迁移时转成 activities，
// 前端不再保留只读的「历史阶段视图」。这里钉住「没有兼容层」这件事。
// 本仓库的 vitest 是 node 环境（没有 DOM renderer），所以用源码契约断言。

const root = path.resolve(import.meta.dirname, "..");
const read = (rel) => fs.readFileSync(path.join(root, rel), "utf8");

test("历史任务与普通任务走同一个对话视图，不再有阶段分流", () => {
  const panel = read("src/components/task/TaskDetailPanel.tsx");
  assert.match(panel, /<ConversationView/, "详情面板必须渲染对话视图");
  assert.doesNotMatch(panel, /LegacyTaskView/, "LegacyTaskView 已删除，不能留兼容分支");
  assert.doesNotMatch(panel, /hasLegacyStages/, "详情面板不得再按阶段快照分流");
  assert.doesNotMatch(panel, /latestStageRun/, "详情面板不再读取 stage_runs");
});

test("运行视图与历史流水的状态色只有一个来源", () => {
  const row = read("src/components/task/RunNodeRunRow.tsx");
  const model = read("src/components/task/runTimelineModel.ts");
  assert.match(model, /export function nodeRunStatusColor/, "共享状态色必须定义在纯模型里");
  assert.match(row, /nodeRunStatusColor/, "运行视图必须复用共享状态色");
  assert.doesNotMatch(row, /function statusColor/, "运行视图不得再自己定义一份状态色");
});

test("模板来源按胶囊呈现，不再是裸文本", () => {
  const panel = read("src/components/task/TaskDetailPanel.tsx");
  assert.match(panel, /task\.legacyFlowFromTemplate/, "模板来源要走 i18n 胶囊文案");
  for (const locale of ["src/i18n/locales/zh-CN-tasks.ts", "src/i18n/locales/en-US-tasks.ts"]) {
    const src = read(locale);
    assert.match(src, /"task\.legacyFlowFromTemplate":/, `${locale} 缺 task.legacyFlowFromTemplate`);
  }
});

test("详情面板不再内联阶段编辑器或阶段执行键", () => {
  const panel = read("src/components/task/TaskDetailPanel.tsx");
  assert.doesNotMatch(panel, /<StageEditor/, "详情面板不再内联阶段编辑器");
  assert.doesNotMatch(panel, /runStage\(\)/, "详情面板不再有阶段执行入口");
});