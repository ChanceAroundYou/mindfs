import assert from "node:assert/strict";
import test from "node:test";
import fs from "node:fs";
import path from "node:path";

// 收尾（wt-finish 的服务端那一半）的接线契约。
// 钉的是「结构对不对」：按钮长在哪、什么条件下给、冲突走哪条路。真 git 的行为
// （哪些退出码代表冲突、branch -d 什么时候拒绝）在 Go 侧用真仓库验，见
// server/internal/gitview/worktree_finish_test.go 和 kanban/worktree_finish_test.go。

const root = path.resolve(import.meta.dirname, "..");
const read = (rel) => fs.readFileSync(path.join(root, rel), "utf8");
const card = read("src/components/TaskCardRows.tsx");
const panel = read("src/components/TaskDetailPanel.tsx");
const app = read("src/App.tsx");
const tasks = read("src/services/tasks.ts");
const gitviewFinish = read("../server/internal/gitview/worktree_finish.go");
const kanbanFinish = read("../server/internal/kanban/worktree_finish.go");
const serviceSrc = read("../server/internal/kanban/service.go");
const httpTasks = read("../server/internal/api/http_tasks.go");
const httpRoutes = read("../server/internal/api/http.go");
const zh = read("src/i18n/locales/zh-CN.ts");
const en = read("src/i18n/locales/en-US.ts");

