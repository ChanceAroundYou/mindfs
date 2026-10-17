package kanban

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"
)

type UpdateTaskInput struct {
	RootID             string
	TaskID             string
	Input              string
	CreateWorktree     *bool
	WorktreeBranchMode string
	WorktreeBranch     string
}

func (s *Service) Next(ctx context.Context, in MoveInput) (TaskDetail, error) {
	store, err := s.taskStore(in.RootID)
	if err != nil {
		return TaskDetail{}, err
	}
	task, err := store.GetTask(ctx, strings.TrimSpace(in.TaskID))
	if err != nil {
		return TaskDetail{}, err
	}
	if isTerminalStatus(task.Status) {
		return store.GetDetail(ctx, task.ID)
	}
	// 末段等待用户＝收尾，直接完成而不是报 stage out of range。
	if task.Status == StatusWaitingUser && task.CurrentStageIndex >= len(task.Stages)-1 {
		if latest, latestErr := store.LatestStageRun(ctx, task.ID, task.CurrentStageIndex); latestErr == nil {
			if latest.Status != StageStatusSuccess {
				_ = store.UpdateStageRunStatus(ctx, latest.ID, StageStatusApproved)
			}
		}
		if err := s.finishTask(ctx, store, task, StatusSuccess, "completed", in.Reason); err != nil {
			return TaskDetail{}, err
		}
		detail, err := store.GetDetail(ctx, task.ID)
		if err == nil && s.Runner != nil {
			s.Runner.TaskUpdated(task.RootID, detail)
		}
		return detail, err
	}
	release, admErr := s.beginRunAdmission(in.RootID, in.TaskID)
	if admErr != nil {
		return store.GetDetail(ctx, in.TaskID)
	}
	defer release()
	detail, err := s.moveRelative(ctx, in, 1, "user_approved", StageStatusApproved)
	if err == nil {
		if runErr := s.RunTask(detail.Task.RootID, detail.Task.ID); runErr != nil {
			return TaskDetail{}, runErr
		}
	}
	return detail, err
}

// RunNow：待开始直接开跑；等待用户视同验收推进到下一段。
func (s *Service) RunNow(ctx context.Context, in MoveInput) (TaskDetail, error) {
	store, err := s.taskStore(in.RootID)
	if err != nil {
		return TaskDetail{}, err
	}
	task, err := store.GetTask(ctx, strings.TrimSpace(in.TaskID))
	if err != nil {
		return TaskDetail{}, err
	}
	switch task.Status {
	case StatusWaitingUser:
		detail, nerr := s.Next(ctx, in)
		// 推进失败时错误已经记在任务上了（moveRelative 里的 recordTaskError）：
		// 「立即执行」不该因此失败，返回当前详情让前端显示那条错误。
		// 典型是 worktree 目录已被删除 —— 用户点按钮要拿到的是卡片上那句人话，
		// 而不是 HTTP 400 加一串底层报错。
		if nerr != nil {
			return store.GetDetail(ctx, task.ID)
		}
		return detail, nil
	case StatusPending:
		// 未开始态点「开始」= 批准当前 user 段并跑起来。已经停在最后一段时
		// 没有下一段可进（moveRelative 会报 out of range），直接执行体推进即可。
		// 曾经这里对 pending 一律只调 RunTask 不推进阶段，多段任务会卡在待审核，
		// 用户得再点一次「执行」。
		if task.CurrentStageIndex >= len(task.Stages)-1 {
			break
		}
		detail, nerr := s.Next(ctx, in)
		// worktree 建不起来时 moveRelative 会报错，但错误已经记在任务上了：
		// 「开始」不该因此失败，返回当前详情让前端显示那条错误。
		// 判据是「此刻有没有可用 worktree」而不是「字段空不空」：目录被删时字段仍非空，
		// 但那不等于有树可用，错误同样已经记在任务上了。
		if nerr != nil && task.CreateWorktree && !worktreeDirUsable(task.WorktreePath) {
			return store.GetDetail(ctx, task.ID)
		}
		return detail, nerr
	case StatusRunning, StatusPaused:
		// 正在跑 / 已暂停：点「立即执行」推进不了任何一段。原先这里静默回 200 +
		// 未变的详情，前端 apply 完看着「什么都没发生」—— 用户无从判断是按钮坏了
		// 还是任务本来就动不了（2026-10-03 任务 26 卡死时就是这个形态）。
		// 明确报错：调用方已经有 reportError 弹窗的通路（App.handleMoveKanbanTask），
		// 重复点击也拿得到同样的反馈。
		return TaskDetail{}, errors.New("task is running or paused: 任务正在执行或已暂停，先暂停/恢复再推进")
	}
	// 准入检查在建事件之前：收尾期间进来的请求不该留下一条「点过立即执行」而
	// 实际什么都没发生的流水（那会让用户以为按钮生效了）。
	release, admErr := s.beginRunAdmission(in.RootID, task.ID)
	if admErr != nil {
		return store.GetDetail(ctx, task.ID)
	}
	defer release()
	_ = store.AddEvent(ctx, TaskEvent{
		ID:        newID("event"),
		TaskID:    task.ID,
		Type:      "run_now",
		Payload:   eventPayload(map[string]any{"reason": strings.TrimSpace(in.Reason)}),
		CreatedAt: time.Now().UTC(),
	})
	if task.CreateWorktree {
		if _, werr := s.ensureTaskWorktree(ctx, store, task); werr != nil {
			_ = s.recordTaskError(ctx, store, task, "", werr.Error())
			return store.GetDetail(ctx, task.ID)
		}
	}
	detail, err := store.GetDetail(ctx, task.ID)
	if err == nil {
		if runErr := s.RunTask(detail.Task.RootID, detail.Task.ID); runErr != nil {
			return TaskDetail{}, runErr
		}
	}
	return detail, err
}

