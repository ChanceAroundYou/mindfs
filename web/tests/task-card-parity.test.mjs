import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

// 看板卡与工作台卡必须是**同一张卡**（2026-10-07）。
//
// 两侧早就引用同一个组件 TaskCardRows.tsx，但数据不是同一份：项目看板拿完整任务，
// 工作台拿 /api/tasks/overview 的投影。投影是手写的允许清单，于是同一个任务在两边
// 长出不同的按钮和徽标 —— 用户实测到的是「看板卡有小图标、工作台没有」「工作台
// 有 worktree 的卡片没有收尾键」「同一个任务两边按钮不一样」。
//
// 这条测试从**消费者**那一侧钉：把同一份任务数据喂给两个渲染入口，断言按钮集合与
// 徽标集合逐项相等。投影少一个字段 → 工作台那一侧少一个键 → 红。
//
// 服务端那一半由 server/internal/api/http_tasks_overview_card_parity_test.go 守
// （它从 TaskCardRows.tsx 源码里抽出所有字段读取，断言投影全都带）。两条合起来才
// 闭合：那条保证「数据够」，这条保证「两侧用得一样」。

const cardRows = readFileSync(new URL("../src/components/task/TaskCardRows.tsx", import.meta.url), "utf8");
const board = readFileSync(new URL("../src/components/task/TaskBoardView.tsx", import.meta.url), "utf8");
const taskRow = readFileSync(new URL("../src/components/workspace/WorkspaceTaskRow.tsx", import.meta.url), "utf8");
const types = readFileSync(new URL("../src/services/task/types.ts", import.meta.url), "utf8");
const zh = readFileSync(new URL("../src/i18n/locales/zh-CN.ts", import.meta.url), "utf8");

// ── 1. 两侧必须引用同一个卡片组件 ────────────────────────────────────────────
// 这是「同一张卡」的前提。谁再复制一份卡片实现，这里会红。
assert.match(
  board,
  /from "\.\/TaskCardRows"|from "\.\.\/task\/TaskCardRows"/,
  "the project board must render the shared TaskCardRows",
);
assert.match(
  taskRow,
  /from "\.\.\/task\/TaskCardRows"/,
  "the workspace row must render the same shared TaskCardRows",
);

// ── 2. 错误图标只能出现一次 ──────────────────────────────────────────────────
// 工作台恒显示状态文字（看板只有「已结束」列才显示），所以失败任务的红色错误图标
// 会被画两次：一次在状态文字里，一次在 aux 徽章里。hideSessionError 就是那把开关，
// 两侧必须传同一个值。
assert.match(
  board,
  /hideSessionError=\{showTaskStatus && task\.status === "fail"\}/,
  "the board suppresses the aux error icon when the status text already shows failure",
);
assert.match(
  taskRow,
  /hideSessionError=\{task\.status === "fail"\}/,
  "the workspace row must suppress it too — it always renders the status text",
);

// ── 3. 收尾键的判据两侧一致 ──────────────────────────────────────────────────
// 判据只剩 worktree 三态。「有 agent 段」那条 2026-10-07 去掉了：它会让「有
// worktree 但没有 agent 段」的任务永远拿不到收尾键，而那种任务恰恰最需要它。
assert.match(
  cardRows,
  /const canFinishWorktree = worktreeEnabled && !worktreeMissing && hasWorktreePath;/,
  "the finish gate is worktree-liveness only",
);
assert.doesNotMatch(
  cardRows,
  /has_agent_stage/,
  "has_agent_stage must be gone from the card — it is redundant once the projection carries stages",
);

// ── 4. 完成键必须兜住「推进键给不出来」的一切局面 ────────────────────────────
// 只剩两条例外：正在跑、终态。收尾中不再豁免 —— 收尾段卡在待审核时推进键恒假，
// 旧判据把完成键也一起收走了，于是那张卡只剩一个最容易失败的收尾键。
assert.match(
  cardRows,
  /const canComplete = !terminal && !stageRunning && !showAdvance;/,
  "完成 must be the inverse of showAdvance, with only running/terminal withheld",
);

