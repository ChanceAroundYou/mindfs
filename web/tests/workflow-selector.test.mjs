import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import ts from "typescript";
import vm from "node:vm";
import React from "react";
import { renderToString } from "react-dom/server";
import { test } from "vitest";
import { I18nProvider } from "../src/i18n/index.tsx";
import { WorkflowMenu, WorkflowSelector } from "../src/components/task/WorkflowSelector.tsx";
import { WorkflowOptionList } from "../src/components/task/WorkflowOptionList.tsx";
import { zhCN } from "../src/i18n/locales/zh-CN.ts";
import { enUS } from "../src/i18n/locales/en-US.ts";

const SRC = path.resolve(import.meta.dirname, "../src");
const read = (rel) => fs.readFileSync(path.join(SRC, rel), "utf8");

// ── 服务层：控制面端点 + 回包归一化 ────────────────────────────────────────
// `/api/workflows` 在 ControlPlanePrefixes 里，worker 上必然 403。服务层必须走
// controlPath 打页面服务器；用 appURL 会跟着当前选中节点走，在 worker 上直接坏掉。
function loadWorkflowService(response) {
  const file = path.join(SRC, "services/workflows.ts");
  const compiled = ts.transpileModule(fs.readFileSync(file, "utf8"), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
  }).outputText;
  const calls = [];
  const sandbox = {
    exports: {},
    module: { exports: {} },
    console,
    URLSearchParams,
    require: (name) => {
      if (name.includes("controlPlane")) {
        return {
          controlPath: (p, params) => {
            const query = params ? params.toString() : "";
            calls.push({ kind: "path", path: p, params: query });
            return `/page${p}${query ? `?${query}` : ""}`;
          },
        };
      }
      if (name.includes("api")) {
        return {
          protectedJSON: async (url, init) => {
            calls.push({ kind: "request", url, init });
            return response;
          },
        };
      }
      throw new Error(`unexpected require: ${name}`);
    },
  };
  vm.runInNewContext(compiled, sandbox, { filename: file });
  return { service: sandbox.exports, calls };
}

test("listAvailableWorkflows uses the control plane and normalizes the payload", async () => {
  const { service, calls } = loadWorkflowService({
    workflows: [
      { id: "wf-1", name: "WF 1" },
      { id: "wf-2", name: "WF 2", use_cases: ["debug"], tags: ["bug"], recommended: true, match_score: 0.9 },
    ],
  });

  const items = await service.listAvailableWorkflows();
  assert.equal(calls[0].kind, "path");
  assert.equal(calls[0].path, "/api/workflows/available");
  assert.equal(calls[0].params, "");
  assert.equal(calls[1].url, "/page/api/workflows/available");
  // vm 里的数组原型来自另一个 realm，用 JSON 比避免原型检查误判。
  assert.equal(JSON.stringify(items.map((item) => item.id)), JSON.stringify(["wf-1", "wf-2"]));
  // Go 的 nil slice 编码成 null；组件会读 wf.tags.length，缺字段必须归一成 []。
  assert.equal(JSON.stringify(items[0].use_cases), "[]");
  assert.equal(JSON.stringify(items[0].tags), "[]");
  assert.equal(JSON.stringify(items[1].use_cases), JSON.stringify(["debug"]));
  assert.equal(items[1].recommended, true);
  assert.equal(items[1].match_score, 0.9);
});

test("listAvailableWorkflows forwards a trimmed user_input as a query param", async () => {
  const { service, calls } = loadWorkflowService({ workflows: [] });
  await service.listAvailableWorkflows("  登录报错了  ");
  const params = new URLSearchParams(calls[0].params);
  assert.equal(params.get("user_input"), "登录报错了");
  assert.deepEqual(await service.listAvailableWorkflows("   "), []);
});

// ── WorkflowSelector：图标按钮 + 一级三态 + 二级列表 ───────────────────────
const render = (children) => renderToString(React.createElement(I18nProvider, null, children));
const renderSelector = (props) => render(React.createElement(WorkflowSelector, props));
const renderMenu = (props) =>
  render(React.createElement(WorkflowMenu, {
    pickedName: "",
    manualOpen: false,
    onPickAuto: () => {},
    onPickDisabled: () => {},
    onPickManual: () => {},
    onPickWorkflow: () => {},
    ...props,
  }));