func (s *Service) Pause(ctx context.Context, in MoveInput) (TaskDetail, error) {
	return s.setTaskStatus(ctx, in.RootID, in.TaskID, StatusPaused, "paused", in.Reason, false)
}

func (s *Service) Resume(ctx context.Context, in MoveInput) (TaskDetail, error) {
	// 先问能不能起，再改状态。反过来（先置 running 再 RunTask）会在收尾撞车时
	// 留下一个永远 running、却没有执行体的任务 —— 没有 agent 会再碰它。
	release, admErr := s.beginRunAdmission(in.RootID, in.TaskID)
	if admErr != nil {
		return s.GetTask(ctx, in.RootID, in.TaskID)
	}
	defer release()
	detail, err := s.setTaskStatus(ctx, in.RootID, in.TaskID, StatusRunning, "resumed", in.Reason, false)
	if err == nil {
		if runErr := s.RunTask(detail.Task.RootID, detail.Task.ID); runErr != nil {
			return TaskDetail{}, runErr
		}
	}
	return detail, err
}

// beginRunAdmission 为「要起执行体的动词」开一段准入窗口：查没有收尾 → 放动词改状态
// → RunTask 接手。整段由返回的 release 结束。
//
// 为什么需要一把真的锁而不是「查一下」：查和改之间不是原子的。查通过之后、
// 状态还没改的这一刻，一个收尾请求可以抢进来把 worktree 拆掉；等动词改完状态
// 再去 RunTask 已经被拒 —— 结果是「状态 running、没有执行体」，没有 agent 会再
// 碰它，看起来像卡死。把准入和状态变更绑在一段互斥区间里，收尾要么进不来，
// 要么等这段走完。
// errTaskAlreadyAdmitted 内部用：同一任务的启动准入已被别人占着。
var errTaskAlreadyAdmitted = errors.New("该任务正在启动中")

func (s *Service) Fail(ctx context.Context, in MoveInput) (TaskDetail, error) {
	return s.setTaskStatus(ctx, in.RootID, in.TaskID, StatusFail, "stage_failed", in.Reason, true)
}

func (s *Service) Cancel(ctx context.Context, in MoveInput) (TaskDetail, error) {
	return s.setTaskStatus(ctx, in.RootID, in.TaskID, StatusCancelled, "cancelled", in.Reason, true)
}

// Complete：任何非终态任务都可一键完成。
// 不看会话/worktree 是否还在——会话被删、worktree 丢了，任务状态照样能人工收尾，
// 否则这些任务会永远卡在 running 没法推进。
func (s *Service) Complete(ctx context.Context, in MoveInput) (TaskDetail, error) {
	store, task, err := s.loadForMove(ctx, in.RootID, in.TaskID)
	if err != nil {
		return TaskDetail{}, err
	}
	if isTerminalStatus(task.Status) {
		return store.GetDetail(ctx, task.ID)
	}
	if latest, runErr := store.LatestStageRun(ctx, task.ID, task.CurrentStageIndex); runErr == nil {
		if latest.Status != StageStatusSuccess {
			_ = store.UpdateStageRunStatus(ctx, latest.ID, StageStatusApproved)
		}
	}
	if err := s.finishTask(ctx, store, task, StatusSuccess, "completed", in.Reason); err != nil {
		return TaskDetail{}, err
	}
	return store.GetDetail(ctx, task.ID)
}

