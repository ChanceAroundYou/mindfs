package kanban

import (
	"context"
	"errors"
	"log"
	"strings"
)


func (s *Service) beginRunAdmission(rootID, taskID string) (func(), error) {
	key := rootID + "\x00" + taskID
	token := &admitToken{}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, finishing := s.taskFinish[key]; finishing {
		return nil, errTaskFinishing
	}
	// 已经有一次启动在途（另一个动词正在改状态）：让它先走完，别让两处同时改。
	if _, admitted := s.taskAdmit[key]; admitted {
		return nil, errTaskAlreadyAdmitted
	}
	s.taskAdmit[key] = token
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if held, ok := s.taskAdmit[key]; ok && held == token {
			delete(s.taskAdmit, key)
		}
	}, nil
}


func (s *Service) acquireTaskFinish(rootID, taskID string) (func(), error) {
	key := rootID + "\x00" + taskID
	// 令牌：release 只认自己那一份。两个并发收尾如果共用一个 bool，先完成的那个
	// release 会把还在跑的另一个的锁一起放开，RunTask 就能对着一个正在被拆的目录
	// 起来。所以锁的「所有权」必须是可辨认的一份，不是一个开关。
	token := &finishToken{}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.taskRun[key] {
		return nil, errors.New("任务正在执行中，先停止再收尾")
	}
	// 有人正在为这个任务启动执行体（beginRunAdmission 的窗口里）：那一次会把状态
	// 改成 running 再起 agent。等它走完再收尾，否则收尾插进去就是「状态已改、
	// agent 起不来」。
	if _, admitted := s.taskAdmit[key]; admitted {
		return nil, errors.New("该任务正在启动中，稍后再收尾")
	}
	// 已经在收尾：第二个收尾不能并着跑 —— 它们会对着同一个 worktree 一起
	// merge / remove，其中一个的 release 还会提前放开另一个的锁。
	if _, busy := s.taskFinish[key]; busy {
		return nil, errors.New("该任务正在收尾中，请等它结束")
	}
	s.taskFinish[key] = token
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		// 只删自己那份：别人的收尾不该被自己的 release 解锁。
		if held, ok := s.taskFinish[key]; ok && held == token {
			delete(s.taskFinish, key)
		}
	}, nil
}


func (s *Service) KickPending(rootID string) {
	if s == nil || s.Runner == nil {
		return
	}
	rootID = strings.TrimSpace(rootID)
	if rootID == "" {
		return
	}
	go func() {
		store, err := s.taskStore(rootID)
		if err != nil {
			return
		}
		tasks, err := store.ListTasks(context.Background(), ListTasksOptions{})
		if err != nil {
			return
		}
		for _, task := range tasks {
			if isTerminalStatus(task.Status) || task.Status == StatusWaitingUser || task.Status == StatusPending || task.Status == StatusPaused {
				continue
			}
			if strings.TrimSpace(task.AuxFlags.SessionError) != "" {
				continue
			}
			if runErr := s.RunTask(rootID, task.ID); runErr != nil {
				log.Printf("[kanban] task.kick.skipped root=%s task=%s err=%v", rootID, task.ID, runErr)
			}
		}
	}()
}
