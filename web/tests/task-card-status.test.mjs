import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { test } from "vitest";
import { readGoPackage } from "./go-source.mjs";

// 卡与状态的契约：已执行阶段只读、任务状态不绑会话/worktree、
// Service Worker 缓存策略、任务状态色单源。

test("task-card-status", async () => {
const root = path.resolve(import.meta.dirname, "..");
const read = (rel) => fs.readFileSync(path.join(root, rel), "utf8");

const appTask = read("src/app/appTask.ts");
const tasksService = read("src/services/tasks.ts");
const panel = read("src/components/task/TaskDetailPanel.tsx");
const panelHook = read("src/components/task/useTaskDetailPanel.ts");
const stageEditor = read("src/components/StageEditor.tsx");
const css = read("src/index.css");
const boardView = read("src/components/TaskBoardView.tsx");
const viteConfig = read("vite.config.ts");
// Go 侧按**包**读，不钉文件名：2026-10-16 的 B1–B10 拆分把 service.go /
// service_tasks.go 切成职责文件（service_run_moves.go、service_create.go…），
// 实现没少、只是搬了家。
const serviceGo = readGoPackage("server/internal/kanban");

// 8) 已执行过的阶段只可看：不可改、不可保存。三道门控各挡一路入口。
assert.match(
  panelHook,
  /const existingRun = latestStageRun\(detail, index\);\s*\n\s*if \(existingRun && String\(existingRun\.status\) !== "pending"\) return;/,
  "startEditStage must refuse an executed stage: no path may put it into edit mode",
);
assert.match(
  panel,
  /onEdit=\{editing \|\| executed \? undefined : \(\) => requestEditStage\(index\)\}/,
  "an executed stage must not offer the pencil",
);
assert.match(
  panel,
  /onSend=\{editing && !executed \? \(text: string\) => void saveStage\(index, text\) : undefined\}/,
  "an executed stage must not offer save",
);
assert.match(
  panelHook,
  /const run = latestStageRun\(detail, index\);\s*\n\s*if \(run && String\(run\.status\) !== "pending"\) return;/,
  "saveStage must bail on an executed stage even if some future caller reaches it",
);
// 只读态仍然显示该段真实的选项值（不是隐藏、也不是全灰）
assert.match(stageEditor, /readOnly=\{mode !== "editable"\}/, "read mode shows option values, disabled");
assert.match(
  panel,
  /\{!executed && !isCurrent \? \(/,
  "an executed stage must not offer delete either",
);

// 9) 任务状态不能和会话 / worktree 绑死：会话没了、worktree 被删导致卡住的
//    任务，卡片上仍要能调整状态。曾经整排操作区都包在 !taskTerminal 里，
//    于是「已结束但状态不对」的任务连删除都点不到，只能干看着。
//    标题行/操作行已抽进 TaskCardRows（看板和工作台共用），契约跟着文件走。
//
//    2026-10-06 改了**出口的形状**，意图不变：终态任务现在给的是「删除」而不是
//    「取消」—— 取消只改状态、卡片照旧留在板上，终态卡永远清不掉。所以那条
//    「终态也得能收拾」的断言换成了钉删除键，而不是删掉换绿灯。
//
//    2026-10-07 终态那一侧多了一把键：**收尾**。任务跑完了 worktree 还留着没收，
//    那正是收尾唯一有意义的时刻（服务端 reviveTerminalTask 会把它拉回 waiting_user
//    再让收尾段跑）。所以终态分支现在是「收尾 + 删除」，不是只有删除。
const cardRows = read("src/components/TaskCardRows.tsx");
assert.match(
  cardRows,
  /\{!terminal \? \(\s*<>[\s\S]*?showAdvance[\s\S]*?canComplete[\s\S]*?onMove\(task, "cancel"\)[\s\S]*?<\/>\s*\) : \(\s*<>[\s\S]*?canFinishWorktree[\s\S]*?<button[\s\S]{0,400}?onMove\(task, "delete-task"\)[\s\S]*?<\/>\s*\)/,
  "terminal tasks must still get a cleanup key — 收尾 + 删除 — while run/complete/advance/cancel stay withheld",
);
// 删除键收尾于终态分支的 `)}` —— 它不在 !terminal 那一侧（那一侧以取消键收尾）。
// 2026-10-07 起那一侧多了一个 Fragment 包裹（收尾 + 删除），所以锚点从 `)}`
// 换成 Fragment 的 `</>`。
assert.match(
  cardRows,
  /onMove\(task, "delete-task"\)[\s\S]{0,220}?<\/button>\s*<\/>\s*\)\}/,
  "the delete key belongs to the terminal branch, not to the whole action row",
);
assert.doesNotMatch(
  cardRows,
  /onMove\(task, "cancel"\)[\s\S]{0,60}?<DeleteIcon \/>/,
  "取消 must not wear the trash can — it deletes nothing",
);
assert.doesNotMatch(
  boardView,
  /\{!taskTerminal \? \(\s*<div style=\{\{ display: "flex", justifyContent: "flex-end"/,
  "the whole action row must no longer be hidden for terminal tasks",
);
// Cancel 本身不看 worktree / session：服务端末端任务也能取消，并清掉会话错误。
assert.match(
  serviceGo,
  /func \(s \*Service\) Cancel\(ctx context\.Context, in MoveInput\) \(TaskDetail, error\) \{\s*\n\s*return s\.setTaskStatus\(ctx, in\.RootID, in\.TaskID, StatusCancelled, "cancelled", in\.Reason, true\)/,
  "Cancel must not be gated on worktree or session state",
);
assert.match(
  serviceGo,
  /store\.UpdateTaskStatus\(ctx, taskID, status, terminal\)/,
  "Cancel must clear the session error flag so a stuck task can be cleaned up",
);

// 10) Service Worker 缓存策略：index.html 是整条缓存链的根。
//     一旦把它 cache-first 住，旧标签页会一直拿旧壳子去请求早已不存在的
//     旧 chunk（内容哈希对不上），表现为一整批过期 js 反复加载。
//     静态资源则改 stale-while-revalidate，让哈希收敛自动发生。
const navHandler = viteConfig.slice(
  viteConfig.indexOf("async function handleNavigationRequest"),
  viteConfig.indexOf("async function handleStaticRequest"),
);
assert.doesNotMatch(
  navHandler,
  /cache\.put\(INDEX_URL/,
  "index.html must never be written back into the cache: it is the root of the whole asset chain",
);
assert.match(
  navHandler,
  /return await fetch\(request\);/,
  "navigation must go straight to the network, falling back to the precached shell when offline",
);
const staticHandler = viteConfig.slice(
  viteConfig.indexOf("async function handleStaticRequest"),
  viteConfig.indexOf("function revalidate("),
);
assert.match(
  staticHandler,
  /cachedRuntimeResponse[\s\S]{0,160}?revalidate\(request, runtimeCache\);/,
  "cached runtime assets must revalidate in the background instead of being served stale forever",
);
assert.match(
  viteConfig,
  /function revalidate\(request, cache\) \{[\s\S]*?fetch\(request\)[\s\S]*?cache\.put\(/,
  "revalidate must refresh the cached copy in the background",
);

// 10) 任务状态色只有一份定义（appTask.taskStatusColor），看板卡 / 工作台卡 /
//     详情面板都从它拿。以前详情面板自带一份 statusColors，工作台干脆没有配色，
//     于是同一个「已完成」在三处是三种颜色。色值走 --status-* token 而不是字面 hex，
//     深浅主题（含 meadow/moss）才能自动跟随。
assert.match(
  appTask,
  /export function taskStatusColor\(status: string\): string \{[\s\S]*?return colors\[status\] \|\| "var\(--text-secondary\)";/,
  "one helper maps task status to a color, with a neutral fallback",
);
for (const [status, color] of [
  ["success", "var(--status-ok)"],
  ["approved", "var(--status-ok)"],
  ["fail", "var(--status-bad)"],
  ["rejected", "var(--status-bad)"],
  ["waiting_user", "var(--status-warn)"],
  ["running", "var(--accent-color)"],
  ["pending", "var(--text-secondary)"],
  ["paused", "var(--text-secondary)"],
  ["cancelled", "var(--text-secondary)"],
]) {
  assert.match(
    appTask,
    new RegExp(`\\b${status}: "${color.replace(/[()]/g, "\\$&")}"`),
    `${status} must map to ${color}`,
  );
}

// queued 已退役：migrate() 无条件把存量归入 pending（Go 侧由
// TestMigrateFoldsQueuedIntoPending 钉住），所以前端不该再认得它。
// 类型系统挡不住这一条 —— taskStatusColor 的 colors 是 Record<string, string>，
// 加回一条 queued 不报错；所以用源码断言兜住，免得「排队 / 调度位」那套已删模型复活。
assert.doesNotMatch(appTask, /\bqueued\s*:/, "taskStatusLabel/taskStatusColor 不得再出现 queued");
assert.doesNotMatch(tasksService, /"queued"/, "TaskStatus 联合类型不得再出现 queued");
assert.doesNotMatch(
  appTask,
  /#[0-9a-fA-F]{3,8}\b/,
  "the status helper must not hardcode hex — light/dark themes each supply their own value",
);
assert.match(panel, /taskStatusColor\(task\.status\)/, "the detail panel reads the shared status color");
assert.match(panel, /taskStatusColor\(run\?\.status \|\| ""\)/, "so does the per-stage run badge");
assert.doesNotMatch(
  panel,
  /const statusColors: Record<string, string>/,
  "the detail panel's private statusColors map is gone — one definition, not two",
);
for (const token of ["--status-ok", "--status-warn", "--status-bad"]) {
  assert.ok(css.includes(`${token}:`), `${token} must be defined in index.css`);
  // 浅色 :root + 深色 data-theme + prefers-color-scheme 三个块都得给值
  const occurrences = css.split(`${token}:`).length - 1;
  assert.ok(occurrences >= 3, `${token} needs a light value plus both dark blocks, found ${occurrences}`);
}

console.log("task-card-status.test.mjs: OK");
});
