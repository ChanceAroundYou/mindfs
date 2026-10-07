import assert from "node:assert/strict";
import test from "node:test";
import fs from "node:fs";
import path from "node:path";

const root = path.resolve(import.meta.dirname, "..");
const promptEditor = fs.readFileSync(path.join(root, "src/components/PromptEditor.tsx"), "utf8");
const panel = fs.readFileSync(path.join(root, "src/components/TaskDetailPanel.tsx"), "utf8");
const stageEditor = fs.readFileSync(path.join(root, "src/components/StageEditor.tsx"), "utf8");
const composer = fs.readFileSync(path.join(root, "src/components/action/composerStyles.tsx"), "utf8");
const composerHeight = fs.readFileSync(path.join(root, "src/components/action/useComposerEditorHeight.ts"), "utf8");
const tasks = fs.readFileSync(path.join(root, "src/services/tasks.ts"), "utf8");
const app = fs.readFileSync(path.join(root, "src/App.tsx"), "utf8");
const appTask = fs.readFileSync(path.join(root, "src/app/appTask.ts"), "utf8");
const zh = fs.readFileSync(path.join(root, "src/i18n/locales/zh-CN.ts"), "utf8");
const en = fs.readFileSync(path.join(root, "src/i18n/locales/en-US.ts"), "utf8");

