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

test("recommendWorkflow posts user_input and returns the recommendation", async () => {
  const { service, calls } = loadWorkflowService({
    recommended: { workflow_id: "wf-debug", reason: "matches", match_score: 0.9 },
  });
  const rec = await service.recommendWorkflow("  登录报错了  ");
  assert.equal(calls[0].path, "/api/workflows/recommend");
  const request = calls[1];
  assert.equal(request.url, "/page/api/workflows/recommend");
  assert.equal(String(request.init.method).toUpperCase(), "POST");
  assert.equal(request.init.headers["Content-Type"], "application/json");
  assert.deepEqual(JSON.parse(request.init.body), { user_input: "登录报错了" });
  // vm 里造出来的对象原型来自另一个 realm，deepEqual 的原型检查会判「结构相同但不相等」。
  assert.equal(JSON.stringify(rec), JSON.stringify({ workflow_id: "wf-debug", reason: "matches", match_score: 0.9 }));
});

test("recommendWorkflow rejects an empty input and a malformed response", async () => {
  const { service } = loadWorkflowService({});
  await assert.rejects(() => service.recommendWorkflow("   "), /user_input required/);
  await assert.rejects(() => service.recommendWorkflow("hello"), /recommendation/i);
});

// ── WorkflowSelector：三态按钮组 + 推荐芯片 ────────────────────────────────
const render = (children) => renderToString(React.createElement(I18nProvider, null, children));
const renderSelector = (props) => render(React.createElement(WorkflowSelector, props));

test("WorkflowSelector renders the three workflow choices in order", () => {
  const html = renderSelector({ choice: "empty", versionId: "", onChange: () => {} });
  assert.match(html, /data-testid="workflow-selector"/);
  assert.equal((html.match(/role="radio"/g) || []).length, 3);
  assert.deepEqual([...html.matchAll(/data-choice="([^"]+)"/g)].map((m) => m[1]), [
    "empty",
    "specified",
    "disabled",
  ]);
  assert.match(html, /aria-checked="true"[^>]*data-choice="empty"/);
});

test("WorkflowSelector marks the active choice", () => {
  for (const choice of ["empty", "specified", "disabled"]) {
    const html = renderSelector({ choice, versionId: "", onChange: () => {} });
    assert.equal((html.match(/aria-checked="true"/g) || []).length, 1, `${choice} must be the only checked radio`);
    assert.match(html, new RegExp(`aria-checked="true"[^>]*data-choice="${choice}"`));
  }
});

test("WorkflowSelector shows the recommendation chip", () => {
  const rec = { workflow_id: "wf-debug", reason: "matches", match_score: 0.9 };
  const html = renderSelector({ choice: "empty", versionId: "", recommendation: rec, onChange: () => {} });
  // 断言参数化后的结果，不依赖宿主机语言（Node 的 navigator.language 通常是 en-US）。
  assert.match(html, /(?:推荐: wf-debug|Recommended: wf-debug)/);
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
    "task.workflowChoice.empty",
    "task.workflowChoice.specified",
    "task.workflowChoice.disabled",
    "task.workflowRecommended",
    "task.selectWorkflow",
    "task.noAvailableWorkflows",
  ];
  for (const key of keys) {
    assert.equal(typeof zhCN[key], "string", `zh-CN missing ${key}`);
    assert.equal(typeof enUS[key], "string", `en-US missing ${key}`);
    assert.ok(zhCN[key].trim(), `zh-CN empty ${key}`);
    assert.ok(enUS[key].trim(), `en-US empty ${key}`);
  }
  assert.match(zhCN["task.workflowRecommended"], /\{name\}/, "recommendation label must be parameterized");
  assert.match(enUS["task.workflowRecommended"], /\{name\}/, "recommendation label must be parameterized");
});