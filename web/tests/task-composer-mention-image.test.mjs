import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { test } from "vitest";
import { getClipboardImageFiles } from "../src/components/editor/pasteSupport.ts";

// 新建任务面板与任务详情输入框要看齐会话输入框的两件事：
//  1. @ 文件 / # 提示词 / / 命令候选：输入触发词 → 拉候选 → ↑↓/Tab/Enter/Esc；
//  2. 粘贴截图 = 加附件（与「+」按钮同一条链路）。
// 组件层禁止 import services，候选拉取放在 hooks/useComposerCandidates，键盘契约
// 抽在 components/editor/useCandidateKeyboard，两端共用一份。

const SRC = path.resolve(import.meta.dirname, "../src");
const read = (rel) => fs.readFileSync(path.join(SRC, rel), "utf8");

test("getClipboardImageFiles keeps only clipboard image files", () => {
  const image = { name: "image.png", type: "image/png", size: 3 };
  const files = getClipboardImageFiles({
    clipboardData: {
      items: [
        { kind: "file", type: "image/png", getAsFile: () => image },
        { kind: "file", type: "text/plain", getAsFile: () => ({ name: "a.txt" }) },
        { kind: "string", type: "text/plain", getAsFile: () => null },
        { kind: "file", type: "image/jpeg", getAsFile: () => null },
      ],
    },
  });
  assert.deepEqual(files, [image]);
  assert.deepEqual(getClipboardImageFiles({}), []);
  assert.deepEqual(getClipboardImageFiles({ clipboardData: null }), []);
});

test("PromptEditor exposes active-token, keydown and paste to task composers", () => {
  const editor = read("components/editor/PromptEditor.tsx");
  assert.match(editor, /onActiveTokenChange\?: \(token: ActiveToken \| null\) => void/);
  assert.match(editor, /onActiveTokenChange\?\.\(payload\.activeToken\)/);
  assert.match(editor, /onKeyDown=\{onKeyDown\}/);
  assert.match(editor, /onPaste=\{onPaste\}/);
  // 自定义 onEnter 只消费候选，不能顺手吞掉默认发送（返回值 false 继续走发送逻辑）。
  assert.match(editor, /if \(onEnter\?\.\(event\)\) return true;/);
  assert.match(editor, /if \(isComposing\(event\) \|\| event\?\.shiftKey\) return false;/);
});

test("new-task panel wires @ mentions, candidate keyboard and pasted images", () => {
  const panel = read("components/app/TaskInlineEditPanel.tsx");
  assert.match(panel, /onActiveTokenChange=\{setTaskInlineActiveToken\}/);
  assert.match(panel, /onKeyDown=\{handleCandidateKeyDown\}/);
  assert.match(panel, /onEnter=\{handleCandidateEnter\}/);
  assert.match(panel, /onPaste=\{handleTaskInlinePaste\}/);
  assert.match(panel, /appendTaskInlineAttachments\(imageFiles\)/);
  assert.match(panel, /setCandidates: setTaskInlineCandidates/);
  assert.match(panel, /sendDisabled=\{!\(taskInlineEdit\.text\.trim\(\) \|\| taskInlineEdit\.attachments\.length > 0\)\}/);
  // 候选菜单贴着编辑器，而不是相对整个面板定位。
  assert.match(panel, /position: "relative"[\s\S]{0,1500}<TaskInlineCandidateMenu[\s\S]{0,1500}<PromptEditor/);
  assert.match(panel, /appendTaskInlineAttachments: \(files: File\[\]\) => void/);
});

test("task detail input fetches candidates and accepts pasted images", () => {
  const input = read("components/task/ActivityInput.tsx");
  assert.match(input, /useComposerCandidates\(\{/);
  assert.match(input, /agent: DEFAULT_TASK_AGENT/);
  assert.match(input, /<TaskInlineCandidateMenu/);
  assert.match(input, /onActiveTokenChange=\{setActiveToken\}/);
  assert.match(input, /onKeyDown=\{handleCandidateKeyDown\}/);
  assert.match(input, /onEnter=\{handleCandidateEnter\}/);
  assert.match(input, /onPaste=\{handlePaste\}/);
  assert.match(input, /getClipboardImageFiles\(event\)/);
  assert.match(input, /onAttachFiles\(imageFiles\)/);
  assert.match(input, /hasAttachments: attachments\.length > 0/);
  // 候选按任务所属项目 + 节点拉，跨节点任务不会拿到激活节点的候选。
  const view = read("components/task/ConversationView.tsx");
  assert.match(view, /<ActivityInput[\s\S]{0,200}rootId=\{rootId\}[\s\S]{0,120}nodeId=\{nodeId\}/);
});

test("useComposerCandidates mirrors the session candidate fetch contract", () => {
  const hook = read("hooks/useComposerCandidates.ts");
  assert.match(hook, /fetchCandidates\(\{/);
  assert.match(hook, /nodeId,/);
  assert.match(hook, /type: activeToken\.type === "file"/);
  assert.match(hook, /activeToken\.type === "slash" \? agent : undefined/);
  assert.match(hook, /CANDIDATE_FETCH_DEBOUNCE_MS/);
  assert.match(hook, /controller\.abort\(\)/);
});

test("candidate keyboard contract matches the session composer", () => {
  const hook = read("components/editor/useCandidateKeyboard.ts");
  assert.match(hook, /event\.key === "ArrowDown"/);
  assert.match(hook, /event\.key === "ArrowUp"/);
  assert.match(hook, /event\.key === "Tab"/);
  assert.match(hook, /event\.key === "Escape"/);
  assert.match(hook, /applyCandidate\(candidate\)/);
  // 输入法组合回车与 Shift+Enter 不抢。
  assert.match(hook, /isComposing/);
  assert.match(hook, /keyCode === 229/);
  assert.match(hook, /event\?\.shiftKey/);
});

test("pasted clipboard images are normalized before upload", () => {
  const composer = read("app/useTaskComposer.ts");
  assert.match(composer, /files\.map\(buildPendingAttachment\)/);
  assert.match(composer, /setTaskInlineActiveToken\(null\)/);
  const session = read("components/action/useActionBarHandlers.ts");
  assert.match(session, /getClipboardImageFiles\(event\)/);
});

test("App passes the shared attachment append down to the new-task panel", () => {
  const root = read("app/AppRootView.tsx");
  assert.match(root, /appendTaskInlineAttachments,/);
  assert.match(root, /<TaskInlineEditPanel[\s\S]{0,300}appendTaskInlineAttachments=\{appendTaskInlineAttachments\}/);
});
