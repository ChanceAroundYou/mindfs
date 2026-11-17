import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import React from "react";
import { renderToString } from "react-dom/server";
import { test } from "vitest";
import { I18nProvider } from "../src/i18n/index.tsx";
import { ActivityInput } from "../src/components/task/ActivityInput.tsx";

// 任务详情面板的输入框与新建任务对话框必须长同一副样子：同源 PromptEditor、
// 同一个添加文件按钮、同一条待发送附件条；工作流选择器是编辑器控件行里的图标按钮。
// 这些断言钉住「两处没有各写一套」。

const SRC = path.resolve(import.meta.dirname, "../src");
const read = (rel) => fs.readFileSync(path.join(SRC, rel), "utf8");
const render = (node) => renderToString(React.createElement(I18nProvider, null, node));

const inputProps = {
  body: "",
  onBodyChange: () => {},
  choice: "empty",
  versionId: "",
  onWorkflowChange: () => {},
  onSend: () => {},
};

test("ActivityInput carries the same add-file button and attachment strip as the new-task panel", () => {
  const html = render(
    React.createElement(ActivityInput, {
      ...inputProps,
      body: "看一下这个",
      attachments: [
        { id: "a1", file: { name: "pic.png" }, isImage: true, previewUrl: "blob:preview" },
        { id: "a2", file: { name: "notes.txt" }, isImage: false },
      ],
      onAttachFiles: () => {},
      onRemoveAttachment: () => {},
    }),
  );
  // 添加文件按钮（PromptEditor 的 ＋）与隐藏 file input 都在。
  assert.match(html, /task.addAttachment|添加附件|Add attachment/);
  assert.match(html, /data-testid="activity-attachment-input"/);
  assert.match(html, /data-testid="composer-attachments"/);
  assert.match(html, /pic\.png/);
  assert.match(html, /notes\.txt/);
  assert.match(html, /blob:preview/);
  // 工作流选择器是编辑器右下角控件行里的图标按钮，不再另起一行。
  assert.match(
    html,
    /data-testid="activity-input"[\s\S]*data-testid="workflow-selector-trigger"[\s\S]*data-testid="activity-send"/,
  );
});

test("ActivityInput omits the add-file control without an attach handler", () => {
  const html = render(React.createElement(ActivityInput, inputProps));
  assert.doesNotMatch(html, /data-testid="activity-attachment-input"/);
  assert.doesNotMatch(html, /data-testid="composer-attachments"/);
});

test("ConversationView sends attachments through the activity hook", () => {
  const view = read("components/task/ConversationView.tsx");
  assert.match(view, /usePendingAttachments\(\)/);
  assert.match(view, /send\(text, choice, versionId, pendingAttachments\)/);
  assert.match(view, /clearPendingAttachments\(\)/);
  const hook = read("hooks/useTaskActivities.ts");
  assert.match(hook, /uploadFiles\(/);
  assert.match(hook, /formatFileToken\(fileTokenPath\(file\)\)/);
  // 用户取消上传不是错误，不能写红色错误条。
  assert.match(hook, /if \(!isUploadAbortError\(err\)\) setSendError/);
});

test("ActivityInput keeps the workflow selector and attachments inside the editor composer", () => {
  const input = read("components/task/ActivityInput.tsx");
  assert.match(input, /extraControls=\{/);
  assert.match(input, /onAttach=\{onAttachFiles \? \(\) => fileInputRef\.current\?\.click\(\) : undefined\}/);
  assert.match(input, /<ComposerAttachmentStrip/);
});

test("the same attachment strip is used by the new-task panel", () => {
  const panel = read("components/app/TaskInlineEditPanel.tsx");
  assert.match(panel, /<ComposerAttachmentStrip/);
  // 不再各自写一份 54px 缩略图/胶囊标记。
  assert.doesNotMatch(panel, /width:\s*"54px"/);
  assert.match(panel, /uploadFiles|handleTaskInlineAttachmentChange/);
  const strip = read("components/editor/ComposerAttachmentStrip.tsx");
  assert.match(strip, /CompactUploadProgress/);
  assert.match(strip, /task.removeAttachment/);
});