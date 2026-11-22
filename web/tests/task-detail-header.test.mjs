import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "vitest";

// 任务详情 header 的收口：编辑键贴着任务名，右上角**平铺**全部可用动作（含删除），
// 不再有「操作控件」菜单；移动端正文左右留白收窄。
//
// 2026-11-22 起两处图标键（看板卡片 / 详情 header）只读 taskActionButtons 一份：
// 「哪些动作此刻存在」与各状态的形状由 task-card-actions.test.mjs 钉住，
// 这里只管详情侧的结构契约（怎么渲染、有没有自造口径）。几何与实测由
// Playwright 覆盖（e2e/test_task_detail_actions.py）。

const read = (p) => readFileSync(new URL(p, import.meta.url), "utf8");

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
  // 不能把编辑键挤到第二行（header 会变成两行高）。编辑键与名字的间距取统一常量
  // TASK_CONTROL_GAP（原先 8px），与动作键之间、动作键与关闭键同一条节奏。
  assert.match(
    panel,
    /display: "inline-flex", alignItems: "center", gap: TASK_CONTROL_GAP, minWidth: 0, maxWidth: "100%", flexWrap: "nowrap"/,
    "名字与编辑键要锁在同一行，且间距与动作键同源",
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

  // 关闭键放在 headerRight 之后、由 PanelShell opt-in 渲染；两者编成一组，
  // 间距由 headerRightGap 决定（任务详情传 TASK_CONTROL_GAP，与动作键内部同一条）。
  const closeAt = shell.indexOf('aria-label={t("common.close")}');
  assert.ok(closeAt > 0);
  assert.ok(shell.indexOf("{headerRight}") < closeAt, "关闭键排在动作行右边");
  assert.match(panel, /headerRightGap=\{TASK_CONTROL_GAP\}/, "详情要把关闭键间距接到统一常量上");
  assert.match(shell, /headerRightGap = 8,/, "其它面板不传时保持原来的 8px");
  assert.match(
    shell,
    /gap: `\$\{headerRightGap\}px`/,
    "headerRight 与关闭键必须同组渲染，间距才是可配的那一条",
  );
});

test("平铺动作与看板卡片同源：一份键序、一份图形、一份配色", () => {
  const panel = read("../src/components/task/TaskDetailPanel.tsx");

  // 键序：与卡片同一个纯函数（不再是详情专用的 taskDetailActions）。
  assert.match(panel, /taskActionButtons\(task\)\.map/);
  assert.doesNotMatch(panel, /taskDetailActions|TASK_DETAIL_ACTION_META/);
  // 文案/档位取共享表，图形取共享映射。
  assert.match(panel, /TASK_ACTION_BUTTON_META\[key\]\.labelKey/);
  assert.match(panel, /TASK_ACTION_BUTTON_META\[key\]\.tone/);
  assert.match(panel, /taskActionIcons\[key\]/);
  // hover 文案 = aria-label = 动作全称（用户裁定「更随卡片，补全所有短语」）。
  assert.match(panel, /title=\{action\.label\}/);
  assert.match(panel, /aria-label=\{action\.label\}/);

  // 九种动作都要有回调；漏一个会在 tsc 的 Record 上直接报错，这里再钉一次。
  for (const key of [
    "finish-worktree",
    "pause",
    "resume",
    "complete",
    "cancel",
    "reopen",
    "archive-task",
    "unarchive-task",
    "delete-task",
  ]) {
    assert.match(panel, new RegExp(`"?${key}"?: \\(\\) => void `), `panel must wire the ${key} action`);
  }
  // 收尾与暂停都接到真实处理函数上（用户裁定「详情补收尾」「以卡片为准」）。
  assert.match(panel, /"finish-worktree": \(\) => void panel\.finishWorktree\(\)/);
  assert.match(panel, /pause: \(\) => void panel\.pauseTask\(\)/);

  // 不再有菜单弹层：没有 portal、没有 role=menu、没有竖排弹层。
  assert.doesNotMatch(panel, /createPortal|role="menu"|role="menuitem"/);
  // 形状与配色交给卡片同一份映射：不再自己写 tone 三元式、不再覆写宽高/圆角。
  assert.match(panel, /taskActionIconStyle\(action\.tone\)/);
  assert.doesNotMatch(panel, /taskCardIconButtonStyle\(/, "详情不再手搓档位映射");
  assert.doesNotMatch(panel, /width: "28px"|height: "28px"|borderRadius: "8px"/, "盒子尺寸交给卡片同一份样式");
  // 键间距与卡片同一条常量（不再各自写死 0）：容器本身仍只负责排列与换行。
  assert.match(panel, /data-testid="task-detail-actions" style=\{\{ display: "inline-flex", alignItems: "center", justifyContent: "flex-end", flexWrap: "wrap", gap: TASK_CONTROL_GAP \}\}/);
});

test("详情侧不再保留自己的一套动作口径", () => {
  // 旧文件删除后不许有人再 import 它；开始/中断两条旧动作与文案一起下线。
  for (const src of [
    read("../src/components/task/taskCardActions.ts"),
    read("../src/components/task/TaskDetailPanel.tsx"),
    read("../src/components/task/TaskCardRows.tsx"),
  ]) {
    assert.doesNotMatch(src, /taskDetailActions|TASK_DETAIL_ACTION_META/);
  }
  const zh = read("../src/i18n/locales/zh-CN-tasks.ts");
  const en = read("../src/i18n/locales/en-US-tasks.ts");
  for (const src of [zh, en]) {
    for (const dead of ["task.actionStart", "task.actionInterrupt", "task.actionsMenu", "task.actionsEmpty"]) {
      assert.doesNotMatch(src, new RegExp(`"${dead.replace(/\./g, "\\.")}":`), `${dead} 不许留成死键`);
    }
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
