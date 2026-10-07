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

// ── 孤儿目录：自己的整个删，别人的只列不删 ──
// 2026-10-07 重做：收尾要删掉**自己的**整个目录（含 git 不跟踪的 .mindfs/ .omc/
// .claude/），否则 .worktree/ 会越积越多；但**别人的**孤儿目录仍然只列不删 ——
// 里面可能有别的任务正在用的东西，删了不可恢复。
//
// 判据只认白名单（.mindfs/ .omc/ .claude/），不来自任何外部输入：含用户文件的
// 目录绝不能自动删。
assert.match(
  gitviewFinish,
  /func RemoveWorktreeDir\(worktreePath string\) \(bool, error\)/,
  "the task's own worktree directory must be deletable after git lets go of it",
);
assert.match(
  gitviewFinish,
  /func RemoveToolStateOnlyOrphanDirs\(ctx context.Context, mainDir string\) \(\[\]string, error\)/,
  "orphan pruning must be limited to tool-state-only shells",
);
assert.match(
  gitviewFinish,
  /func isToolStateOnlyDir\(dir string\) bool/,
  "the orphan auto-delete decision must be a whitelist check, not a blanket delete",
);
// 自动删只能走白名单那条路，不能对任意孤儿目录调 RemoveOrphanDir(…, true)。
assert.doesNotMatch(
  kanbanFinish,
  /RemoveOrphanDir\([a-zA-Z]+, true\)/,
  "no orphan directory may be force-deleted outside the tool-state-only path",
);
assert.match(kanbanFinish, /Orphans = orphans/, "orphans must be reported back to the caller");
assert.match(kanbanFinish, /RemovedOrphans = removed/, "auto-removed shells must be reported so the user can see what disappeared");

// ── 附件迁移：拆目录之前必须搬 ──
// 用户在 worktree 会话里传的附件落在 <worktree>/.mindfs/upload/，不搬就跟着目录消失。
assert.match(
  gitviewFinish,
  /func MigrateWorktreeUploads\(worktreePath, mainDir string\) \(\[\]string, error\)/,
  "uploads must be migrated before the directory is deleted",
);
assert.match(
  kanbanFinish,
  /MigrateWorktreeUploads\(worktreePath, mainDir\)/,
  "the migration must run before the teardown, not after",
);
const migrateAt = kanbanFinish.indexOf("MigrateWorktreeUploads(worktreePath, mainDir)");
const removeDirAt = kanbanFinish.indexOf("RemoveWorktreeDir(worktreePath)");
assert.ok(migrateAt > 0, "FinishTaskWorktree must call the upload migration");
assert.ok(migrateAt > 0 && removeDirAt > migrateAt, "uploads must be moved before the directory is deleted");
assert.match(kanbanFinish, /MigratedUploads = migrated/, "migrated uploads must be reported back");