// ── 服务端：路由 + 冲突分流 ──
assert.match(
  httpRoutes,
  /r\.Post\("\/api\/tasks\/\{id\}\/finish-worktree", h\.protectedEndpoint\(h\.handleKanbanTaskFinishWorktree\)\)/,
  "the finish endpoint must be routed like every other task verb",
);
assert.match(
  httpTasks,
  /errors\.As\(err, &conflict\)[\s\S]{0,80}http\.StatusConflict/,
  "a merge conflict must be 409, not a 400: it is a repo awaiting manual work, not a malformed request",
);
// 文件清单要单独回，不能塞进 message 里 —— 前端要拿它渲染可点的条目。
assert.match(httpTasks, /"conflict_files": conflict\.ConflictFiles/, "the conflict file list must travel as its own field");
// 合并成功但后面某步失败时，已完成的进度要一起回去，否则用户以为全白做了。
assert.match(httpTasks, /"result":\s*result,/, "a partial failure must still carry the work that did land");
// 非冲突的失败是 400 不是 500：root/task 找不到、worktree 身份对不上，都是请求
// 本身不成立，和同文件里 next/run-now 一个口径。回 500 会让前端按「服务端炸了」
// 处理，还顺带吐一个全零的 task 出去。运行期行为由 Go 侧
// http_tasks_finish_test.go 钉（这里只守源码形态）。
//
// 只在**这个 handler 的函数体**里找：在整文件上跑正则的话，`[\s\S]*?` 会一路
// 跳到文件后面随便哪个 StatusBadRequest，断言恒真 —— 改回 500 也照样绿。
const finishHandlerSrc = httpTasks.slice(
  httpTasks.indexOf("func (h *HTTPHandler) handleKanbanTaskFinishWorktree("),
  httpTasks.indexOf("func (h *HTTPHandler) handleKanbanTaskMove("),
);
assert.ok(finishHandlerSrc.length > 0, "the finish handler must exist to assert against");
assert.match(
  finishHandlerSrc,
  /if err != nil \{[\s\S]{0,200}?http\.StatusBadRequest/,
  "a non-conflict failure must be 400, matching the sibling task verbs",
);
assert.doesNotMatch(
  finishHandlerSrc,
  /http\.StatusInternalServerError/,
  "the finish handler must not answer 500; every failure here is either 409 (conflict) or 400 (bad request)",
);

// ── 服务端：顺序不可换 ──
// 先合后拆。反过来会先把分支删掉，合的就不是那份活 —— 这种顺序错了测试全绿、
// 线上丢代码，所以钉死源码里的先后。
const mergeAt = kanbanFinish.indexOf("gitview.MergeBranch(ctx,");
const removeAt = kanbanFinish.indexOf("gitview.RemoveWorktree(ctx,");
const branchAt = kanbanFinish.indexOf("gitview.DeleteBranch(ctx,");
assert.ok(mergeAt > 0 && removeAt > mergeAt, "the merge must happen before the worktree is removed");
assert.ok(branchAt > removeAt, "the branch can only be deleted after the worktree stops holding it");

// 「分支没了」不等于「已合入」：分支可能被 `branch -D` 强删，而 worktree 还停在
// detached HEAD 上 —— 那份提交唯一的引用就是它。把这种情况当幂等跳过、接着拆掉
// worktree，提交就一个引用都不剩了（实测复现过）。所以源分支消失时必须先问
// 「这份活还有没有别的引用」。
assert.match(
  gitviewFinish,
  /if !branchExists\(ctx, opts\.MainDir, source\) \{\s*return mergeVanishedSource\(ctx, opts, source, target\)/,
  "a vanished source branch must reach the reachability check, not be waved through as already-merged",
);
assert.match(
  gitviewFinish,
  /那是它唯一的引用，拆掉就没了/,
  "the vanished-source path must explain that the worktree is the only reference",
);

// 拆 worktree 之前必须核对「这个目录还挂在那个分支上」：任务记的是建树时的
// 目录 + 分支，目录被手工 checkout 到别处时拆它就是拆别人的活。
assert.match(
  kanbanFinish,
  /if err := s\.assertWorktreeMatches\(ctx, mainDir, worktreePath, branch\); err != nil \{/,
  "the worktree's registered branch must be verified before anything is removed",
);
const mergeCallAt = kanbanFinish.indexOf("gitview.MergeBranch(ctx,");
const identityAt = kanbanFinish.indexOf("s.assertWorktreeMatches(ctx,");
assert.ok(identityAt > 0 && identityAt < mergeCallAt, "the identity check must run before the merge, not after");

// 收尾与执行必须互斥：查完「没在跑」到拆目录之间有几秒，中间起 agent 的话
// 它的 cwd 就是那个即将被拆掉的目录。
assert.match(
  kanbanFinish,
  /defer release\(\)/,
  "the finish lock must be held across the whole teardown, not just the first check",
);
assert.match(
  serviceSrc,
  /if _, finishing := s\.taskFinish\[key\]; finishing \{\s*s\.mu\.Unlock\(\)\s*return errTaskFinishing/,
  "starting an agent during a finish must be refused loudly, not dropped silently",
);
// 锁的「所有权」必须可辨认：两个并发收尾共一个 bool 时，先完成的那个 release 会把
// 还在跑的另一个一起解锁，之后 RunTask 就能对着一个正在被拆的目录起来。
assert.match(
  serviceSrc,
  /if _, busy := s\.taskFinish\[key\]; busy \{\s*return nil, errors\.New\("该任务正在收尾中/,
  "a second concurrent finish must be refused, not run alongside the first",
);
assert.match(
  serviceSrc,
  /if held, ok := s\.taskFinish\[key\]; ok && held == token \{\s*delete\(s\.taskFinish, key\)/,
  "release must only drop its own lock, never the one a concurrent finish still holds",
);

// 准入必须是**互斥的一段**，不能只是「查一下」：查通过之后、状态改成 running 之前，
// 一个收尾抢进来插队的后果是「状态 running、没有执行体」——没有 agent 会再碰它。
// 所以准入和状态变更绑在同一把锁里，收尾要么进不来，要么等这段走完。
assert.match(
  serviceSrc,
  /func \(s \*Service\) beginRunAdmission\(rootID, taskID string\) \(func\(\), error\)/,
  "run admission must be a held lock, not a non-atomic pre-check",
);
assert.match(
  serviceSrc,
  /if _, admitted := s\.taskAdmit\[key\]; admitted \{\s*return nil, errors\.New\("该任务正在启动中/,
  "a finish must be refused while a run admission is in flight",
);
const resumeAt = serviceSrc.indexOf("func (s *Service) Resume(");
const resumeBody = serviceSrc.slice(resumeAt, resumeAt + 800);
assert.match(
  resumeBody,
  /beginRunAdmission\(in\.RootID, in\.TaskID\)[\s\S]{0,120}?defer release\(\)[\s\S]{0,200}?setTaskStatus\(ctx, in\.RootID, in\.TaskID, StatusRunning/,
  "Resume must hold admission across both the status change and RunTask",
);
// 五个动词都要走同一把准入锁，别只改一个。
for (const verb of ["Resume", "Next", "RerunStage", "RunNow"]) {
  const at = serviceSrc.indexOf(`func (s *Service) ${verb}(`);
  assert.ok(at > 0, `${verb} must exist`);
  const body = serviceSrc.slice(at, at + 2500);
  assert.match(
    body,
    /beginRunAdmission\(/,
    `${verb} must take a run admission before mutating task state`,
  );
}

// ── 服务端：两条口径由用户拍板，不能被后续改动悄悄改掉 ──
assert.match(
  gitviewFinish,
  /"branch", "-d", branch/,
  "branch deletion must stay `branch -d`: it refuses to delete an unmerged branch instead of losing commits",
);
assert.doesNotMatch(
  gitviewFinish,
  /"branch", "-D"/,
  "there must be no force-delete path: silently dropping commits is the worst outcome of a worktree teardown",
);
assert.match(
  gitviewFinish,
  /if !opts\.FastForward \{\s*args = append\(args, "--no-ff"\)/,
  "a merge must leave a merge commit by default, so the mainline shows worktree work was merged in",
);
// 冲突不自动 abort：解到一半的取舍连同 MERGE_MSG 一起丢，用户得从头再来。
assert.doesNotMatch(gitviewFinish, /"merge", "--abort"/, "conflicts must be left in place for manual resolution");
assert.match(
  gitviewFinish,
  /ErrMergeConflict = errors\.New\("merge conflict"\)/,
  "conflicts need a sentinel so callers can tell them apart from other merge failures",
);

// ── 孤儿目录：只列不删 ──
// worktree 里常留着 git 完全不跟踪的 .mindfs/（会话库），删了不可恢复。
assert.doesNotMatch(
  kanbanFinish,
  /os\.RemoveAll\(/,
  "finishing a task must never bulk-delete leftover directories; the session DB lives in one of them",
);
assert.match(kanbanFinish, /Orphans = orphans/, "orphans must be reported back to the caller");

// ── 前端：冲突要分流，不能只 toast 一句 ──
// 收尾改成流水线阶段（2026-09）之后冲突的**唯一**来源是服务端在清场 goroutine
// 里播回来的 task.finish_teardown：HTTP 响应早就回来了（那是「阶段已发起」），
// 面板也早就把上下文丢了。于是分流点从「catch 里 instanceof」移到了 WS 处理器。
assert.match(
  read("src/app/useRealtimeEvents.ts"),
  /if \(payload\.error\) \{[\s\S]{0,400}details: conflicts/,
  "the teardown receipt must surface a conflict as a titled dialog listing the files, not a toast",
);
assert.doesNotMatch(
  panel,
  /setFinishConflict/,
  "the detail panel must not keep a second copy of the conflict state — there is no HTTP response to catch it from any more",
);

// ── 前端：按钮的给法 ──
// 目录已经没了的只能重建，没有 worktree 可拆 —— 两个键不能同时出现，
// 更不能让收尾键在失效路径上假装能点。
//
// **终态不再拦截**（2026-10-04 用户要求）：任务跑完但 worktree 还留着没收，
// 那正是收尾唯一有意义的时刻 —— 服务端 reviveTerminalTask 会把它拉回
// waiting_user 再推进（worktree_finish_stage.go）。原断言钉的是「终态不给」，
// 与新需求直接矛盾，所以这里换掉钉的东西，不是删了换绿灯。
// 换来的是必须补上「有 agent 段」这道门：收尾段继承上一段的 agent/模型，
// 没有可继承的服务端会 409 —— 给一个必然失败的按钮比不给更糟。
assert.match(
  panel,
  /const canFinishWorktree = task\?\.create_worktree === true\s*&&\s*!!task\?\.worktree_path\s*&&\s*task\?\.worktree_missing !== true\s*&&\s*hasAgentStage\s*&&\s*!finishActive;/,
  "the finish button requires a live worktree and an agent stage to inherit from; terminal tasks are NOT withheld",
);
assert.doesNotMatch(
  panel,
  /const canFinishWorktree[^;]*isTerminalKanbanTask/,
  "the finish button must not be gated on terminal status — a finished task holding an unfinished worktree is exactly the case worth finishing",
);
assert.match(
  panel,
  /const hasAgentStage = \(task\?\.stages \|\| \[\]\)\.some\(\(stage\) => stage\.role === "agent"\);/,
  "the agent-stage gate must exist, mirroring worktree_finish_stage.go's lastAgentStage precondition",
);
assert.match(
  card,
  /const canFinishWorktree = worktreeEnabled && !worktreeMissing && hasWorktreePath && hasAgentStage && !finishActive;/,
  "the card button must match the panel exactly: same gates, same terminal exemption",
);
assert.match(card, /onMove\(task, "finish-worktree"\)/, "the card button must dispatch finish-worktree");
assert.match(app, /action === "finish-worktree"/, "App must handle the finish-worktree card action");

// 收尾和重建是两个键，长在同一处但不是一回事：混起来会让人以为「重建」也把活合回去了。
assert.match(panel, /<TaskFinishWorktreeIcon \/>/, "the panel must render the finish icon");
assert.match(card, /<TaskFinishWorktreeIcon \/>/, "the card must render the finish icon");
assert.match(read("src/app/taskIcons.tsx"), /export function TaskFinishWorktreeIcon\(\)/, "the finish icon must exist");

// ── 契约：接口与文案 ──
assert.match(
  tasks,
  /export class FinishWorktreeConflict extends Error \{[\s\S]{0,200}conflictFiles: string\[\]/,
  "the conflict must be a distinct class carrying the file list",
);
assert.match(
  tasks,
  /if \(error instanceof APIError && error\.status === 409\)/,
  "409 must be translated back into a conflict; going through protectedJSON keeps the E2EE/account-gone handling",
);
assert.doesNotMatch(
  tasks,
  /finish-worktree[\s\S]{0,200}await fetch\(/,
  "the finish call must go through protectedJSON, not a bare fetch (that would skip E2EE and the account-gone path)",
);
for (const key of [
  "task.finishWorktree",
  "task.finishWorktreeDone",
  "task.finishWorktreeConflict",
  "task.finishWorktreeConflictHint",
  "task.finishWorktreeOrphan",
]) {
  assert.match(zh, new RegExp(`"${key.replace(/\./g, "\\.")}":`), `zh-CN must define ${key}`);
  assert.match(en, new RegExp(`"${key.replace(/\./g, "\\.")}":`), `en-US must define ${key}`);
}

// 残留目录文案必须插进路径，否则「列出来」等于没列。
assert.match(zh, /"task\.finishWorktreeOrphan": "残留目录（未删除）：\{path\}"/, "the orphan line must interpolate the path");

test("both locales keep the same finish-worktree key set", () => {
  const keysOf = (src) => new Set([...src.matchAll(/"(task\.finishWorktree[A-Za-z]*)":/g)].map((m) => m[1]));
  const zhKeys = keysOf(zh);
  const enKeys = keysOf(en);
  assert.deepEqual([...zhKeys].sort(), [...enKeys].sort(), "zh-CN and en-US must not drift on the finish-worktree keys");
});

console.log("worktree-finish.test.mjs: OK");
