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
const css = read("src/index.css");
const boardView = read("src/components/TaskBoardView.tsx");
const viteConfig = read("vite.config.ts");
// Go 侧按**包**读，不钉文件名：2026-10-16 的 B1–B10 拆分把 service.go /
// service_tasks.go 切成职责文件（service_run_moves.go、service_create.go…），
// 实现没少、只是搬了家。
const serviceGo = readGoPackage("server/internal/kanban");

// 8) 阶段编辑已随阶段模型删除：详情面板既不渲染阶段列表，也没有阶段级的
//    保存/删除入口。历史任务在迁移时转成 activities，不再有只读兼容视图。
assert.doesNotMatch(
  panel,
  /<StageEditor/,
  "the detail panel no longer renders an editable stage list",
);
assert.doesNotMatch(
  panel,
  /onEdit=\{/,
  "stage editing (and its pencil) is no longer part of the detail panel",
);
assert.doesNotMatch(
  panel,
  /onSend=\{/,
  "stage save is no longer part of the detail panel",
);
assert.doesNotMatch(
  panelHook,
  /startEditStage|saveStage/,
  "the detail hook must not keep stage editing helpers after the stage model was removed",
);
assert.doesNotMatch(
  panel,
  /\{!executed && !isCurrent \? \(/,
  "the detail panel no longer renders per-stage delete controls",
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
//    2026-11-22 动作集合收成一份纯函数（taskCardActions.ts）：卡片与详情图标行
//    都按 `taskActionButtons(task)` 渲染，分支形状改由那份函数钉住
//    （见 task-card-actions.test.mjs）。这里只钉「卡片确实读它、且不再按 terminal
//    把整排藏起来」。
const cardRows = read("src/components/TaskCardRows.tsx");
assert.match(cardRows, /const buttons = taskActionButtons\(task\);/, "the action row renders the shared list");
assert.match(cardRows, /buttons\.map\(\(key\) => \{/, "one renderer for every state — no duplicated branches");
assert.match(
  cardRows,
  /if \(key === "finish-worktree"\) \{\s*return <TaskCardFinishButton/,
  "the finish key keeps its dedicated button inside the shared list",
);
const cardActionsSource = read("src/components/task/taskCardActions.ts");
assert.match(
  cardActionsSource,
  /if \(task\.status === "running"\) return \["terminate"\];/,
  "运行中只有一个动作：终止（暂停/完成/取消都会让「活还在跑」与界面不一致）",
);
assert.match(
  cardActionsSource,
  /if \(task\.status === "pending"\) \{[\s\S]*?actions\.push\("start"\);[\s\S]*?\} else \{[\s\S]*?actions\.push\("complete"\);[\s\S]*?\}[\s\S]*?actions\.push\("cancel"\);/,
  "未开始给「开始」、待处理给「完成」，两者都有「取消」",
);
assert.match(
  cardActionsSource,
  /if \(canFinishWorktree\(task\)\) actions\.push\("finish-worktree"\);\n\s*actions\.push\("reopen", "archive-task"\);/,
  "terminal keeps 收尾 + 返回 + 归档",
);
// 删除由 taskActionButtons 只在终态追加，且永远在最右。
assert.match(
  cardActionsSource,
  /if \(isTerminalKanbanTask\(task\)\) keys\.push\("delete-task"\);/,
  "the delete key belongs to terminal tasks only",
);
assert.doesNotMatch(
  cardRows,
  /<DeleteIcon \/>/,
  "取消与删除的图形各在一处（共享映射），卡片里不再内联删除图标",
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
// 切片到下一个函数为止，而不是断言整个函数体只有这一条 return —— workflow run
// 分支在前面多一层引擎分派，不该让「legacy 取消不看 worktree」这条约束失效。
const cancelAt = serviceGo.indexOf("func (s *Service) Cancel(");
// 切到函数自身的结束行，而不是下一个 `func`：两者之间还夹着下一个函数的
// 文档注释（Complete 的注释本来就提到 worktree/session），算进来会污染断言。
const cancelClose = serviceGo.indexOf("\n}\n", cancelAt);
const cancelEnd = cancelClose > cancelAt ? cancelClose + 3 : serviceGo.indexOf("\nfunc ", cancelAt + 1);
const cancelBody = serviceGo.slice(cancelAt, cancelEnd);
assert.match(
  cancelBody,
  /return s\.setTaskStatus\(ctx, in\.RootID, in\.TaskID, StatusCancelled, "cancelled", in\.Reason, true\)/,
  "Cancel must not be gated on worktree or session state",
);
// 分支再多也不能把「会话/worktree 在不在」变成取消的前置条件：workflow 引擎
// 分支同样只做取消，不读取 worktree 路径或会话状态来决定能不能取消。
assert.doesNotMatch(
  cancelBody,
  /worktree|session/i,
  "neither Cancel branch may consult the worktree or session before cancelling",
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
// 阶段级状态色已随内联阶段循环一起移出详情面板；运行视图那一份仍由
// RunNodeRunRow / runTimelineModel 单源守卫（见 legacy-flow-render.test.mjs）。
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
