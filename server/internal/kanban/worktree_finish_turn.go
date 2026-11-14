package kanban

import (
	"context"
	"errors"
	"strings"
	"time"
)

// worktree 收尾的 agent 那一半：让任务会话自己 commit + merge，服务端接着清场。
//
// 为什么不直接一键收尾：agent 的活没 commit 时，服务端拿着工作树去 merge 等于白干
// （合了个空）；有未提交改动时 `git worktree remove` 又会拒绝，卡在「合并已完成但拆不掉」。
// commit 哪些文件是成品、哪些是半成品，只有 agent 知道 —— wt-finish.sh 也是这个分工
// （它把 commit 交给 agent 自己，只把「拆 worktree / 删分支」留给自己做）。
//
// 对话模型下这就是**一轮普通回合**：不追加任何阶段，也不写 StageRun。回合成功
// （末尾出现完成标记）之后由服务端钩子接着调清场。

// finishTurnPromptTemplate 是收尾回合给 agent 的指令。
//
// 命令一律用「」包起来而不是反引号：本串是 Go 原始字符串（反引号本身是定界符），
// 再用反引号就得层层转义，可读性会烂掉。
//
// 刻意不在 prompt 里塞 worktree / 主 checkout 的绝对路径：让 agent 自己
// 「git worktree list」找主 checkout 更可靠 —— 那是 git 的权威答案，不会因为
// 路径推断错了而合到别的地方。
const finishTurnPromptTemplate = `这一轮是 worktree 收尾：把本 worktree 里的活提交并合并回主干。

你现在位于一个 git linked worktree 内。按顺序做完下面几步：

1. 先搞清楚自己在哪：跑「git worktree list」和「git status」，确认当前 worktree 路径，
   以及主干仓库（列表里路径最短的那个通常是主 checkout，但要自己核对它的分支名
   是不是 main，不要凭猜）。

2. 看清楚要提交什么：「git status --porcelain」和「git diff」。只提交**这个任务真正
   做出来的成品改动**。以下两类绝不提交：
   - 构建产物与依赖目录：node_modules/、dist/、build/、*.log、*.tmp、*.swp、.env 及其变体
   - mindfs 自己的状态目录（.mindfs/ 等）与本地临时文件

3. 有需要提交的内容时，**显式列出文件路径**再提交（不要用「git add -A」或「git add .」）：
   「git add -- <路径1> <路径2> ...」然后「git commit -m "<type>: <简短描述>"」，
   type 用 Conventional Commits（feat / fix / refactor / docs / test / chore / perf / ci）。
   一个文件都没有要提交（「git status」干净）就跳过这步，别造空提交。

4. 合并到主干：先切到主 checkout 目录、切到 main，再把本 worktree 所在分支合进去，
   典型做法是「git -C <主checkout路径> checkout main」之后
   「git -C <主checkout路径> merge --no-ff <本分支名>」。
   合并提交信息里用单引号包住分支名，避免和命令行的双引号打架。

   先跑一次「git -C <主checkout路径> merge-base --is-ancestor <本分支名> main」：
   返回 0 就说明改动全在主干里了（服务端多半已经替你合过），**跳过合并**直接进
   第 5 步 —— 再合一次只会多出一个 "Already up to date" 的空提交。

5. 收尾阶段到此为止：**不要**自己执行「git worktree remove」、「git worktree prune」
   或「git branch -d / -D」。拆目录、删分支、把这个会话搬回主 checkout 由 mindfs
   服务端接着做 —— 你去做会和服务端抢同一个目录。

遇到下列情况**停下来问用户**，不要自己拍板：
- 合并撞上冲突（CONFLICT / MERGE_HEAD 仍存在）：说明哪些文件冲突、你倾向怎么合，
  然后直接结束这一轮，不要输出完成标记。用户会在 mindfs 里看到并处理。
- 主 checkout 有未提交改动，**而且和本分支要合的东西有重叠**：合并会覆盖它，
  先报给用户。
  不重叠时（典型：别人留下的未跟踪文件，分支根本不碰那些路径）**照常合** ——
  git 自己会拦下真会覆盖的情况（"untracked working tree files would be
  overwritten by merge"），那时再按上面那条冲突处理。
- 不确定某个改动该不该进仓库、或者提交信息拿不准：问，不要猜。

顺利做完（或者本来就没什么可提交、且已经在主干里了）之后，报告你实际做了什么
（提交了哪些文件、合并到哪个提交），最后单独一行输出 ` + "`" + finishTurnDoneMarker + "`" + `。`

// finishTurnDoneMarker 是收尾回合的完成标记。
//
// 为什么还需要标记，而不是「回合没报错就算成功」：agent 撞上冲突时是被要求
// **停下来问人**的，那一轮同样会正常结束、err 为 nil。没有标记的话服务端会把
// 一次中途停下的收尾当成成功，直接去拆目录、删分支 —— 而活还没合进去。
const finishTurnDoneMarker = "[WORKTREE-FINISH-DONE]"

// BeginFinishInput 是一次「发起收尾」的请求。
type BeginFinishInput struct {
	RootID string
	TaskID string
}

