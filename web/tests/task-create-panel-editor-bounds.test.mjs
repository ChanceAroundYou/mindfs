import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { test } from "vitest";

// 新建任务面板里两处「看着不像 MindFS」的地方（2026-10-26 用户实测）：
//
// ① 任务文本一长，滚不到底、点不到发送键。
//    根因是 PromptEditor 给 TokenEditor 传了 fillHeight —— 那条路径把 contentEditable
//    设成 height:100% / maxHeight:none，高度随内容无限长高；composerContainerStyle
//    只有 minHeight 没有 max-height，发送键又是 absolute + bottom:6 挂在容器上，
//    于是它跟着容器一起沉到滚动视口之外。删掉 fillHeight 就走回默认路径：
//    maxHeight 240px + 编辑器内部滚动，与对话输入框（ActionBar）同一套。
//
// ② 「新分支名称」是裸 input，还悬在正文容器外面。现在与任务名输入框同源
//    （composerInputStyle、26px），并排放在分支选择器那一行。

test("task-create-panel-editor-bounds", async () => {
const root = path.resolve(import.meta.dirname, "..");
const read = (rel) => fs.readFileSync(path.join(root, rel), "utf8");

const promptEditor = read("src/components/editor/PromptEditor.tsx");
const tokenEditor = read("src/components/editor/TokenEditor.tsx");
const inlinePanel = read("src/components/app/TaskInlineEditPanel.tsx");

// ── ① 编辑器高度必须有上界 ──────────────────────────────────────────
// 切片从 JSX 那一处开始：文件开头还有 `forwardRef<TokenEditorHandle, …>`，
// 泛泛找 "<TokenEditor" 会切到类型参数上，断言就变成看整个文件了。
const tokenEditorCall = promptEditor.slice(promptEditor.indexOf("<TokenEditor\n"));
assert.ok(tokenEditorCall.startsWith("<TokenEditor"), "PromptEditor must still render a TokenEditor");
assert.doesNotMatch(
  tokenEditorCall,
  /fillHeight/,
  "PromptEditor must not pass fillHeight: that path has no max height, so the absolute send button scrolls out of reach",
);
// 默认路径必须是「有上界 + 自己滚」—— 光删 fillHeight 而默认路径也无界的话没修好。
assert.match(
  tokenEditor,
  /maxHeight:\s*fillHeight\s*\?\s*"none"\s*:\s*"240px"/,
  "TokenEditor's non-fillHeight path must cap its height at 240px",
);
assert.match(
  tokenEditor,
  /overflowY:\s*"auto"/,
  "TokenEditor's default path must scroll internally",
);

// ── ② 分支名输入框与任务名同源 ──────────────────────────────────────
const branchInput = inlinePanel.slice(
  inlinePanel.indexOf('aria-label={t("task.newBranchName")}') - 400,
  inlinePanel.indexOf('aria-label={t("task.newBranchName")}') + 600,
);
assert.match(branchInput, /composerInputStyle/, "the new-branch input must reuse composerInputStyle");
assert.match(branchInput, /height:\s*"26px"/, "and match the task-name input's 26px height");
// 曾经的形态：margin 撑出来的裸 input，落在正文容器（padding:12 的 div）里，
// 与分支选择器分属两行。它必须已经不在那句 padding div 之后。
const bodyStart = inlinePanel.indexOf('<div style={{ padding: "12px", overflow: "visible"');
assert.ok(bodyStart > 0, "the panel body div should still exist");
assert.doesNotMatch(
  inlinePanel.slice(bodyStart),
  /task\.newBranchName/,
  "the branch-name input must live in the worktree controls row, not in the body after it",
);
// 与分支选择器同一行：两者之间不应再夹着别的容器边界。
const selectorIndex = inlinePanel.indexOf("<WorktreeBranchSelector");
assert.ok(selectorIndex > 0 && selectorIndex < inlinePanel.indexOf('aria-label={t("task.newBranchName")}'),
  "the branch-name input must follow the branch selector in the same row");
});