// ── 5. 终态任务只要 worktree 还在就必须有收尾键 ──────────────────────────────
// 用户原话：「只要还有 worktree 就必须有收尾键」。收尾键因此不能住在 !terminal
// 分支里 —— 它现在由 FinishWorktreeButton 在两个分支各渲染一次。
assert.match(
  cardRows,
  /function FinishWorktreeButton\(/,
  "the finish button is extracted so both branches can render it without duplicating markup",
);
assert.equal(
  (cardRows.match(/canFinishWorktree \? <FinishWorktreeButton/g) || []).length,
  2,
  "the finish button must render in BOTH the non-terminal and the terminal branch",
);
// 终态分支里删除键仍在收尾键之后 —— 顺序反了会让「收尾」被挤出可视区。
assert.match(
  cardRows,
  /canFinishWorktree \? <FinishWorktreeButton[^>]*\/> : null\}\s*<button[\s\S]{0,400}?onMove\(task, "delete-task"\)/,
  "in the terminal branch the finish key comes before delete",
);

// ── 6. 投影必须带上卡片读的每个 aux 标记 ────────────────────────────────────
// 这是「看板有小图标、工作台没有」的根因：投影丢了 aux_flags。
// 服务端那条测试从源码抽字段逐条断言；这里钉的是**类型与 i18n 那一半** ——
// 投影给了字段、类型却没声明，或声明了却没有文案，图标照样画不出来。
for (const flag of ["ask_user_waiting", "has_plan", "has_todos", "has_task", "session_error"]) {
  assert.match(
    types,
    new RegExp(`\\b${flag}\\?:`),
    `KanbanTask.aux_flags must declare ${flag} — the workspace card reads it`,
  );
}
// 四个布尔各对应一个小图标，session_error 对应那个可点的红色错误图标。
// 少一条文案，那个图标就退化成一个没有含义的图形 —— 用户原话是「我都不知道什么意思」。
// 文案是 title + aria-label，两处都要有：只给 title 的话读屏用户听到的是一串乱码。
const badgeLabels = [
  ["ask_user_waiting", "task.waitingUser"],
  ["has_plan", "task.hasPlan"],
  ["has_todos", "task.hasTodos"],
  ["has_task", "task.hasTask"],
];
for (const [flag, key] of badgeLabels) {
  assert.match(zh, new RegExp(`"${key}":`), `zh-CN must label the ${flag} badge`);
}
assert.match(zh, /"task\.viewTaskSessionError"/, "zh-CN must label the session-error badge");
assert.match(zh, /"task\.viewError"/, "zh-CN must label the session-error details action");