// 文字铺满整宽：单行时右侧给控件留位，多行时贴边只在底部让高度。
// 这些数字以前在 ActionBar 与 PromptEditor 各抄一份，现在统一由 useComposerEditorHeight 给出——
// 两边共用同一 hook 与同一组 inset，改一处不会只改一半。
assert.match(
  promptEditor,
  /composerEditorInsets\(isMultiLine, 96\)/,
  "PromptEditor must take its insets from the shared composerEditorInsets helper",
);
assert.match(
  promptEditor,
  /useComposerEditorHeight\(editorRef\)/,
  "PromptEditor must share the wrap-detection hook with ActionBar",
);
assert.match(
  composerHeight,
  /rightInset: isMultiLine \? MULTI_LINE_RIGHT_INSET : singleLineRightInset/,
  "the shared helper must widen the text column once it wraps",
);
assert.match(
  composerHeight,
  /bottomInset: isMultiLine \? MULTI_LINE_BOTTOM_INSET : SINGLE_LINE_BOTTOM_INSET/,
  "the shared helper must reserve toolbar height at the bottom when multi-line",
);
// ActionBar 也必须走同一个 hook，否则"共用"只是嘴上说说
const actionBar = fs.readFileSync(path.join(root, "src/components/ActionBar.tsx"), "utf8");
assert.match(actionBar, /useComposerEditorHeight\(editorRef\)/, "ActionBar must share the wrap-detection hook too");
assert.match(actionBar, /composerEditorInsets\(/, "ActionBar must take its insets from the shared helper too");

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
// 兜底默认是 claude（DEFAULT_TASK_AGENT），不是 codex。
assert.match(
  stageEditor,
  /agent=\{stage\.agent \|\| DEFAULT_TASK_AGENT\}/,
  "StageEditor must show each stage's own agent (not a panel-wide default)",
);
assert.doesNotMatch(
  stageEditor,
  /agent=\{stage\.agent \|\| "codex"\}/,
  "StageEditor must not fall back to codex when a stage has no agent",
);
assert.match(
  stageEditor,
  /model=\{stage\.model \|\| ""\}/,
  "StageEditor must show each stage's own model when not editing",
);
assert.match(
  stageEditor,
  /effort=\{stage\.effort \|\| ""\}/,
  "StageEditor must show each stage's own effort when not editing",
);

// 未执行阶段可删除；已执行的不给删除入口。
// TaskDetailPanel 已归入 components/task/（见 tests/source-map.mjs），composerStyles 在
// components/action/，所以新路径是 ../action/composerStyles。
assert.match(panel, /import \{[^}]*TrashIcon[^}]*\} from "\.\.\/action\/composerStyles"/, "delete icon must come from the shared composerStyles set");
assert.match(panel, /\{!executed && !isCurrent \? \(/, "TaskDetailPanel must offer delete only on unexecuted, non-current stages");
assert.match(panel, /removeTaskStage\(task\.root_id, task\.id, index, nodeId\)/, "TaskDetailPanel must call removeTaskStage");
assert.match(panel, /requestRemoveStage\(index\)/, "delete button must go through the confirm flow");
assert.match(tasks, /\/api\/tasks\/\$\{encodeURIComponent\(taskId\)\}\/remove-stage/, "tasks.ts must POST to the remove-stage endpoint");

// 回车 = 发送（复用 PromptEditor 的输入框都得有，否则只能点按钮）。
// 曾经 PromptEditor 不接 onEnter，TokenEditor 插入换行 —— 面板里回车永远发不出去。
assert.match(
  promptEditor,
  /onEnter\?: \(event: KeyboardEvent \| null\) => boolean;/,
  "PromptEditor must accept an onEnter override",
);
assert.match(
  promptEditor,
  /onEnter=\{handleEnter\}/,
  "PromptEditor must forward Enter handling to TokenEditor",
);
assert.match(
  promptEditor,
  /if \(isComposing\(event\) \|\| event\?\.shiftKey\) return false;/,
  "Enter must pass through while composing (IME) and on Shift+Enter",
);
assert.match(
  promptEditor,
  /if \(canSend\) onSend\?\.\(\);/,
  "Enter must send when the editor is editable and has text",
);

// setText 不得无条件抢焦点：常驻挂载的编辑器（如工作台快速发起）一挂载就把整页焦点夺走。
const tokenEditor = fs.readFileSync(path.join(root, "src/components/editor/TokenEditor.tsx"), "utf8");
assert.match(
  tokenEditor,
  /const keepFocus = !!rootRef\.current && rootRef\.current\.contains\(document\.activeElement\);/,
  "setText must only re-focus when the editor already holds focus",
);
assert.match(
  app,
  /taskInlineEditorRef\.current\?\.setText\([\s\S]{0,120}?taskInlineEditorRef\.current\?\.focus\(\)/,
  "the create dialog must focus its editor explicitly now that setText no longer steals focus",
);

// 新建任务标题不该再塞模板名：模板名已经由标题右侧的下拉框显示，重复一遍看着像两处设置。
assert.match(
  zh,
  /"task\.createDialogTitle": "创建任务"/,
  "the create dialog title must be plain 「创建任务」",
);
assert.doesNotMatch(
  zh,
  /"task\.createDialogTitle": "创建\{name\}任务"/,
  "the create dialog title must not repeat the template name",
);
assert.match(
  en,
  /"task\.createDialogTitle": "Create task"/,
  "en-US create dialog title must match",
);
assert.doesNotMatch(
  en,
  /"task\.createDialogTitle": "Create \{name\} task"/,
  "en-US create dialog title must not repeat the template name",
);
// 任务小卡片的编辑按钮和它开的编辑态弹窗都删了：面板只剩「新建」，
// 所以 editDialogTitle / edit / editFailed / detailNotSynced 全部没有引用了。
for (const [dict, name] of [[zh, "zh-CN"], [en, "en-US"]]) {
  for (const key of ["task.editDialogTitle", "task.edit", "task.editFailed", "task.detailNotSynced"]) {
    assert.doesNotMatch(
      dict,
      new RegExp(`"${key.replace(/\./g, "\\.")}":`),
      `${name} must not keep the dead ${key} key after the task edit dialog was removed`,
    );
  }
}
assert.doesNotMatch(
  app,
  /openTaskEditDialog/,
  "the task edit dialog opener must be gone, not just its button",
);
assert.doesNotMatch(
  app,
  /taskInlineEdit\.taskId/,
  "the inline task dialog is create-only; the taskId branch must be gone",
);

// 「任务输入」三处说法统一成同一个词。
assert.match(zh, /"task\.initialInput": "任务输入"/, "zh-CN must label the first stage 任务输入");
assert.doesNotMatch(zh, /"task\.initialInput": "任务初始输入"/, "zh-CN must drop the 任务初始输入 wording");
assert.match(en, /"task\.initialInput": "Task input"/, "en-US must label the first stage Task input");

// 新增阶段：复制上一段的 agent/model，模板面板和任务详情都走同一个函数。
const templateDialog = fs.readFileSync(path.join(root, "src/components/TaskTemplateDialog.tsx"), "utf8");
assert.match(
  appTask,
  /export const DEFAULT_TASK_AGENT = "claude"/,
  "new agent stages must default to claude, not codex",
);
assert.match(
  appTask,
  /export const DEFAULT_TASK_MODEL = "sonnet"/,
  "new agent stages must default to sonnet",
);
assert.match(
  appTask,
  /export function inheritAgentStage\(stages: StageTemplate\[\], fromIndex: number\)/,
  "inheritAgentStage copies the closest previous agent stage",
);
assert.match(
  templateDialog,
  /snapshot: \{ \.\.\.blankAgentStage\(prev\.stages\[prev\.stages\.length - 1\]\?\.snapshot\)/,
  "template addStage must inherit the last stage's agent/model",
);
assert.match(
  panel,
  /const stage = inheritAgentStage\(stages, stages\.length\);/,
  "task detail addStage must inherit the last stage's agent/model",
);
// 新增完直接落在编辑态，省得再点一次铅笔。
assert.match(
  panel,
  /setEditingStage\(\(next\.task\.stages\?\.length \|\| 1\) - 1\);/,
  "a freshly added stage must open in edit mode",
);
// 两张面板都不许再留 codex 兜底。
for (const [src, name] of [[panel, "TaskDetailPanel"], [templateDialog, "TaskTemplateDialog"], [app, "App"]]) {
  assert.doesNotMatch(
    src,
    /editAgent[^\n]*"codex"|agent: "codex"|\|\| "codex"/,
    `${name} must not fall back to codex`,
  );
}

// 详情面板的「立即执行」：长在**下一个未执行的 agent 段**那一行，跟删除键并排贴右，
// 跟看板卡片上那个按钮同一个图标、同一个绿。
// 钉死「只给一个、且是 current 之后第一个」是必须的 —— 服务端 run-now 是任务级的，
// 只读 task.current_stage_index，请求里的 stage_index 根本不参与（service.go:770）。
// 长在指针所在的段上就是假动作（按钮在没跑过的卡上、动作却是「跳过这张卡」），
// 每段都发一个更是假的。
assert.match(
  appTask,
  /export function nextRunnableStageIndex\(detail: TaskDetail, currentStageIndex: number\): number \{/,
  "the run target must be a pure helper in appTask, not inline JSX (so it can be unit tested)",
);
assert.match(
  appTask,
  /for \(let index = currentStageIndex \+ 1; index < stages\.length; index\+\+\) \{[\s\S]{0,400}?stages\[index\]\?\.role !== "agent"[\s\S]{0,300}?return index;/,
  "the run target must be the first unexecuted agent stage AFTER the current one",
);
assert.match(
  panel,
  /const canRunStage = index === runnableStageIndex/,
  "the run button must be gated on that single next-stage index",
);
assert.match(
  panel,
  /!isTerminalKanbanTask\(task\)/,
  "a finished task must not offer the run button",
);
assert.match(
  panel,
  /title=\{t\("task\.runNow"\)\}/,
  "the run button must reuse the existing 立即执行 label",
);
// 跟看板卡片对齐：同一个 RunNowIcon、同一个 accent 绿（runIconButtonStyle）。
assert.match(panel, /<RunNowIcon \/>/, "the run button must use the shared RunNowIcon");
assert.match(
  panel,
  /color: "var\(--accent-color\)",/,
  "the run button must use the same accent green as the kanban card",
);
assert.match(panel, /onClick=\{\(\) => void runStage\(\)\}/, "the run button must go through runStage");
// 位置：右组里、删除键左边，跟阶段名同一行。
assert.match(
  panel,
  /\{canRunStage \? \([\s\S]{0,900}?<RunNowIcon \/>[\s\S]{0,2500}?\{run\?\.session_key \? \([\s\S]{0,2500}?\{!executed && !isCurrent \? \([\s\S]{0,600}?<TrashIcon \/>/,
  "run / jump-to-session / delete must sit in one right-aligned group, run leftmost of the three",
);
assert.match(
  panel,
  /flex: "1 1 auto", minWidth: 0 \}\}>\s*\{!editing \? \(/,
  "the left group must take the free space so the button group is pushed to the row end",
);
// worktree 目录已删**不**顶替运行键（用户定的）：运行键无条件给，点了服务端会记下
// 「worktree 目录已不存在」。
assert.doesNotMatch(
  panel,
  /canRunStage && !worktreeMissing/,
  "a missing worktree must not suppress the run button",
);
// 重建 worktree 的入口**已从面板移除**（2026-10-06 用户要求）：它只在「目录已经不在」
// 时出现，而目录不在就等于已收尾 —— 收完尾的任务没有树可执行也没有树可拆，
// 「重建恢复后再执行」是一句兑现不了的承诺。那个局面该给的是「完成」。
// 服务端端点 /api/tasks/{id}/rebuild-worktree 仍在（脚本/CLI 用），前端不该再调。
assert.doesNotMatch(
  panel,
  /rebuildTaskWorktree|TaskRebuildWorktreeIcon|task\.rebuildWorktree/,
  "已收尾的面板不该再有重建 worktree 的入口",
);
// 取而代之的是两把键：完成（推进键给不出来时的出口）+ 终态任务的删除。
// 两把都必须在 headerRight 那一段里 —— 放阶段行里会对每一段渲染一个。
const headerRightAt = panel.indexOf("headerRight={editingName");
assert.ok(headerRightAt > 0, "TaskDetailPanel must pass a headerRight block");
const headerRightSlice = panel.slice(headerRightAt, headerRightAt + 2600);
assert.match(
  headerRightSlice,
  /\{canCompleteTask \? \([\s\S]{0,700}?onClick=\{\(\) => void completeTask\(\)\}[\s\S]{0,400}?<TaskCompleteIcon \/>/,
  "完成键必须在面板头部，判据是 canCompleteTask",
);
assert.match(
  headerRightSlice,
  /\{terminal \? \([\s\S]{0,500}?onClick=\{\(\) => void deleteTaskCard\(\)\}[\s\S]{0,300}?<DeleteIcon \/>/,
  "删除键只给终态任务，且必须住在面板头部",
);
// 完成 = 推进键给不出来时的出口，与看板卡片同一套门控。待审核的任务不能一个键都没有。
assert.match(
  panel,
  /const canFinishWorktree = task\?\.create_worktree === true\s*\n\s*&& !!task\?\.worktree_path\s*\n\s*&& task\?\.worktree_missing !== true\s*\n\s*&& hasAgentStage;/,
  "the panel must still read the server-derived worktree_missing flag for 收尾",
);
// 当前段 fail/cancelled/rejected 时不许给：服务端 moveRelative 会报错，而 RunNow 的
// waiting_user 分支把错吞掉只回详情（service.go:786）—— 按钮点了什么都不发生。
assert.match(
  appTask,
  /export function canAdvanceFromCurrentStage\(detail: TaskDetail, currentStageIndex: number\): boolean \{/,
  "the advance gate must be a named helper mirroring the server rule",
);
assert.match(
  panel,
  /const canRunStage = index === runnableStageIndex\s*&&\s*advanceable/,
  "the run button must also require the current stage to be advanceable",
);
assert.match(
  app,
  /onRunTask=\{\(task\) => handleMoveKanbanTask\(task, "run-now"\)\}/,
  "the detail panel must reuse App's handleMoveKanbanTask, not call moveTask itself",
);

// ---- 上面是「源码长什么样」，下面是「算出来对不对」：直接跑真函数 ----
// 正则只能钉住结构，钉不住 nextRunnableStageIndex / canAdvanceFromCurrentStage 的分支。
// 这两个是纯函数，仓库有先例（markdownOutline.test.mjs 直接 import 源码跑）。
const { canAdvanceFromCurrentStage, nextRunnableStageIndex } = await import("../src/app/appTask.ts");

const mkStages = (roles) => roles.map((role, i) => ({ name: `S${i}`, role, prompt_template: "p" }));
const mkDetail = (roles, runs) => ({
  task: { id: "t1", root_id: "r", status: "waiting_user", current_stage_index: 0, stages: mkStages(roles) },
  stage_runs: runs.map((r) => ({ stage_index: r[0], role: "user", status: r[1], created_at: `2026-01-01T00:00:0${r[0]}Z` })),
  events: [],
});

test("run button lands on the first unexecuted agent stage after the pointer", () => {
  // 真机 task-22 的形状：指针停在已 success 的第 4 段，下一段从没跑过 → 按钮该在第 5 段。
  const detail = mkDetail(
    ["user", "agent", "agent", "agent", "agent", "agent"],
    [[0, "approved"], [1, "success"], [2, "approved"], [3, "approved"], [4, "success"]],
  );
  assert.equal(nextRunnableStageIndex(detail, 4), 5, "task-22: button belongs on stage 5");
  // 指针回到 0：1~4 全跑过了，所以仍然是第 5 段，不是「第一个 agent 段」。
  assert.equal(nextRunnableStageIndex(detail, 0), 5, "already-run stages are skipped regardless of the pointer");
});

test("run button lands on the first agent stage when nothing after the pointer has run", () => {
  const detail = mkDetail(["user", "agent", "agent"], [[0, "approved"]]);
  assert.equal(nextRunnableStageIndex(detail, 0), 1, "a fresh pointer gets the very first agent stage");
});

test("run button never lands on the pointer's own stage or an already-run one", () => {
  const detail = mkDetail(["user", "agent", "agent", "agent"], [[0, "approved"], [1, "success"], [2, "success"]]);
  assert.equal(
    nextRunnableStageIndex(detail, 2),
    3,
    "the pointer's own stage is skipped even when it never ran — run-now advances, it does not re-run",
  );
  assert.equal(nextRunnableStageIndex(detail, 3), -1, "nothing left to run");
  // 全部跑完 → 没有按钮，而不是退回去给一个已经跑过的段。
  const done = mkDetail(["user", "agent", "agent"], [[0, "approved"], [1, "success"], [2, "success"]]);
  assert.equal(nextRunnableStageIndex(done, 2), -1);
});

test("user stages are skipped when picking the run target", () => {
  // 下一个是 user 段（等输入），按钮不该长在那儿 —— 那是「等你写输入」，不是「可以跑了」。
  const detail = mkDetail(["user", "agent", "user", "agent"], [[0, "approved"], [1, "success"]]);
  assert.equal(nextRunnableStageIndex(detail, 1), 3, "jump over the user stage to the next agent one");
});

test("a pending stage run still counts as unexecuted", () => {
  // 段已建但还没跑（run 存在、status=pending）→ 该给按钮。
  const detail = mkDetail(["user", "agent", "agent"], [[0, "approved"], [1, "success"], [2, "pending"]]);
  assert.equal(nextRunnableStageIndex(detail, 1), 2);
});

test("advance gate mirrors the server: failed/cancelled current stage blocks the button", () => {
  // 服务端 canAdvanceFromStage 对 fail/cancelled 返回 false，moveRelative 报错，
  // 而 RunNow 的 waiting_user 分支把错吞掉只回详情 → 按钮点了什么都不发生。宁可不给。
  // rejected 是**允许**的：user 段被否掉也算「你处理过了」，可以往下走（task_store.go:703）。
  for (const status of ["fail", "cancelled"]) {
    const detail = mkDetail(["user", "agent", "agent"], [[0, status], [1, "pending"]]);
    assert.equal(canAdvanceFromCurrentStage(detail, 0), false, `user stage ${status} must not advance`);
  }
  for (const status of ["success", "approved", "pending", "running", "waiting_user", "rejected"]) {
    const detail = mkDetail(["user", "agent", "agent"], [[0, status], [1, "pending"]]);
    assert.equal(canAdvanceFromCurrentStage(detail, 0), true, `user stage ${status} must advance`);
  }
  // agent 段：fail/cancelled/rejected 不许推进，success/approved/running 都算走完了。
  for (const [status, expected] of [["fail", false], ["cancelled", false], ["rejected", false], ["success", true], ["approved", true], ["running", true]]) {
    const detail = mkDetail(["user", "agent", "agent"], [[0, "approved"], [1, status], [2, "pending"]]);
    assert.equal(canAdvanceFromCurrentStage(detail, 1), expected, `agent stage ${status}`);
  }
  // 指针越界 / 段不存在 → 不给按钮（而不是当成可推进）。
  const detail = mkDetail(["user", "agent"], [[0, "approved"]]);
  assert.equal(canAdvanceFromCurrentStage(detail, 9), false);
});

console.log("task-stage-panel.test.mjs: OK");

// ---- 卡片与详情面板的门控必须给同一个答案 ----
// 2026-10-03 实测（mindfs 任务 26）：详情面板按 canAdvanceFromCurrentStage 不给按钮，
// 看板卡片只查 hasLaterStage 就给了「立即执行」；点下去服务端 moveRelative 报错、
// RunNow 又把错吞掉只回未变的详情 —— 按钮看着能点，什么也没发生，任务卡死。
// 卡片侧补 canAdvanceCard 就是为了让两边同口径（判据都是 current_stage_status）。
const { canAdvanceCard } = await import("../src/app/appTask.ts");

const mkCard = (roles, cur, status, stageStatus) => ({
  id: "t1",
  root_id: "r",
  status,
  current_stage_index: cur,
  current_stage_status: stageStatus,
  stages: mkStages(roles),
});

test("card advance gate agrees with the detail panel on an unreported agent stage", () => {
  // agent 段 waiting_user（没输出 [STAGE-DONE:N]）：两边都必须放行，
  // 否则就回到「一边不给、一边给个死按钮」的分裂。
  const detail = mkDetail(["user", "agent", "agent"], [[0, "approved"], [1, "waiting_user"]]);
  assert.equal(canAdvanceFromCurrentStage(detail, 1), true, "detail panel: waiting_user is advanceable");
  const card = mkCard(["user", "agent", "agent"], 1, "waiting_user", "waiting_user");
  assert.equal(canAdvanceCard(card), true, "card must agree");
});

test("card advance gate still blocks failed/cancelled/rejected agent stages", () => {
  for (const status of ["fail", "cancelled", "rejected"]) {
    const card = mkCard(["user", "agent", "agent"], 1, "waiting_user", status);
    assert.equal(canAdvanceCard(card), false, `agent stage ${status} must not advance on the card`);
    const detail = mkDetail(["user", "agent", "agent"], [[0, "approved"], [1, status]]);
    assert.equal(canAdvanceFromCurrentStage(detail, 1), false, `detail panel must also block ${status}`);
  }
});

test("card advance gate keeps the user-stage rule identical to the panel", () => {
  for (const [status, expected] of [
    ["fail", false], ["cancelled", false],
    ["success", true], ["approved", true], ["pending", true], ["running", true], ["waiting_user", true], ["rejected", true],
  ]) {
    const card = mkCard(["user", "agent", "agent"], 0, "waiting_user", status);
    assert.equal(canAdvanceCard(card), expected, `user stage ${status}`);
    const detail = mkDetail(["user", "agent", "agent"], [[0, status]]);
    assert.equal(canAdvanceFromCurrentStage(detail, 0), expected, `detail panel, user stage ${status}`);
  }
});

test("card advance gate falls back to pending when the server sent no stage status", () => {
  // 老数据 / 派生字段缺失时读作 pending（与服务端零值一致），而不是 undefined 落到 false
  // 把按钮全灭掉。
  const card = mkCard(["user", "agent", "agent"], 0, "waiting_user", undefined);
  assert.equal(canAdvanceCard(card), true);
  // 指针越界 → 不给
  assert.equal(canAdvanceCard(mkCard(["user", "agent"], 9, "waiting_user", "success")), false);
});

// 详情面板的「完成」判据 = 推进键给不出来时的出口，与看板卡片 canComplete 同口径。
// 旧口径「没有下一段才给」漏掉了一类任务（2026-10-06 用户实测）：指针停在收尾段、
// 卡在待审核、worktree 又已被拆 —— 收尾键给不出来、执行键也给不出来，
// 任务在界面上彻底没有出路。现在只要推进键（canRunStageNow）给不出来就兜住。
assert.match(
  panel,
  /const canCompleteTask = !terminal && !stageRunningNow && !canRunStageNow && !\(finishActive && canFinishWorktree\);/,
  "the detail panel's 完成 must兜住 canRunStageNow 给不出来的一切局面",
);
assert.match(
  panel,
  /\{canCompleteTask \? \([\s\S]{0,400}?onClick=\{\(\) => void completeTask\(\)\}[\s\S]{0,300}?<TaskCompleteIcon \/>/,
  "完成 must be rendered with the same icon the board card uses",
);
