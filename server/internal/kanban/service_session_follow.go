package kanban

import (
	"context"
	"log"
	"strings"
)

// 会话推进跟随（W4）。
//
// turn_settled 只带得出「某个会话走完一轮」这个事实；Router 还需要知道两件
// 更细的事，才能判断这一轮值不值得汇报、是不是该收尾：
//
//   - answered_question：这一轮里用户真的回答了提问卡。答题本身不重启一轮
//     （答案直接喂回正在跑的 ask_user），所以只能在作答那一刻打标记，回合结束
//     时取走；
//   - out_of_band：这一轮是用户在会话视图里自己推的，不是任务/工作流发起的。
//     这类回合开始时要把 waiting_user 的任务头推进到 running，结束时再放回
//     waiting_user，否则任务面板整段时间都停在上一个状态（线上 #68）。
//
// 标记只在进程内，按 root + session 隔离：一个回合一条，取走即失效。

func sessionFollowKey(rootID, sessionKey string) string {
	return strings.TrimSpace(rootID) + "\x00" + strings.TrimSpace(sessionKey)
}

// NoteSessionAnsweredQuestion 记录「这个会话刚刚答过提问卡」。由 api 的
// 答题路径在写入答案之后调用（见 appcontext_task_prompt.go）。
func (s *Service) NoteSessionAnsweredQuestion(rootID, sessionKey string) {
	if s == nil || strings.TrimSpace(sessionKey) == "" {
		return
	}
	key := sessionFollowKey(rootID, sessionKey)
	s.sessionFollowMu.Lock()
	if s.sessionAnswered == nil {
		s.sessionAnswered = make(map[string]bool)
	}
	s.sessionAnswered[key] = true
	s.sessionFollowMu.Unlock()
}

func (s *Service) takeSessionAnsweredQuestion(rootID, sessionKey string) bool {
	if s == nil {
		return false
	}
	key := sessionFollowKey(rootID, sessionKey)
	s.sessionFollowMu.Lock()
	defer s.sessionFollowMu.Unlock()
	if !s.sessionAnswered[key] {
		return false
	}
	delete(s.sessionAnswered, key)
	return true
}

// BeginOutOfBandSessionTurn 把「用户正在任务绑定的会话里自己推一轮」投影到任务头：
// 只有任务当前停在 waiting_user 才推进到 running 并打 out_of_band 标记。返回
// true 表示这一轮确实由本方法认领（结算时负责放回 waiting_user）。
//
// 任务已在 running（正被工作流/直连回合驱动）或已暂停/终态时不认领：那不是
// out-of-band，也不该被本方法改状态。
func (s *Service) BeginOutOfBandSessionTurn(ctx context.Context, rootID, sessionKey string) bool {
	if s == nil || strings.TrimSpace(sessionKey) == "" {
		return false
	}
	store, err := s.taskStore(rootID)
	if err != nil {
		return false
	}
	taskID, err := store.TaskIDForSession(ctx, sessionKey)
	if err != nil || taskID == "" {
		return false
	}
	task, err := store.GetTask(ctx, taskID)
	if err != nil || task.Status != StatusWaitingUser {
		return false
	}
	if err := store.UpdateTaskStatus(ctx, taskID, StatusRunning, false); err != nil {
		return false
	}
	key := sessionFollowKey(rootID, sessionKey)
	s.sessionFollowMu.Lock()
	if s.sessionOutOfBand == nil {
		s.sessionOutOfBand = make(map[string]bool)
	}
	s.sessionOutOfBand[key] = true
	s.sessionFollowMu.Unlock()
	s.broadcastTask(ctx, store, taskID)
	return true
}

func (s *Service) takeSessionOutOfBandTurn(rootID, sessionKey string) bool {
	if s == nil {
		return false
	}
	key := sessionFollowKey(rootID, sessionKey)
	s.sessionFollowMu.Lock()
	defer s.sessionFollowMu.Unlock()
	if !s.sessionOutOfBand[key] {
		return false
	}
	delete(s.sessionOutOfBand, key)
	return true
}

// restoreOutOfBandTask 把 out-of-band 回合开始前被推进到 running 的任务放回
// waiting_user。只动仍在 running 的任务：工作流终态投影或用户操作已经改过状态时
// 不覆盖。失败只记日志（调用方已经拿到了结算结果，不该因展示状态失败而丢弃）。
func (s *Service) restoreOutOfBandTask(ctx context.Context, store *TaskStore, taskID string) {
	if s == nil || store == nil || strings.TrimSpace(taskID) == "" {
		return
	}
	task, err := store.GetTask(ctx, taskID)
	if err != nil {
		log.Printf("[kanban/follow] restore out-of-band task %s: %v", taskID, err)
		return
	}
	if task.Status != StatusRunning {
		return
	}
	if err := store.UpdateTaskStatus(ctx, taskID, StatusWaitingUser, false); err != nil {
		log.Printf("[kanban/follow] restore out-of-band task %s status: %v", taskID, err)
		return
	}
	s.broadcastTask(ctx, store, taskID)
}
