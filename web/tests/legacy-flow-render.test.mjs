import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { test } from "vitest";

// Phase 2：模板时代任务的流水按工作流语法呈现。
// 本仓库的 vitest 是 node 环境（没有 DOM renderer），所以接线用源码契约钉住；
// 纯派生规则由 legacy-task-flow.test.ts 单测覆盖。

const root = path.resolve(import.meta.dirname, "..");
const read = (rel) => fs.readFileSync(path.join(root, rel), "utf8");

test("任务详情用只读投影 + 工作流状态色渲染历史流水", () => {
  const panel = read("src/components/task/TaskDetailPanel.tsx");
  assert.match(panel, /legacyTaskFlow/, "详情必须经 legacyTaskFlow 投影，而不是直接读裸 stage");
  assert.match(panel, /nodeRunStatusColor/, "节点状态点必须与运行视图共用同一套状态色");
  assert.match(panel, /data-legacy-flow-node/, "每个历史节点要带可断言的节点标记");
  assert.match(panel, /data-node-kind=\{node\.kind\}/, "节点类型（agent / human_gate）要参与渲染");
  assert.match(panel, /task\.stage\.agent/, "agent 节点要显示与工作流视图一致的节点类型标签");
});

test("运行视图与历史流水的状态色只有一个来源", () => {
  const row = read("src/components/task/RunNodeRunRow.tsx");
  const model = read("src/components/task/runTimelineModel.ts");
  assert.match(model, /export function nodeRunStatusColor/, "共享状态色必须定义在纯模型里");
  assert.match(row, /nodeRunStatusColor/, "运行视图必须复用共享状态色");
  assert.doesNotMatch(row, /function statusColor/, "运行视图不得再自己定义一份状态色");
});

test("模板来源按胶囊呈现，不再是一段裸文本", () => {
  const panel = read("src/components/task/TaskDetailPanel.tsx");
  assert.match(panel, /task\.legacyFlowFromTemplate/, "模板来源要走 i18n 胶囊文案");
  for (const locale of ["src/i18n/locales/zh-CN-tasks.ts", "src/i18n/locales/en-US-tasks.ts"]) {
    const src = read(locale);
    assert.match(src, /"task\.legacyFlowFromTemplate":/, `${locale} 缺 task.legacyFlowFromTemplate`);
  }
});

test("历史流水仍可编辑/运行，投影只替换外观", () => {
  const panel = read("src/components/task/TaskDetailPanel.tsx");
  assert.match(panel, /<StageEditor/, "阶段编辑器必须保留 —— 历史任务仍由 legacy 引擎推进");
  assert.match(panel, /onClick=\{\(\) => void runStage\(\)\}/, "「立即执行」入口必须保留");
});