// BeginFinishWorktree 给任务发起一轮收尾回合（普通对话，不追加阶段）。
//
// 与 FinishTaskWorktree 的分工：那个是**清场**（合并 → 拆目录 → 删分支 → 清归属），
// 不碰未提交的活；这个是**发起**收尾流程 —— 让 agent 先把活提交合并掉，这一轮成功
// 之后由服务端钩子接着调清场。
//
// 拒绝的情形都返回 error，不静默改状态：
//   - 已在收尾中 → 不并发起第二轮
//   - 任务正在执行中 → 收尾回合会和正在跑的那一轮抢同一个 worktree
//   - 没有 worktree / worktree 目录已失效 → 无从收尾
//   - 没有直连执行端口 → 起不了回合，显式失败而不是留一个永远不动的「收尾中」
func (s *Service) BeginFinishWorktree(ctx context.Context, in BeginFinishInput) (TaskDetail, error) {
	store, task, err := s.loadForMove(ctx, in.RootID, in.TaskID)
	if err != nil {
		return TaskDetail{}, err
	}
	if !task.CreateWorktree {
		return TaskDetail{}, errors.New("该任务没有 worktree")
	}
	// 判据是「目录还在不在」而不是「字段空不空」：worktree 被手工删掉后字段仍非空，
	// 但那种情况没有可拆的目录，也没有能合并的分支（该点的是重建）。
	if p := strings.TrimSpace(task.WorktreePath); p == "" || !worktreeDirUsable(p) {
		return TaskDetail{}, errors.New("该任务的 worktree 已不存在，请先重建")
	}
	if err := s.assertNotRunning(ctx, store, task); err != nil {
		return TaskDetail{}, err
	}
	if s.directExecutor == nil {
		return TaskDetail{}, errors.New("收尾执行端口未配置，无法发起收尾回合")
	}
	release, err := s.acquireTaskFinish(in.RootID, in.TaskID)
	if err != nil {
		return TaskDetail{}, err
	}

	// 终态任务点收尾是唯一有意义的时刻（活干完了但树还留着）：先拉回 running，
	// 否则看板仍按「已完成」渲染，而实际正在合并。completed_at 一并清掉。
	if isTerminalStatus(task.Status) {
		revived := task
		revived.Status = StatusRunning
		revived.CompletedAt = ""
		revived.UpdatedAt = time.Now().UTC()
		if err := store.UpdateTask(ctx, revived); err != nil {
			release()
			return TaskDetail{}, err
		}
		if detail, err := store.GetDetail(ctx, task.ID); err == nil && s.Runner != nil {
			s.Runner.TaskUpdated(task.RootID, detail)
		}
		task = revived
	}

	go s.runFinishTurn(context.WithoutCancel(ctx), in.RootID, task, release)
	return store.GetDetail(ctx, task.ID)
}

// runFinishTurn 跑收尾回合并在结束后按结果触发清场。
//
// 完成判据是「没有报错 + 末尾有完成标记」：撞冲突时 agent 被要求停下来问人，
// 那一轮同样正常结束，只看 err 会把中途停下当成成功。
func (s *Service) runFinishTurn(ctx context.Context, rootID string, task Task, release func()) {
	defer release()
	text, err := s.directExecutor.RunDirectActivity(ctx, task, finishTurnPromptTemplate)
	succeeded := err == nil && strings.Contains(text, finishTurnDoneMarker)

	store, storeErr := s.taskStore(rootID)
	if storeErr == nil {
		if err != nil {
			_ = s.recordTaskError(ctx, store, task, err.Error())
		} else if !succeeded {
			_ = s.recordTaskError(ctx, store, task, "收尾回合没有回报完成标记，需要你确认："+firstLine(text))
		}
		if !isTerminalStatus(task.Status) {
			_, _ = s.setTaskStatus(ctx, rootID, task.ID, StatusWaitingUser, "finish_finished", "", false)
		}
	}
	if !succeeded {
		return
	}
	s.mu.Lock()
	fn := s.finishTurnDone
	s.mu.Unlock()
	if fn == nil {
		return
	}
	// 回调要跑 git merge / worktree remove / pool.Close，几秒起步，绝不能挡在
	// 回合收尾路径上。
	go fn(rootID, task.ID, true)
}

// firstLine 取回复的第一行，用作错误摘要。
func firstLine(text string) string {
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			if len(line) > 200 {
				line = line[:200] + "…"
			}
			return line
		}
	}
	return "(空回复)"
}

// acquireTaskFinish 取得某个任务的收尾权。同一任务同一时刻只允许一个收尾流程。
func (s *Service) acquireTaskFinish(rootID, taskID string) (func(), error) {
	if s == nil {
		return func() {}, nil
	}
	key := strings.TrimSpace(rootID) + "\x00" + strings.TrimSpace(taskID)
	s.mu.Lock()
	if s.taskFinish == nil {
		s.taskFinish = map[string]*finishToken{}
	}
	if _, busy := s.taskFinish[key]; busy {
		s.mu.Unlock()
		return nil, errTaskFinishing
	}
	s.taskFinish[key] = &finishToken{}
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		delete(s.taskFinish, key)
		s.mu.Unlock()
	}, nil
}

// Finishing 报任务此刻是否在收尾流程里（供 UI 与服务端分流）。
func (s *Service) Finishing(rootID, taskID string) bool {
	if s == nil {
		return false
	}
	key := strings.TrimSpace(rootID) + "\x00" + strings.TrimSpace(taskID)
	s.mu.Lock()
	defer s.mu.Unlock()
	_, busy := s.taskFinish[key]
	return busy
}

// SetFinishTurnDone 挂上「收尾回合成功之后」的回调。装配期调一次。
func (s *Service) SetFinishTurnDone(fn func(rootID, taskID string, succeeded bool)) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.finishTurnDone = fn
	s.mu.Unlock()
}
