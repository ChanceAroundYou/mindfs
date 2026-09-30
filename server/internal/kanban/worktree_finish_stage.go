package kanban

import (
	"context"
	"errors"
	"strings"
)

// 收尾段：让 agent 自己做 commit + merge，服务端接着做清场。
//
// 为什么不直接一键收尾：agent 的活没 commit 时，服务端拿着工作树去 merge 等于白干
// （合了个空）；有未提交改动时 `git worktree remove` 又会拒绝，卡在「合并已完成但拆不掉」。
// commit 哪些文件是成品、哪些是半成品，只有 agent 知道 —— wt-finish.sh 也是这个分工
// （它把 commit 交给 agent 自己，只把「拆 worktree / 删分支」留给自己做）。
//
// 分两段的另一个理由：agent 撞上冲突时必须能停下来问人。这一步只能由 agent 做
// —— 冲突要解释、要给取舍判断，服务端只能说「有冲突」，没法替用户决定。

// finishStagePromptTemplate 是收尾段给 agent 的指令。
//
// 命令一律用「」包起来而不是反引号：本串是 Go 原始字符串（反引号本身是定界符），
// 再用反引号就得层层转义，可读性会烂掉。
//
// 刻意不在 prompt 里塞 worktree / 主 checkout 的绝对路径：promptValues 只提供
// previous_input / task_initial_input / task_number（service.go 的 promptValues），
// 为一段性的提示词去扩那张表不值得。让 agent 自己「git worktree list」找主 checkout
// 更可靠 —— 那是 git 的权威答案，不会因为路径推断错了而合到别的地方。
const finishStagePromptTemplate = `这一段是 worktree 收尾：把本 worktree 里的活提交并合并回主干。

你现在位于一个 git linked worktree 内。按顺序做完下面几步：

1. 先搞清楚自己在哪：跑「git worktree list」和「git status」，
   确认当前 worktree 路径，以及主干仓库（列表里路径最短的那个通常是主 checkout，
   但要自己核对它的分支名是不是 main，不要凭猜）。

2. 看清楚要提交什么：「git status --porcelain」和「git diff」。
   只提交**这个任务真正做出来的成品改动**。以下两类绝不提交：
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

   如果 main 上已经有别的分支是本分支的祖先，说明改动已经在里面了，直接进入第 5 步。

5. 收尾阶段到此为止：**不要**自己执行「git worktree remove」、
   「git worktree prune」或「git branch -d / -D」。
   拆目录、删分支、把这个会话搬回主 checkout 由 mindfs 服务端接着做 ——
   你去做会和服务端抢同一个目录。

遇到下列情况**停下来问用户**，不要自己拍板：
- 合并撞上冲突（CONFLICT / MERGE_HEAD 仍存在）：说明哪些文件冲突、你倾向怎么合，
  然后用受阻标记收尾。用户会在 mindfs 里看到并处理。
- 主 checkout 有未提交改动：合并会覆盖它，先报给用户。
- 不确定某个改动该不该进仓库、或者提交信息拿不准：问，不要猜。

顺利做完（或者本来就没什么可提交、且已经在主干里了）之后，报告你实际做了什么
（提交了哪些文件、合并到哪个提交），最后单独一行输出本段的完成标记。`

// finishStageName 是收尾段在阶段流里显示的名字。
const finishStageName = "收尾 worktree"

// BeginFinishInput 是一次「发起收尾」的请求。
type BeginFinishInput struct {
	RootID string
	TaskID string
}

// BeginFinishWorktree 给任务追加一段收尾阶段并让它跑起来。
//
// 与 FinishTaskWorktree 的分工：那个是**清场**（合并 → 拆目录 → 删分支 → 清归属），
// 不碰未提交的活；这个是**发起**收尾流程 —— 追加一段 agent 工作让 agent 先把自己
// 的活提交合并掉，这段成功之后由服务端钩子（见 FinishStageTeardown）接着调清场。
//
// 拒绝的情形都返回 error，不静默改状态：
//   - 已经有收尾段 → 不追加第二段（否则反复点会堆出一串收尾段）
//   - 任务正在执行中 → 收尾段会和正在跑的那一段抢同一个 worktree
//   - 终态 / 没有 worktree / worktree 目录已失效 → 无从收尾
func (s *Service) BeginFinishWorktree(ctx context.Context, in BeginFinishInput) (TaskDetail, error) {
	_, task, err := s.loadForMove(ctx, in.RootID, in.TaskID)
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
	if isTerminalStatus(task.Status) {
		return TaskDetail{}, errors.New("任务已结束")
	}
	for _, stage := range task.Stages {
		if strings.TrimSpace(stage.Kind) == StageKindWorktreeFinish {
			return TaskDetail{}, errors.New("该任务已在收尾流程中")
		}
	}
	// AddStage 只在 waiting_user 态才真正推进并起 agent（service.go 的 AddStage），
	// 其余状态会把段追加到尾巴上、不执行也不报错 —— 那是个静默空操作。
	// 所以这里把状态要求显式写出来，而不是指望 AddStage 兜住。
	if task.Status != StatusWaitingUser {
		return TaskDetail{}, errors.New("任务正在执行中，等它停下再收尾")
	}

	// 继承上一个 agent 段的 agent/模型，跟看板里「追加一段」的行为一致
	// （inheritAgentStage 的同一套规则）。**不能**在这里给硬编码默认值：后端没有
	// 「默认 agent」这个概念，runAgentStage 碰到空 agent 会直接 failTask
	// （agent stage requires agent），而那会表现成一次莫名其妙的失败。
	// 没有上一段可继承就当场拒绝，让用户先加一段 agent 工作。
	previous := lastAgentStage(task.Stages)
	if strings.TrimSpace(previous.Agent) == "" {
		return TaskDetail{}, errors.New("该任务还没有 agent 阶段，无法自动收尾：请先加一段 agent 工作再收尾")
	}
	stage := StageTemplate{
		Name:               finishStageName,
		Role:               RoleAgent,
		Agent:              previous.Agent,
		Model:              previous.Model,
		SessionReusePolicy: SessionReuseTaskMain,
		PromptTemplate:     finishStagePromptTemplate,
		Kind:               StageKindWorktreeFinish,
		// AutoAdvance 保持 false：这段成功之后要停在「待审核」，由服务端钩子接手清场，
		// 不能让 executeTask 把它当普通段自动推进到下一段去。
		AutoAdvance: false,
	}
	return s.AddStage(ctx, AddStageInput{RootID: in.RootID, TaskID: in.TaskID, Stage: stage})
}

