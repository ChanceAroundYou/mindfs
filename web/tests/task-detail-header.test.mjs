import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "vitest";

// 任务详情 header 的收口：编辑键贴着任务名，右上角是「操作控件」菜单 + 删除 + 关闭；
// 移动端正文左右留白收窄。这里只钉源码契约与样式规则，几何由 Playwright 实测。

const read = (p) => readFileSync(new URL(p, import.meta.url), "utf8");

test("header 结构：名字旁边是编辑键，操作控件与删除在右侧，关闭是 opt-in", () => {
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

  // headerRight 只剩两块：操作控件菜单 + 删除（删除按用户要求不进菜单）。
  const rightAt = panel.indexOf("headerRight={editingName");
  const right = panel.slice(rightAt, rightAt + 1600);
  assert.match(right, /<TaskDetailActionsMenu actions=\{actions\} disabled=\{saving\} \/>/);
  assert.match(right, /\{terminal \? \([\s\S]{0,400}?<DeleteIcon \/>/);
  assert.equal(
    (right.match(/pencilStyle\(false\)/g) || []).length,
    1,
    "右侧只留删除这一颗 pencilStyle 图标键",
  );
  assert.match(panel, /^\s*showCloseButton$/m, "任务详情要右上角关闭键");

  // 关闭键放在 headerRight 之后、由 PanelShell opt-in 渲染。
  const closeAt = shell.indexOf("showCloseButton ? (");
  assert.ok(closeAt > 0);
  assert.ok(shell.indexOf("{headerRight}") < closeAt, "关闭键排在操作控件/删除右边");
});

test("操作控件菜单：只有可执行的动作、删除不进菜单、弹层挂到 body", () => {
  const menu = read("../src/components/task/TaskDetailActionsMenu.tsx");
  const panel = read("../src/components/task/TaskDetailPanel.tsx");

  // 三个来源动作都在 panel 侧按 can* 判据组装，菜单只负责渲染。
  assert.match(panel, /if \(canFinishWorktree\)[\s\S]{0,300}?key: "finish-worktree"/);
  assert.match(panel, /if \(canCompleteTask\)[\s\S]{0,300}?key: "complete"/);
  assert.match(panel, /if \(canReopenTask\)[\s\S]{0,400}?key: "reopen"/);
  // 删除不进菜单：菜单里没有任何 deleteTaskCard / DeleteIcon。
  assert.doesNotMatch(menu, /deleteTaskCard|DeleteIcon|task\.deleteTask/);
  assert.doesNotMatch(panel, /key: "delete"/);

  // 面板卡 overflow:hidden，弹层必须 portal 到 body 且 fixed 定位。
  assert.match(menu, /createPortal\(/);
  assert.match(menu, /document\.body/);
  assert.match(menu, /position: "fixed"/);
  assert.match(menu, /role="menu"/);
  assert.match(menu, /role="menuitem"/);
  assert.match(menu, /aria-haspopup="menu"/);
  // 点外面 / Esc / 滚动 / 改窗口都收起，不留一张挂着的菜单。
  assert.match(menu, /document\.addEventListener\("pointerdown", onPointerDown, true\)/);
  assert.match(menu, /event\.key === "Escape"/);
  assert.match(menu, /window\.addEventListener\("scroll", onDismiss, true\)/);
  assert.match(menu, /window\.addEventListener\("resize", onDismiss\)/);
  // 没有可执行动作时给一行说明，而不是空弹层。
  assert.match(menu, /t\("task\.actionsEmpty"\)/);
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
  assert.match(panel, /className="task-detail-content" style=\{\{ display: "flex", flexDirection: "column", gap: "6px", minWidth: 0 \}\}/);
  assert.doesNotMatch(panel, /padding: "12px 14px"/);
  // 折行与横滚都在正文内部解决：容器保持 min-width: 0。
  assert.match(css, /\.task-detail-content \{/);
  assert.match(css, /\.task-detail-execution \{\n\s+min-width: 0;\n\}/);
});

test("新增文案两种语言都有", () => {
  const zh = read("../src/i18n/locales/zh-CN-tasks.ts");
  const en = read("../src/i18n/locales/en-US-tasks.ts");
  for (const src of [zh, en]) {
    assert.match(src, /"task\.actionsMenu": "/);
    assert.match(src, /"task\.actionsEmpty": "/);
  }
  assert.match(zh, /"task\.actionsMenu": "操作控件"/);
});