test("WorkflowSelector collapses into a single workflow icon button", () => {
  const html = renderSelector({ choice: "empty", versionId: "", onChange: () => {} });
  assert.match(html, /data-testid="workflow-selector"/);
  assert.match(html, /data-testid="workflow-selector-trigger"/);
  // 收起态只有一个按钮：没有常驻的选项面板，也没有文字胶囊。
  assert.match(html, /aria-haspopup="menu"/);
  assert.doesNotMatch(html, /data-testid="workflow-menu"/);
  assert.doesNotMatch(html, /role="radio"/);
  // 图标代表工作流（分支图），按钮用 aria-label/title 交代当前状态。
  assert.match(html, /<svg/);
  assert.match(html, /执行方式: 自动|Workflow choice: Auto/);
  // 默认（自动）不显示状态圆点。
  assert.doesNotMatch(html, /data-testid="workflow-selection-dot"/);
});

test("WorkflowSelector marks non-default choices with a dot", () => {
  const specified = renderSelector({ choice: "specified", versionId: "wf-v1", onChange: () => {} });
  assert.match(specified, /data-choice="specified"/);
  assert.match(specified, /data-testid="workflow-selection-dot"/);
  assert.match(specified, /执行方式: 手动选择|Workflow choice: Manual/);
  // 回显里不允许出现内部版本 id。
  assert.doesNotMatch(specified, /wf-v1/);

  const disabled = renderSelector({ choice: "disabled", versionId: "", onChange: () => {} });
  assert.match(disabled, /data-choice="disabled"/);
  assert.match(disabled, /data-testid="workflow-selection-dot"/);
  assert.match(disabled, /执行方式: 禁用|Workflow choice: Disabled/);
});

test("WorkflowMenu renders auto / disabled / manual in order", () => {
  const html = renderMenu({ choice: "empty", versionId: "" });
  const items = [...html.matchAll(/data-testid="workflow-choice-([a-z]+)"/g)].map((match) => match[1]);
  assert.deepEqual(items, ["auto", "disabled", "manual"]);
  assert.match(html, /自动|Auto/);
  assert.match(html, /禁用|Disabled/);
  assert.match(html, /手动选择|Manual/);
  // 三个一级选项的选中态只落在当前 choice 上。
  const checked = [...html.matchAll(/aria-checked="true"/g)];
  assert.equal(checked.length, 1);
  assert.match(html, /data-testid="workflow-choice-auto"[^>]*aria-checked="true"|aria-checked="true"[^>]*data-testid="workflow-choice-auto"/);
  // 二级列表默认收起；「手动选择」是展开入口，不直接提交空 id。
  assert.doesNotMatch(html, /data-testid="workflow-manual-panel"/);
  assert.match(html, /aria-expanded="false"/);
});

test("WorkflowMenu expands the manual list and echoes the picked name", () => {
  const html = renderMenu({
    choice: "specified",
    versionId: "wf-debug:v1",
    pickedName: "Debug 流程",
    manualOpen: true,
  });
  assert.match(html, /data-testid="workflow-manual-panel"/);
  assert.match(html, /aria-expanded="true"/);
  assert.match(html, /Debug 流程/);
  // SSR 下列表 hook 只停在加载态；版本 id 不泄露。
  assert.doesNotMatch(html, /wf-debug:v1/);
});

test("WorkflowOptionList renders names, descriptions and the selected marker without ids", () => {
  const html = render(
    React.createElement(WorkflowOptionList, {
      workflows: [
        { id: "wf-debug", name: "Debug 流程", description: "定位登录问题", use_cases: [], tags: [] },
        { id: "wf-new", name: "新功能", description: "", use_cases: [], tags: [] },
      ],
      selected: "wf-debug",
      onSelect: () => {},
    }),
  );
  assert.match(html, /Debug 流程/);
  assert.match(html, /定位登录问题/);
  assert.match(html, /新功能/);
  const items = html.split("<button").slice(1);
  const selected = items.filter((item) => item.includes('aria-checked="true"'));
  assert.equal(selected.length, 1, "exactly one workflow may be marked selected");
  assert.match(selected[0], /Debug 流程/);
  assert.doesNotMatch(html, />wf-debug<|>wf-new</);
});

