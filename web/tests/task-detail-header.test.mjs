import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "vitest";
import { TASK_DETAIL_ACTION_META, taskDetailActions } from "../src/components/task/taskDetailActions.ts";

// 任务详情 header 的收口：编辑键贴着任务名，右上角**平铺**全部可用动作（含删除），
// 不再有「操作控件」菜单；移动端正文左右留白收窄。这里只钉源码契约与纯函数行为，
// 几何与各状态的实际展示由 Playwright 实测（e2e/test_task_detail_actions.py）。

const read = (p) => readFileSync(new URL(p, import.meta.url), "utf8");

const task = (extra = {}) => ({ status: "waiting_user", create_worktree: false, ...extra });

test("header 结构：名字旁边是编辑键，动作平铺在右侧，关闭是 opt-in", () => {
  const panel = read("../src/components/task/TaskDetailPanel.tsx");
  const shell = read("../src/components/shell/PanelShell.tsx");

  // 标题槽：名字按内容收缩（不抢满整行），编辑键紧跟其后 —— 中间剩余空间留给
  // 右侧那组控件。
  const titleAt = panel.indexOf('{task.name || t("task.namePlaceholder")}');
  assert.ok(titleAt > 0, "the title slot must render the task name");
  const titleTail = panel.slice(titleAt, titleAt + 400);
  assert.match(titleTail, /task\.renameTask[\s\S]{0,200}?<PencilIcon \/>/, "编辑键必须紧贴任务名，不再挂在右上角");
  assert.match(
    panel,
    /flex: "0 1 auto", minWidth: 0, fontSize: "14px", fontWeight: 700/,
    "任务名要能收缩，剩余空间才算「中间留白」",
  );
  // 标题槽允许换行，名字 + 编辑键必须自己 nowrap：长名字只把名字压出省略号，
  // 不能把编辑键挤到第二行（header 会变成两行高）。
  assert.match(
    panel,
    /display: "inline-flex", alignItems: "center", gap: "8px", minWidth: 0, maxWidth: "100%", flexWrap: "nowrap"/,
    "名字与编辑键要锁在同一行",
  );

  // headerRight 只有一排平铺的动作键：既没有菜单触发键，也没有「删除」特例。
  const rightAt = panel.indexOf("headerRight={editingName");
  const right = panel.slice(rightAt, rightAt + 2000);
  assert.match(right, /actions\.map\(\(action\) => \(/, "动作必须逐项平铺");
  assert.match(right, /title=\{action\.label\}/);
  assert.match(right, /aria-label=\{action\.label\}/);
  assert.doesNotMatch(right, /TaskDetailActionsMenu|aria-haspopup="menu"/, "菜单整个去掉，不留触发键");
  assert.doesNotMatch(right, /\{terminal \? \([\s\S]{0,300}?deleteTaskCard/, "删除不再走 terminal 特例分支");
  assert.match(panel, /^\s*showCloseButton$/m, "任务详情要右上角关闭键");

  // 关闭键放在 headerRight 之后、由 PanelShell opt-in 渲染。
  const closeAt = shell.indexOf("showCloseButton ? (");
  assert.ok(closeAt > 0);
  assert.ok(shell.indexOf("{headerRight}") < closeAt, "关闭键排在动作行右边");
});

test("平铺动作：动作表单一来源、九种动作都有图标与回调、删除在同一排里", () => {
  const panel = read("../src/components/task/TaskDetailPanel.tsx");

  assert.match(panel, /taskDetailActions\(task\)\.map/);
  assert.match(panel, /TASK_DETAIL_ACTION_META\[key\]\.labelKey/);
  // 九种动作都要有图标与回调；漏一个会在 tsc 的 Record 上直接报错，这里再钉一次。
  for (const key of [
    "start",
    "interrupt",
    "resume",
    "complete",
    "cancel",
    "reopen",
    "archive-task",
    "unarchive-task",
    "delete-task",
  ]) {
    assert.match(panel, new RegExp(`"?${key}"?: `), `panel must wire the ${key} action`);
  }
  // 删除与其它动作一起排：在同一个 actionIcons / actionHandlers 映射里。
  assert.match(panel, /"delete-task": <DeleteIcon \/>/);
  assert.match(panel, /"delete-task": \(\) => void deleteTaskCard\(\)/);
  // 不再有菜单弹层：没有 portal、没有 role=menu、没有竖排弹层。
  assert.doesNotMatch(panel, /createPortal|role="menu"|role="menuitem"/);
  // 平铺键与看板卡片的图标键同形同色：形状取 taskCardIconButtonStyle（不再覆写宽高/圆角），
  // 三种语义档位按卡片的配色映射，间距也回到卡片的 0。
  assert.match(panel, /taskCardIconButtonStyle\(action\.tone === "primary"/);
  assert.match(panel, /action\.tone === "success" \? "success"/);
  assert.doesNotMatch(panel, /width: "28px"|height: "28px"|borderRadius: "8px"/, "盒子尺寸交给卡片同一份样式");
  assert.match(panel, /data-testid="task-detail-actions" style=\{\{ display: "inline-flex", alignItems: "center", justifyContent: "flex-end", flexWrap: "wrap", gap: 0 \}\}/);

  // 动作集合只有一份定义，且与看板卡片的 taskCardActions 分开（口径不同）。
  const detail = read("../src/components/task/taskDetailActions.ts");
  // 完成的颜色与卡片一致（卡片按钮用 success 绿），不是详情原来的 accent 蓝。
  assert.match(detail, /complete: \{ labelKey: "task\.complete", tone: "success" \}/);
  assert.match(detail, /export function taskDetailActions\(task: KanbanTask\): TaskDetailActionKey\[\] \{/);
  assert.doesNotMatch(detail, /from "\.\/taskCardActions"/, "详情口径不反过来 import 卡片口径，避免两边一起漂");
});

test("按状态给按钮：完成/取消、已归档、待审阅、执行中、待开始", () => {
  // 完成 / 取消（终态）→ 返回、归档、删除
  for (const status of ["success", "fail", "cancelled"]) {
    assert.deepEqual(taskDetailActions(task({ status })), ["reopen", "archive-task", "delete-task"], status);
  }
  // 已归档 → 返回、取消归档、删除
  assert.deepEqual(taskDetailActions(task({ status: "success", archived_at: "2026-11-22T00:00:00Z" })), [
    "reopen",
    "unarchive-task",
    "delete-task",
  ]);
  // 待审阅 → 完成、取消
  assert.deepEqual(taskDetailActions(task({ status: "waiting_user" })), ["complete", "cancel"]);
  // 执行中 → 只给中断
  assert.deepEqual(taskDetailActions(task({ status: "running" })), ["interrupt"]);
  // 待开始 → 只给开始
  assert.deepEqual(taskDetailActions(task({ status: "pending" })), ["start"]);
  // 用户清单没列「已暂停」：给恢复，否则中断之后无处可去。
  assert.deepEqual(taskDetailActions(task({ status: "paused" })), ["resume"]);
  // 已归档优先于终态：不再多给归档键。
  assert.ok(!taskDetailActions(task({ status: "success", archived_at: "x" })).includes("archive-task"));
});

test("动作文案键在两种语言里都写了", () => {
  const zh = read("../src/i18n/locales/zh-CN-tasks.ts");
  const en = read("../src/i18n/locales/en-US-tasks.ts");
  for (const [action, meta] of Object.entries(TASK_DETAIL_ACTION_META)) {
    assert.match(meta.labelKey, /^task\./, `${action}: labels come from the task namespace`);
    assert.match(zh, new RegExp(`"${meta.labelKey}": `), `${action}: zh label missing`);
    assert.match(en, new RegExp(`"${meta.labelKey}": `), `${action}: en label missing`);
  }
  assert.match(zh, /"task\.actionStart": "开始任务"/);
  assert.match(zh, /"task\.actionInterrupt": "中断任务"/);
  // 菜单删掉后，它的两条文案也不许留成死键。
  for (const src of [zh, en]) {
    assert.doesNotMatch(src, /"task\.actionsMenu":/);
    assert.doesNotMatch(src, /"task\.actionsEmpty":/);
  }
});

test("移动端正文左右留白收窄，且不引入横向溢出", () => {
  const css = read("../src/index.css");
  const panel = read("../src/components/task/TaskDetailPanel.tsx");

  assert.match(css, /\.task-detail-content \{\n\s+padding: 12px 14px;\n\}/);
  assert.match(
    css,
    /@media \(max-width: 640px\) \{\n\s+\.task-detail-content \{\n\s+padding: 10px 8px;\n\s+\}\n\}/,
    "手机上左右留白收到 8px",
  );
  // 正文容器的样式里不再写死 padding（否则会盖过媒体查询）。
  assert.match(panel, /className="task-detail-content" style=\{\{ display: "flex", flexDirection: "column", gap: "6px", minWidth: 0, flex: "1 0 auto" \}\}/);
  assert.doesNotMatch(panel, /padding: "12px 14px"/);
  // 折行与横滚都在正文内部解决：容器保持 min-width: 0。
  assert.match(css, /\.task-detail-content \{/);
  assert.match(css, /\.task-detail-execution \{\n\s+min-width: 0;\n\}/);
});