// ── 机械优先：能直接清场就不等 agent ──
// 以前只要有 agent 段就一定先跑 agent，于是「agent 早把活合完了、只差机械清场」的
// 任务要白等一轮、还多一个空提交。现在服务端先做一次只读判定。
assert.match(
  kanbanFinish,
  /func \(s \*Service\) PlanFinishWorktree\(ctx context.Context, rootID, taskID string\) \(FinishPlan, error\)/,
  "the finish decision must be a read-only plan, not a blind agent run",
);
assert.match(
  kanbanFinish,
  /CanMergeCleanly\(ctx, mainDir, branch, target\)/,
  "the plan must ask git whether the merge would conflict, without touching the repo",
);
assert.match(
  gitviewFinish,
  /func CanMergeCleanly\(ctx context.Context, mainDir, branch, target string\) \(MergeFeasibility, error\)/,
  "the conflict check must live in gitview so it can be unit-tested against a real repo",
);
assert.match(
  gitviewFinish,
  /"merge-tree", "--write-tree", target, branch/,
  "the conflict check must use a read-only merge-tree dry run, never a real merge",
);
// 主 checkout 干不干净**不参与**判定：用户明确「只要机械合并能成就行」。
//
// 只在**函数体**里找：注释里那句「MergeBranch 会自己拦下（MainCheckoutDirtyError）」
// 是解释为什么不提前拦，拿整段源码跑正则会把那句说明也算成命中。
const planBody = kanbanFinish.slice(
  kanbanFinish.indexOf("func (s *Service) PlanFinishWorktree("),
  kanbanFinish.indexOf("func (s *Service) TaskFinishStageIndex("),
);
assert.ok(planBody.length > 0, "PlanFinishWorktree must exist to assert against");
assert.doesNotMatch(
  planBody,
  /MainCheckoutDirty/,
  "the plan must not gate on the main checkout being clean — only the merge succeeding matters",
);
// 判定顺序必须把「worktree 有没提交的活」放在「合并可行性」之前：前者是用户自己的
// 活，后者只是 git 的机械结论 —— 顺序反了会把「有活没提交」误报成「合并会冲突」。
const uncommittedAt = planBody.indexOf("worktree 里还有没提交的改动");
const feasibilityAt = planBody.indexOf("CanMergeCleanly(");
assert.ok(uncommittedAt > 0 && feasibilityAt > uncommittedAt,
  "uncommitted work must be checked before merge feasibility — it is the user's own work, not a git verdict");

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
//
// **收尾中也不拦截**（2026-10-05 用户要求）：按钮必须一直可点且幂等。agent 那半
// 跑完却卡住时，再点一次让服务端直接做机械清场（后端按「分支是否已合进主干」分流，
// 见 handleKanbanTaskBeginFinish）；换成转圈等于把唯一的出路藏起来，任务就此永远
// 转下去 —— 用户实测的「收尾一直转、点不动、也结束不了」就是这个形状。
assert.match(
  panel,
  /const canFinishWorktree = task\?\.create_worktree === true\s*&&\s*!!task\?\.worktree_path\s*&&\s*task\?\.worktree_missing !== true;/,
  "the finish button requires a live worktree; terminal and finishing tasks are NOT withheld",
);
assert.doesNotMatch(
  panel,
  /const canFinishWorktree[^;]*isTerminalKanbanTask/,
  "the finish button must not be gated on terminal status — a finished task holding an unfinished worktree is exactly the case worth finishing",
);
assert.doesNotMatch(
  panel,
  /const canFinishWorktree[^;]*!finishActive/,
  "the finish button must stay clickable while the finish stage runs — that is the moment the mechanical cleanup is needed",
);
assert.doesNotMatch(
  panel,
  /finishActive \? \(\s*<span[\s\S]{0,400}?<TaskQueuedSpinnerIcon \/>/,
  "the panel must not swap the finish button for a spinner — the button is the only way out of a stuck finish",
);
// 「有 agent 段」这条判据 2026-10-07 从**两侧**去掉了：它会让「有 worktree 但没有
// agent 段」的任务永远拿不到收尾键，而那种任务恰恰最需要它（没有 agent 去 commit，
// 只能服务端直接机械清场）。服务端已同步：begin-finish 的 ③.5 分支无 agent 段时
// 直接清场，不再 409。两侧必须一起改 —— 只改一侧就是「给一个必然 409 的按钮」。
assert.doesNotMatch(
  panel,
  /const canFinishWorktree[^;]*hasAgentStage/,
  "the panel's finish gate must not require an agent stage — the server tears down mechanically when there is none",
);
assert.doesNotMatch(
  card,
  /const canFinishWorktree[^;]*hasAgentStage/,
  "the card's finish gate must not require an agent stage either — both sides must agree",
);
assert.match(
  card,
  /const canFinishWorktree = worktreeEnabled && !worktreeMissing && hasWorktreePath;/,
  "the card button must match the panel exactly: same gates, same terminal and finishing exemptions",
);
assert.doesNotMatch(
  card,
  /worktreeTagState === "finishing" \? \(\s*<span[\s\S]{0,400}?<TaskQueuedSpinnerIcon \/>/,
  "the card must not swap the finish button for a spinner either",
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
  "task.finishWorktreeDirRemoved",
  "task.finishWorktreeDirKept",
  "task.finishWorktreeOrphanRemoved",
  "task.finishWorktreeUploadsMoved",
  "task.finishWorktreeAlreadyFinished",
  "task.finishWorktreePlanUncommitted",
  "task.finishWorktreePlanConflict",
  "task.finishWorktreePlanFiles",
]) {
  assert.match(zh, new RegExp(`"${key.replace(/\./g, "\\.")}":`), `zh-CN must define ${key}`);
  assert.match(en, new RegExp(`"${key.replace(/\./g, "\\.")}":`), `en-US must define ${key}`);
}

// 残留目录文案必须插进路径，否则「列出来」等于没列。
assert.match(zh, /"task\.finishWorktreeOrphan": "残留目录（未删除）：\{path\}"/, "the orphan line must interpolate the path");

// ── 前端：新字段要真的接出来 ──
// 类型声明了但没人读，等于没加 —— 用户看不到「目录删了 / 附件搬了 / 为什么交给 agent」。
const worktreeService = read("src/services/task/worktree.ts");
assert.match(
  worktreeService,
  /worktree_dir_removed\?: boolean/,
  "the result type must carry whether the directory itself was deleted",
);
assert.match(
  worktreeService,
  /migrated_uploads\?: string\[\]/,
  "the result type must carry the migrated uploads",
);
assert.match(
  worktreeService,
  /export type FinishPlan = \{/,
  "the plan must be a typed response field, not an untyped any",
);
assert.match(
  worktreeService,
  /plan\?: FinishPlan/,
  "begin-finish must return the plan so the UI can explain the decision",
);
assert.match(
  read("src/app/useRealtimeEvents.ts"),
  /result\?\.worktree_dir_removed[\s\S]{0,200}finishWorktreeDirRemoved/,
  "the teardown receipt must announce that the directory itself is gone",
);
assert.match(
  read("src/app/useRealtimeEvents.ts"),
  /result\?\.migrated_uploads[\s\S]{0,200}finishWorktreeUploadsMoved/,
  "the teardown receipt must announce the attachments that were moved back",
);
assert.match(
  read("src/app/useRealtimeEvents.ts"),
  /payload\.note[\s\S]{0,200}lines\.push\(note\)/,
  "the teardown receipt must surface the idempotent-skip note",
);
assert.match(
  read("src/App.tsx"),
  /res\.plan\?\.reason[\s\S]{0,200}planLine/,
  "the finish button must explain why it deferred to the agent",
);

test("both locales keep the same finish-worktree key set", () => {
  const keysOf = (src) => new Set([...src.matchAll(/"(task\.finishWorktree[A-Za-z]*)":/g)].map((m) => m[1]));
  const zhKeys = keysOf(zh);
  const enKeys = keysOf(en);
  assert.deepEqual([...zhKeys].sort(), [...enKeys].sort(), "zh-CN and en-US must not drift on the finish-worktree keys");
});

console.log("worktree-finish.test.mjs: OK");
