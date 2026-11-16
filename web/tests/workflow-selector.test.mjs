import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import ts from "typescript";
import vm from "node:vm";
import React from "react";
import { renderToString } from "react-dom/server";
import { test } from "vitest";
import { I18nProvider } from "../src/i18n/index.tsx";
import { WorkflowSelector } from "../src/components/task/WorkflowSelector.tsx";
import { WorkflowPickerList } from "../src/components/task/WorkflowPickerDialog.tsx";
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

// ── WorkflowSelector：三态按钮组 ──────────────────────────────────────────
const render = (children) => renderToString(React.createElement(I18nProvider, null, children));
const renderSelector = (props) => render(React.createElement(WorkflowSelector, props));

test("WorkflowSelector renders the three workflow choices in order", () => {
  // 默认收起：只留一个「自动选择」胶囊，不展示任何解释文案。
  const htmlCollapsed = renderSelector({ choice: "empty", versionId: "", onChange: () => {} });
  assert.match(htmlCollapsed, /data-testid="workflow-selector"/);
  assert.match(htmlCollapsed, /data-testid="workflow-show-advanced"/);
  assert.match(htmlCollapsed, /自动选择|Auto select/);
  assert.equal((htmlCollapsed.match(/role="radio"/g) || []).length, 0);
  assert.doesNotMatch(htmlCollapsed, /AI 会根据需求自动选择|Advanced options|高级选项/);

  // 非empty时会自动展开，显示三个选项
  const htmlExpanded = renderSelector({ choice: "specified", versionId: "wf-v1", onChange: () => {} });
  assert.match(htmlExpanded, /data-testid="workflow-advanced-panel"/);
  assert.equal((htmlExpanded.match(/type="radio"/g) || []).length, 3);
  assert.deepEqual([...htmlExpanded.matchAll(/data-choice="([^"]+)"/g)].map((m) => m[1]), [
    "empty",
    "specified",
    "disabled",
  ]);
  // 触发器只写友好名；回显里不允许出现内部版本 id。
  assert.match(htmlExpanded, /指定工作流|Specify workflow/);
  assert.doesNotMatch(htmlExpanded, /wf-v1/);
});

test("WorkflowSelector marks the active choice", () => {
  // 新UI：只有非empty时才展开显示radio按钮
  // empty状态下不显示radio，所以只测试specified和disabled
  for (const choice of ["specified", "disabled"]) {
    const html = renderSelector({ choice, versionId: choice === "specified" ? "wf-v1" : "", onChange: () => {} });
    assert.equal((html.match(/checked/g) || []).length, 1, `${choice} must be the only checked radio`);
    // HTML中checked可能在data-choice之前或之后，所以用更灵活的匹配
    assert.match(html, new RegExp(`data-choice="${choice}"[^>]*checked|checked[^>]*data-choice="${choice}"`));
  }

  // empty状态下不显示radio，而是显示默认提示
  const htmlEmpty = renderSelector({ choice: "empty", versionId: "", onChange: () => {} });
  assert.equal((htmlEmpty.match(/type="radio"/g) || []).length, 0);
  assert.match(htmlEmpty, /data-testid="workflow-show-advanced"/);
});

// Node 环境下没有 DOM，点击由浏览器验证；这里钉住每个按钮都接到了 onChange，
// 且「指定工作流」走的是弹窗而不是直接提交空 id。
test("WorkflowSelector wires each radio to its action", () => {
  const source = read("components/task/WorkflowSelector.tsx");
  assert.match(source, /onChange\(["']empty["'],\s*["']["']\)/);
  assert.match(source, /onChange\(["']disabled["'],\s*["']["']\)/);
  assert.match(source, /onChange\(["']specified["'],\s*[a-zA-Z_$][\w$]*\)/);
  assert.match(source, /setShowPicker\(true\)/);
  assert.match(source, /<WorkflowPickerDialog/);
  // 选定后收起选项面板：选择器保持成一个胶囊，而不是一直展开。
  assert.match(source, /onSelect=\{\(id, name\) => \{[\s\S]*?setOpen\(false\)/);
});

// 选择器回显的是 workflow 名字，不是内部版本 id（wf-feature:v1 这种不该给用户看）。
// 名字只在 id 仍对得上时生效：外部改写 versionId 后回退到通用「指定工作流」，
// 绝不把版本 id 漏到界面上。
test("WorkflowSelector shows the picked workflow name with no version-id fallback", () => {
  const source = read("components/task/WorkflowSelector.tsx");
  assert.match(source, /setPicked\(\{\s*id,\s*name\s*\}\)/);
  assert.match(source, /picked\.id === versionId \? picked\.name : t\("task\.workflowChoice\.manual"\)/);
  assert.doesNotMatch(source, /picked\.id === versionId \? picked\.name : versionId/);
  const dialog = read("components/task/WorkflowPickerDialog.tsx");
  assert.match(dialog, /onSelect\(workflow\.id, workflow\.name \|\| workflow\.id\)/);
});

test("WorkflowSelector keeps the view free of direct service imports", () => {
  for (const file of ["components/task/WorkflowSelector.tsx", "components/task/WorkflowPickerDialog.tsx"]) {
    assert.doesNotMatch(read(file), /from\s*["'](?:\.\.\/)+services\//, `${file} must go through a hook`);
  }
});

// ── WorkflowPickerDialog：列表渲染 ─────────────────────────────────────────
test("WorkflowPickerList renders workflows, tags, and the selected marker", () => {
  const html = render(
    React.createElement(WorkflowPickerList, {
      workflows: [
        { id: "wf-debug", name: "Debug 流程", description: "定位登录问题", use_cases: [], tags: ["bug", "login"] },
        { id: "wf-new", name: "新功能", description: "加功能", use_cases: [], tags: [] },
      ],
      selected: "wf-debug",
      onSelect: () => {},
    }),
  );
  assert.match(html, /Debug 流程/);
  assert.match(html, /定位登录问题/);
  assert.match(html, /bug/);
  assert.match(html, /login/);
  const items = html.split("<li").slice(1);
  const selected = items.filter((item) => item.includes('data-selected="true"'));
  assert.equal(selected.length, 1, "exactly one workflow may be marked selected");
  assert.match(selected[0], /Debug 流程/);
  assert.doesNotMatch(selected[0], /新功能/);
});

test("WorkflowPickerDialog is a modal that loads options through a hook", () => {
  const dialog = read("components/task/WorkflowPickerDialog.tsx");
  assert.match(dialog, /role="dialog"/);
  assert.match(dialog, /aria-modal="true"/);
  assert.match(dialog, /useWorkflowPicker\(/);
  const hook = read("components/task/useWorkflowPicker.ts");
  assert.match(hook, /from\s*["']\.\.\/\.\.\/services\/workflows["']/);
  assert.match(hook, /listAvailableWorkflows\(/);
});

// ── i18n：中英键集不许漂移 ────────────────────────────────────────────────
test("workflow selector i18n keys exist in both locales", () => {
  const keys = [
    "task.workflowChoice",
    "task.workflowChoice.auto",
    "task.workflowChoice.manual",
    "task.workflowChoice.none",
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
  // 选择器只保留「自动选择 / 指定工作流 / 纯对话」。
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
