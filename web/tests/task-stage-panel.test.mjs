import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";

const root = path.resolve(import.meta.dirname, "..");
const promptEditor = fs.readFileSync(path.join(root, "src/components/PromptEditor.tsx"), "utf8");
const panel = fs.readFileSync(path.join(root, "src/components/TaskDetailPanel.tsx"), "utf8");
const composer = fs.readFileSync(path.join(root, "src/components/composerStyles.tsx"), "utf8");
const tasks = fs.readFileSync(path.join(root, "src/services/tasks.ts"), "utf8");

// 文字铺满整宽：单行时右侧给控件留位，多行时贴边只在底部让高度（与 ActionBar/TokenEditor 同款）。
assert.match(
  promptEditor,
  /rightInset=\{isMultiLine \? 14 : 96\}/,
  "PromptEditor must widen the text column once it wraps (single-line only reserves the right toolbar)",
);
assert.match(
  promptEditor,
  /bottomInset=\{isMultiLine \? 44 : 12\}/,
  "PromptEditor must reserve toolbar height at the bottom when multi-line",
);
assert.match(
  promptEditor,
  /const \[isMultiLine, setIsMultiLine\] = useState\(false\)/,
  "PromptEditor must track multi-line state",
);

// + 号只在能新增文件时出现，不做不可用的常显按钮。
assert.match(promptEditor, /\{onAttach \? \(/, "PromptEditor must render the + button only when a file picker is wired");
assert.doesNotMatch(
  promptEditor,
  /disabled=\{!editable \|\| sending \|\| !onAttach\}/,
  "PromptEditor must not render a disabled + button",
);

// 禁用态灰底：不能用未定义的 CSS 变量（button-bg 全仓库无定义 → 实际透明，看不出「不可按」）。
assert.doesNotMatch(
  composer,
  /var\(--button-bg\)/,
  "composerStyles must not reference the undefined button-bg variable",
);

// 非编辑态必须显示该阶段自身的 agent/model —— 曾经一律传打开面板时的草稿，
// 结果所有阶段卡（含已执行的 claude/sonnet 卡）都显示 codex 无模型。
assert.match(
  panel,
  /agent=\{editing \? editAgent : \(stage\.agent \|\| "codex"\)\}/,
  "TaskDetailPanel must show each stage's own agent when not editing",
);
assert.match(
  panel,
  /model=\{editing \? editModel : \(stage\.model \|\| ""\)\}/,
  "TaskDetailPanel must show each stage's own model when not editing",
);
assert.match(
  panel,
  /effort=\{editing \? editEffort : \(stage\.effort \|\| ""\)\}/,
  "TaskDetailPanel must show each stage's own effort when not editing",
);

// 未执行阶段可删除；已执行的不给删除入口。
assert.match(panel, /import \{ PencilIcon, TrashIcon \} from "\.\/composerStyles"/, "delete icon must come from the shared composerStyles set");
assert.match(panel, /\{!executed && !isCurrent \? \(/, "TaskDetailPanel must offer delete only on unexecuted, non-current stages");
assert.match(panel, /removeTaskStage\(task\.root_id, task\.id, index, nodeId\)/, "TaskDetailPanel must call removeTaskStage");
assert.match(panel, /requestRemoveStage\(index\)/, "delete button must go through the confirm flow");
assert.match(tasks, /\/api\/tasks\/\$\{encodeURIComponent\(taskId\)\}\/remove-stage/, "tasks.ts must POST to the remove-stage endpoint");

console.log("task-stage-panel.test.mjs: OK");