// lastAgentStage 返回最后一个 agent 段的定义（没有则零值）。
func lastAgentStage(stages []StageTemplate) StageTemplate {
	for i := len(stages) - 1; i >= 0; i-- {
		if stages[i].Role == RoleAgent {
			return stages[i]
		}
	}
	return StageTemplate{}
}

// IsFinishStage 报这一段是不是收尾段。
func IsFinishStage(stage StageTemplate) bool {
	return strings.TrimSpace(stage.Kind) == StageKindWorktreeFinish
}

// FinishStageOutcome 是一次收尾段跑完之后的结论。
type FinishStageOutcome struct {
	// Succeeded=true 表示收尾段**成功**（agent 回报了完成标记）：这时该接着清场。
	Succeeded bool
	// Task 是收尾段跑完之后的任务快照（清场要用的字段都在上面）。
	Task Task
}

// FinishStageFinished 在收尾段跑完之后被调用一次，**无论成功还是受阻**。
//
// 有了它，api 层才能在成功时接着跑清场，在受阻时知道「什么都不会发生」。
// 挂在 kanban.Service 上而不是 Runner 接口上：Runner 是「造 agent / 跑 agent」的
// 能力（4 个方法，两个测试 fake 都实现了），加方法要连带改两处 fake；而这是
// 「一段跑完之后要做什么」，不是造 agent 的能力。nil 时整条路径安静跳过。
type FinishStageFinishedFunc func(rootID string, outcome FinishStageOutcome)

// SetFinishStageFinished 挂上收尾段完成回调。装配期调一次。
func (s *Service) SetFinishStageFinished(fn FinishStageFinishedFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finishStageFinished = fn
}

// finishStageTeardownDue 报「这次收尾段跑完，要不要接着清场」。
//
// 抽成纯函数是为了能直接单测：这个判据曾经挂在一个只有「成功」一条路走得到的
// 分支后面，导致其中的 running 守卫从来没被任何测试碰到过（变异测试确认：去掉
// 它，全套测试照样绿）。
func finishStageTeardownDue(task Task, stageRunStatus string) bool {
	index := task.CurrentStageIndex
	if index < 0 || index >= len(task.Stages) {
		return false
	}
	// 指针不在收尾段上 → 是别的段刚跑完，普通 agent 段收工也满足下面两条。
	if !IsFinishStage(task.Stages[index]) {
		return false
	}
	// 还在跑 → 这一段的结果还没定。executeTask 正常路径会先经 waitForUser 落回
	// waiting_user 再返回，所以这里平时是假；但补跑循环里 executeTask 可能以别的
	// 方式退出（error 提前返回），那时 task.Status 仍是 running。
	if task.Status == StatusRunning {
		return false
	}
	// 只有 success 才清场。受阻（停下来问用户）和静默（agent 没回报）都不动 ——
	// 清场等于替用户把没做完的活合进主干。
	return stageRunStatus == StageStatusSuccess
}

// notifyFinishStageOutcome 在执行体收尾之后查一次任务状态，必要时触发回调。
func (s *Service) notifyFinishStageOutcome(rootID, taskID string) {
	s.mu.Lock()
	fn := s.finishStageFinished
	s.mu.Unlock()
	if fn == nil {
		return
	}
	store, err := s.taskStore(rootID)
	if err != nil {
		return
	}
	task, err := store.GetTask(context.Background(), taskID)
	if err != nil {
		return
	}
	index := task.CurrentStageIndex
	if index < 0 || index >= len(task.Stages) {
		return
	}
	run, err := store.LatestStageRun(context.Background(), taskID, index)
	if err != nil {
		return
	}
	if !finishStageTeardownDue(task, run.Status) {
		return
	}
	// 回调本身要跑 git merge / worktree remove / pool.Close，几秒起步，
	// 绝不能挡在执行体的收尾路径上 —— 挡住会让 RunTask 的 goroutine 一直占着
	// taskRun 标记，之后任何收尾都撞上「任务正在执行中」。
	go fn(rootID, FinishStageOutcome{Succeeded: true, Task: task})
}
