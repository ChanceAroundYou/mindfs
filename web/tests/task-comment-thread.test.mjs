import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import ts from "typescript";
import vm from "node:vm";
import { test } from "vitest";

test("task-comment-thread", () => {
  const apiSourcePath = path.resolve(import.meta.dirname, "../src/services/task/comments.ts");
  const apiSource = fs.readFileSync(apiSourcePath, "utf8");
  // 显式 appPath/appURL 带 nodeId（WP-02 契约：跨节点必须显式传 nodeId）
  assert.match(apiSource, /appURL\(.*nodeId/s);
  assert.match(apiSource, /workflow_choice/);
  assert.match(apiSource, /comment_id/);
  assert.match(apiSource, /\/api\/tasks\/.*\/comments/);
  // choice 默认 empty，specified 才带 version
  assert.match(apiSource, /"empty"/);
  assert.match(apiSource, /workflow_version_id/);

  const hookPath = path.resolve(import.meta.dirname, "../src/components/task/useCommentThread.ts");
  const hookSource = fs.readFileSync(hookPath, "utf8");
  assert.match(hookSource, /fetchTaskComments/);
  assert.match(hookSource, /postTaskComment/);
  assert.match(hookSource, /nodeId/);

  const compPath = path.resolve(import.meta.dirname, "../src/components/task/CommentThread.tsx");
  const compSource = fs.readFileSync(compPath, "utf8");
  // 三态 choice、user↔agent 时间线、agent 段内嵌下钻
  for (const v of ["specified", "disabled", "empty"]) assert.ok(compSource.includes(v), `missing choice ${v}`);
  assert.match(compSource, /user-message/);
  assert.match(compSource, /agent-segment/);
  assert.match(compSource, /agent-drilldown/);
  assert.match(compSource, /useCommentThread/);

  const panelPath = path.resolve(import.meta.dirname, "../src/components/task/TaskDetailPanel.tsx");
  const panelSource = fs.readFileSync(panelPath, "utf8");
  assert.match(panelSource, /CommentThread/);
  assert.match(panelSource, /rootId=\{task\.root_id\}/);
  assert.match(panelSource, /taskId=\{task\.id\}/);
  assert.match(panelSource, /nodeId=\{nodeId\}/);

  // i18n 双语键契约
  const zh = fs.readFileSync(path.resolve(import.meta.dirname, "../src/i18n/locales/zh-CN-tasks.ts"), "utf8");
  const en = fs.readFileSync(path.resolve(import.meta.dirname, "../src/i18n/locales/en-US-tasks.ts"), "utf8");
  for (const key of ["task.comments.title", "task.comments.send", "task.comments.choiceSpecified", "task.comments.workflowVersion"]) {
    assert.ok(zh.includes(`"${key}"`), `zh missing ${key}`);
    assert.ok(en.includes(`"${key}"`), `en missing ${key}`);
  }

  // 纯函数：newCommentId 唯一性（transpile 后跑）
  const compiled = ts.transpileModule(apiSource, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
  }).outputText;
  // comments.ts 的 import 转译后会 require ../net/base 与 ../net/api，
  // 本用例只验证纯函数 newCommentId，注入桩即可（require 未定义会 ReferenceError）。
  // 类型与 newCommentId 已抽到 shared/taskComments（components 不得 import services，
  // 两边共用同一份定义）；桩按模块路径分派，保证 re-export 拿到真实实现。
  const sharedPath = path.resolve(import.meta.dirname, "../src/shared/taskComments.ts");
  const sharedSource = fs.readFileSync(sharedPath, "utf8");
  const sharedCompiled = ts.transpileModule(sharedSource, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
  }).outputText;
  const sharedSandbox = { exports: {}, module: { exports: {} }, require: () => ({}), crypto: globalThis.crypto };
  vm.runInNewContext(sharedCompiled, sharedSandbox, { filename: sharedPath });
  const requireStub = (id) =>
    id && id.includes("shared/taskComments")
      ? sharedSandbox.exports
      : { appURL: () => "", protectedJSON: async () => ({}) };
  const sandbox = { exports: {}, module: { exports: {} }, require: requireStub, URLSearchParams, crypto: globalThis.crypto };
  vm.runInNewContext(compiled, sandbox, { filename: apiSourcePath });
  const { newCommentId } = sandbox.exports;
  assert.ok(typeof newCommentId === "function");
  assert.notEqual(newCommentId(), newCommentId());
});