func (s *Service) Status(ctx context.Context, rootID, taskID string) (TaskDetail, error) {
	return s.GetTask(ctx, rootID, taskID)
}

// DeleteTask 删掉一张任务卡 —— 只有卡片，worktree / 分支 / 会话一律不碰。
//
// 为什么要有它：看板上的卡只会累积。终态任务留着是为了看历史，看完得能清掉，
// 而「取消」只把状态改成 cancelled，卡片照旧留在板上。
//
// **只在任务没在跑时允许**。执行体还活着的话，它随后对那张行的写入会落空
// （UPDATE 打到不存在的行上，静默 no-op），或者把卡又插回来 —— 与「删掉」相反。
// 判据复用收尾那条 assertNotRunning（会话在不在回复是金标准，见 worktree_finish.go），
// 不另造一套。taskRun 那道锁也要查：已经排进执行队列、还没落 running 状态的窗口里，
// assertNotRunning 看不出东西来。
func (s *Service) DeleteTask(ctx context.Context, in MoveInput) error {
	store, task, err := s.loadForMove(ctx, in.RootID, in.TaskID)
	if err != nil {
		return err
	}
	if err := s.assertNotRunning(ctx, store, task); err != nil {
		return err
	}
	key := strings.TrimSpace(in.RootID) + "\x00" + strings.TrimSpace(in.TaskID)
	s.mu.Lock()
	executing := s.taskRun[key]
	s.mu.Unlock()
	if executing {
		return errors.New("任务正在执行中，先停止再删除")
	}
	return store.DeleteTask(ctx, task.ID)
}

func (s *Service) RunTask(rootID, taskID string) error {
	if s == nil || s.Runner == nil {
		return nil
	}
	rootID = strings.TrimSpace(rootID)
	taskID = strings.TrimSpace(taskID)
	if rootID == "" || taskID == "" {
		return nil
	}
	// 同一任务同时只允许一个执行体。Next/Resume/RunNow 等重复请求不应把同一 agent 阶段跑两次
	// （重复创建 agent 会话、重复消耗 token）。重复请求记为待补跑。
	key := rootID + "\x00" + taskID
	s.mu.Lock()
	// 正在收尾 worktree：不能起 agent。收尾会把 agent 的 cwd 拆掉，而这里起的话
	// agent 会在一个即将消失的目录里跑。宁可不跑，也不跑一个必死的。
	if _, finishing := s.taskFinish[key]; finishing {
		s.mu.Unlock()
		return errTaskFinishing
	}
	if s.taskRun[key] {
		s.taskPend[key] = true
		s.mu.Unlock()
		return nil
	}
	s.taskRun[key] = true
	s.mu.Unlock()
	go func() {
		for {
			if err := s.executeTask(context.Background(), rootID, taskID); err != nil {
				log.Printf("[kanban] task.execute.error root=%s task=%s err=%v", rootID, taskID, err)
			}
			// 收尾段跑完（成功或受阻）就在这里报给外层。**必须在 executeTask 返回之后、
			// 且在 taskRun 标记摘掉之后**：清场要抢 taskFinish 锁，而 acquireTaskFinish
			// 见到 s.taskRun[key] 就拒绝（它就是「还有执行体在跑」）。挂在 executeTask
			// 内部必然被这条锁拒掉 —— agent 收工那一刻执行体确实还占着这个标记。
			// 见 notifyFinishStageOutcome。
			s.notifyFinishStageOutcome(rootID, taskID)
			s.mu.Lock()
			// 执行期间又有请求进来 → 补跑一次（此时阶段多已 waiting_user，补跑不会重复执行 agent）。
			if s.taskPend[key] {
				delete(s.taskPend, key)
				s.mu.Unlock()
				continue
			}
			delete(s.taskRun, key)
			s.mu.Unlock()
			break
		}
	}()
	return nil
}

// acquireTaskFinish 独占这个任务的「收尾权」，收尾期间挡住新的执行。
//
// 为什么必须锁住整个流程而不只是开头查一次：查完「没在跑」到 merge/remove 之间
// 有几秒，任何 Next/RunNow/Rerun 都能在这窗口里把 agent 起来，而它的 cwd 就是
// 那个即将被拆掉的目录。所以锁要一直持到拆完、删完为止。
// KickPending 会启动时进入：仅兜底执行既存任务指针所在段落，不再有排队/槽位语义。
// 只对 pending/running 且未被任何执行体持有的任务触发一次 RunTask。
