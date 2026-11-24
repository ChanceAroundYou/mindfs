package kanban

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"
)

// SessionArchiver 是「只给会话打归档标记」的端口。
//
// 与 usecase.ArchiveSession 刻意分开：那条路径会把后代会话真删掉（归档区按项目
// 平铺，子会话留着只会变孤儿行）。任务归档的级联语义是用户裁定的 A ——
// **只打标记、不删后代**，所以这里只提供标记能力，删不掉任何东西。
//
// 入参同时带 taskID 和种子 key：实现方要用 taskID 找齐「名下的全部会话」，再用
// 种子 key 兜住 task_id 为空的老数据，最后按父子链把子会话一起收进来。
type SessionArchiver interface {
	ArchiveSessions(ctx context.Context, rootID, taskID string, sessionKeys []string) error
}

// SetSessionArchiver 由 api 层在装配时注入；nil 时归档只改任务标记（单测、
// 老装配路径），不级联会话。
func (s *Service) SetSessionArchiver(a SessionArchiver) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.sessionArchiver = a
	s.mu.Unlock()
}

func (s *Service) sessionArchiverPort() SessionArchiver {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionArchiver
}

type ArchiveTaskInput struct {
	RootID   string
	TaskID   string
	Archived bool
}

// ArchiveTask 归档/取消归档一个任务。
//
// 归档：先落任务标记（终态校验在 store 里），再给相关会话打归档标记；会话标记
// 失败只记日志 —— 用户要的是「这张卡收起来」，某个会话早被删掉不该让归档失败。
// 取消归档：只清任务自己的标记，**不**反过来解开会话 —— 会话可能是用户自己归档的。
func (s *Service) ArchiveTask(ctx context.Context, in ArchiveTaskInput) (TaskDetail, error) {
	rootID := strings.TrimSpace(in.RootID)
	taskID := strings.TrimSpace(in.TaskID)
	if rootID == "" || taskID == "" {
		return TaskDetail{}, errors.New("root_id and task_id required")
	}
	store, err := s.taskStore(rootID)
	if err != nil {
		return TaskDetail{}, err
	}
	if _, err := store.SetTaskArchived(ctx, taskID, in.Archived); err != nil {
		return TaskDetail{}, err
	}
	if in.Archived {
		s.archiveRelatedSessions(ctx, store, rootID, taskID)
		s.maybeAutoFinishWorktree(ctx, rootID, taskID)
	}
	return store.GetDetail(ctx, taskID)
}

// archiveRelatedSessions 把属于这个任务的会话一起打归档标记。
// 只标记、不删除：JSONL 一行不动，深链接仍能打开，取消任务归档时也不解开会话。
func (s *Service) archiveRelatedSessions(ctx context.Context, store *TaskStore, rootID, taskID string) {
	archiver := s.sessionArchiverPort()
	if archiver == nil {
		return
	}
	keys, err := store.TaskRelatedSessionKeys(ctx, taskID)
	if err != nil {
		log.Printf("[kanban/archive] task=%s session-keys error=%v", taskID, err)
		return
	}
	// 即使种子 key 为空也要调用：实现方还会按 taskID 找齐名下会话、按父子链收
	// 子会话。种子为空只代表「没有可枚举的列」，不代表「没有会话」。
	if err := archiver.ArchiveSessions(ctx, rootID, taskID, keys); err != nil {
		log.Printf("[kanban/archive] task=%s session-archive error=%v keys=%v", taskID, err, keys)
		return
	}
	log.Printf("[kanban/archive] task=%s marked sessions=%d", taskID, len(keys))
}

// SweepAutoArchivedTasks 把超过 TaskArchiveAfter 的终态任务真正归档（写
// archived_at 并级联会话标记）。返回本轮归档的任务数；单个 root 失败只记日志，
// 不影响其它 root。
//
// 「真写」是刻意的：界面上靠 created_at/updated_at 现算「看起来归档了」的话，
// 取消归档、后台排序、工作台过滤三处会各算各的；一个持久标记只有一个答案。
func (s *Service) SweepAutoArchivedTasks(ctx context.Context) int {
	if s == nil || s.Roots == nil {
		return 0
	}
	total := 0
	for _, root := range s.Roots.ListRoots() {
		store, err := s.taskStore(root.ID)
		if err != nil {
			log.Printf("[kanban/archive] root=%s store error=%v", root.ID, err)
			continue
		}
		cutoff := store.nowTime().Add(-TaskArchiveAfter)
		due, err := store.ListTasksDueForArchive(ctx, cutoff, taskArchiveSweepLimit)
		if err != nil {
			log.Printf("[kanban/archive] root=%s list error=%v", root.ID, err)
			continue
		}
		archived := 0
		for _, task := range due {
			if _, err := store.SetTaskArchived(ctx, task.ID, true); err != nil {
				log.Printf("[kanban/archive] root=%s task=%s archive error=%v", root.ID, task.ID, err)
				continue
			}
			s.archiveRelatedSessions(ctx, store, root.ID, task.ID)
			s.broadcastArchive(ctx, store, root.ID, task.ID)
			archived++
		}
		if archived > 0 {
			log.Printf("[kanban/archive] root=%s auto-archived=%d", root.ID, archived)
		}
		total += archived
	}
	return total
}

// broadcastArchive 把自动归档的结果推给已打开的看板。
//
// 手动归档走 HTTP 响应 + 前端那一侧的重拉，自动归档没有这两样：不推的话，卡片
// 会一直留在「完成」列直到用户手动刷新页面 —— 用户看到的就是「陈旧任务自动归档
// 没实现」。与手动归档同一条事件（task.updated + 完整 detail），前端不用为
// 「谁触发的归档」分叉。
func (s *Service) broadcastArchive(ctx context.Context, store *TaskStore, rootID, taskID string) {
	if s.Runner == nil {
		return
	}
	detail, err := store.GetDetail(ctx, taskID)
	if err != nil {
		log.Printf("[kanban/archive] task=%s detail error=%v", taskID, err)
		return
	}
	s.Runner.TaskUpdated(rootID, detail)
}

// StartArchiveSweeper 起一个低频后台扫描（启动即跑一次，之后每小时）。
// ctx 取消即退出，不另留 goroutine；同一个 Service 重复调用只会起一次。
func (s *Service) StartArchiveSweeper(ctx context.Context) {
	if s == nil {
		return
	}
	s.archiveSweepOnce.Do(func() {
		go func() {
			if n := s.SweepAutoArchivedTasks(ctx); n > 0 {
				log.Printf("[kanban/archive] startup sweep archived=%d", n)
			}
			ticker := time.NewTicker(taskArchiveSweepInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					s.SweepAutoArchivedTasks(ctx)
				}
			}
		}()
	})
}