// ── 7. 收尾失败：单通道 + 短文案 + 结构化清单 ────────────────────────────────
// 用户原话：「过于详细且复杂，简化文本，还重复显示两条红色报错，需要去重」。
// 三件事分别钉：
//
// 2026-10-07 第二轮：用户实测到**另一条**超长报错 —— 「合并已成功，但拆除 worktree
// 失败：<path> 里还有没提交的东西（<30 个文件拼成一句>）…工具自己留下的临时目录
// （.claude/ .omc/ .mindfs/）不用你管，那部分会自己清」。那是 worktree 里还有用户
// 没提交的东西、git 拒绝拆目录。旧写法把 strings.Join(UserChanges, "、") 拼进
// 一句话，三十个文件就是一句读不完的话。现在它是第四个类型化错误。
// 短文案在 i18n 里（卡片本身不渲染这个弹窗，App.tsx 与 WS 处理器渲染）。
assert.match(
  zh,
  /"task\.finishWorktreeDirty": "[^"]*主 checkout 有未提交改动[^"]*"/,
  "the dirty-main message must be one short sentence, not a file list",
);
assert.match(
  zh,
  /"task\.finishWorktreeDirtyHint": "[^"]*合并会覆盖[^"]*"/,
  "the hint must say what to do next — the sentence alone is not enough",
);
// 文件清单走结构化字段，不进消息。
const worktreeService = readFileSync(new URL("../src/services/task/worktree.ts", import.meta.url), "utf8");
assert.match(
  worktreeService,
  /class FinishWorktreeDirty extends Error/,
  "FinishWorktreeDirty carries the file list in a typed field",
);
assert.match(
  worktreeService,
  /readonly files: string\[\];/,
  "FinishWorktreeDirty.files is the structured list the dialog renders",
);
// 409 的两种失败必须分流到两个类型 —— 混成一个的话 UI 只能靠猜。
assert.match(
  worktreeService,
  /dirty_files[\s\S]{0,200}?FinishWorktreeDirty/,
  "a 409 with dirty_files must reject FinishWorktreeDirty, not a generic error",
);
// 第三个：worktree 里还有用户没提交的东西（合并已成功、只是目录拆不掉）。
assert.match(
  worktreeService,
  /class FinishWorktreeUserChanges extends Error/,
  "FinishWorktreeUserChanges is the fourth typed failure — the old long sentence is gone",
);
assert.match(
  worktreeService,
  /user_changes[\s\S]{0,200}?FinishWorktreeUserChanges/,
  "a 409 with user_changes must reject FinishWorktreeUserChanges, not a generic error",
);
// 短文案在 i18n 里，而且必须点明「合并已成功」—— 否则用户以为白干了一场。
assert.match(
  zh,
  /"task\.finishWorktreeUserChanges": "[^"]*合并已成功[^"]*"/,
  "the worktree-blocked message must say the merge already succeeded",
);
assert.match(
  zh,
  /"task\.finishWorktreeUserChangesHint": "[^"]*再点一次收尾[^"]*"/,
  "the hint must say what to do next",
);
// 错误码不能再张冠李戴：任务动作失败挂 file.write_failed，用户只会去查磁盘。
assert.match(
  zh,
  /"error\.task\.actionFailed": "任务操作失败"/,
  "task actions get their own error code, not file.write_failed",
);
// Toast 不再显示错误码那一行。
const toast = readFileSync(new URL("../src/components/shell/Toast.tsx", import.meta.url), "utf8");
assert.doesNotMatch(
  toast,
  /error\.code &&/,
  "the toast must not render the error code — it is noise for users and was actively misleading here",
);

// ── 8. 去重规则：失败只走 HTTP，成功才广播 ──────────────────────────────────
// 这条规则在服务端（http_tasks.go 的 teardownFinishWorktree），前端这一半钉的是
// 「两条通道都在，但只有一条会响」—— WS 处理器必须能处理 dirty_files，否则异步
// 那条路的失败会退化成一句没有清单的报错。
const app = readFileSync(new URL("../src/App.tsx", import.meta.url), "utf8");
const realtime = readFileSync(new URL("../src/app/useRealtimeEvents.ts", import.meta.url), "utf8");
// 同步那条路（点收尾 → HTTP 409）必须弹同一个弹窗，而不是只 toast 一句。
assert.match(
  app,
  /FinishWorktreeDirty[\s\S]{0,200}?task\.finishWorktreeDirty/,
  "App.tsx must open the dirty-main dialog, not just toast",
);
assert.match(
  realtime,
  /dirty_files/,
  "the finish_teardown WS handler must understand dirty_files — otherwise async failures lose the file list",
);
assert.match(
  realtime,
  /task\.finishWorktreeDirty/,
  "async dirty-main failures get the same dialog as synchronous ones",
);
// 异步那条路同样要认得 user_changes —— 清单丢了就只能报一句没有清单的错。
// 两条分开钉：清单字段要认得，文案键要在。合起来才弹得出那个弹窗。
assert.match(realtime, /payload\.user_changes/, "the finish_teardown handler must read user_changes");
assert.match(
  realtime,
  /task\.finishWorktreeUserChanges/,
  "async worktree-blocked failures get the same dialog as synchronous ones",
);
// begin-finish 的 409 也要还原成类型化错误（③.5 分支的清场是同步做的）。
// 两条分开钉：函数体里要认得 user_changes，也要抛对应的类型。
assert.match(
  worktreeService,
  /export async function beginTaskFinishWorktree[\s\S]{0,1200}?user_changes/,
  "beginTaskFinishWorktree must read user_changes from the 409 payload",
);
assert.match(
  worktreeService,
  /export async function beginTaskFinishWorktree[\s\S]{0,1400}?throw new FinishWorktreeUserChanges/,
  "beginTaskFinishWorktree must convert its 409 into typed errors too — otherwise the file list is lost",
);