// Node 环境下没有 DOM，点击由浏览器验证；这里钉住每项接到哪个动作，
// 以及「手动选择」走二级展开而不是直接提交空 id。
test("WorkflowSelector wires each mode to its action", () => {
  const source = read("components/task/WorkflowSelector.tsx");
  assert.match(source, /onChange\(["']empty["'],\s*["']["']\)/);
  assert.match(source, /onChange\(["']disabled["'],\s*["']["']\)/);
  assert.match(source, /onPickManual=\{\(\) => setManualOpen\(/);
  assert.match(source, /onChange\(["']specified["'],\s*id\)/);
  assert.match(source, /useState\(choice === "specified"\)/);
  assert.match(source, /<WorkflowManualPanel/);
  // 菜单 portal 到 body：面板正文是滚动容器，内联绝对定位会被裁掉。
  assert.match(source, /createPortal\(/);
  const hook = read("components/task/useWorkflowSelectorMenu.ts");
  assert.match(hook, /pointerdown/);
  assert.match(hook, /Escape/);
});

// 选择器回显的是 workflow 名字，不是内部版本 id（wf-feature:v1 这种不该给用户看）。
// 名字只在 id 仍对得上时生效：外部改写 versionId 后回退到通用「手动选择」。
test("WorkflowSelector shows the picked workflow name with no version-id fallback", () => {
  const source = read("components/task/WorkflowSelector.tsx");
  assert.match(source, /setPicked\(\{\s*id,\s*name\s*\}\)/);
  assert.match(source, /picked && picked\.id === versionId \? picked\.name : ""/);
  assert.match(source, /pickedName \|\| t\("task\.workflowChoice\.manual"\)/);
  assert.doesNotMatch(source, /\? picked\.name : versionId/);
  const list = read("components/task/WorkflowOptionList.tsx");
  assert.match(list, /onSelect\(workflow\.id, workflow\.name \|\| workflow\.id\)/);
});

test("WorkflowSelector keeps the view free of direct service imports", () => {
  for (const file of [
    "components/task/WorkflowSelector.tsx",
    "components/task/WorkflowOptionList.tsx",
  ]) {
    assert.doesNotMatch(read(file), /from\s*["'](?:\.\.\/)+services\//, `${file} must go through a hook`);
  }
  const hook = read("components/task/useWorkflowPicker.ts");
  assert.match(hook, /from\s*["']\.\.\/\.\.\/services\/workflows["']/);
  assert.match(hook, /listAvailableWorkflows\(/);
});

// ── i18n：中英键集不许漂移 ────────────────────────────────────────────────
test("workflow selector i18n keys exist in both locales", () => {
  const keys = [
    "task.workflowChoice",
    "task.workflowChoice.auto",
    "task.workflowChoice.autoHint",
    "task.workflowChoice.manual",
    "task.workflowChoice.manualHint",
    "task.workflowChoice.none",
    "task.workflowChoice.noneHint",
    "task.selectWorkflow",
    "task.noAvailableWorkflows",
  ];
  for (const key of keys) {
    assert.equal(typeof zhCN[key], "string", `zh-CN missing ${key}`);
    assert.equal(typeof enUS[key], "string", `en-US missing ${key}`);
    assert.ok(zhCN[key].trim(), `zh-CN empty ${key}`);
    assert.ok(enUS[key].trim(), `en-US empty ${key}`);
  }
  // 解释性提示语、重复的三态文案与推荐标记都只剩一种说法：
  // 选择器只保留「自动（默认）/ 禁用 / 手动选择」。
  for (const key of [
    "task.workflowAutoMode",
    "task.workflowAdvancedOptions",
    "task.workflowUseDefault",
    "task.workflowMode",
    "task.workflowChoice.empty",
    "task.workflowChoice.specified",
    "task.workflowChoice.disabled",
    "task.workflowRecommended",
  ]) {
    assert.equal(zhCN[key], undefined, `zh-CN must drop ${key}`);
    assert.equal(enUS[key], undefined, `en-US must drop ${key}`);
  }
});

// ── 移动端避让：定位必须委托量测式 hook，不再自算 layout 视口 ──────────────
test("workflow selector menu delegates placement to the shared measured hook", () => {
  const hook = read("components/task/useWorkflowSelectorMenu.ts");
  // 自算 window.innerHeight/innerWidth 在软键盘弹出时会算错（layout 视口不缩小），
  // 也拿不到「两侧都放不下时内部滚动」的高度上限。
  assert.match(hook, /useAnchoredMenuPosition\(/);
  assert.match(hook, /prefer:\s*"above"/);
  assert.match(hook, /deps:\s*\[manualOpen\]/);
  assert.doesNotMatch(hook, /window\.innerHeight|window\.innerWidth/);
  assert.doesNotMatch(hook, /bottom:\s*/);
  // 位置来自量测结果，未算好前沿用一帧隐藏，避免菜单先闪在 0,0。
  assert.match(hook, /top:\s*position\?\.top/);
  assert.match(hook, /maxHeight:\s*position\?\.maxHeight/);
  assert.match(hook, /visibility:\s*position\s*\?\s*"visible"\s*:\s*"hidden"/);
});

test("workflow menu columns size from the available width, not 100vw math", () => {
  const source = read("components/task/WorkflowSelector.tsx");
  assert.match(source, /flex:\s*"1 1 232px"/);
  assert.doesNotMatch(source, /calc\(100vw - 200px\)/);
  assert.match(source, /flexShrink:\s*0/);
  // 二级列展开会改变菜单宽高，必须把 manualOpen 传进定位 hook 触发重量。
  assert.match(source, /useWorkflowSelectorMenu\(manualOpen\)/);
});