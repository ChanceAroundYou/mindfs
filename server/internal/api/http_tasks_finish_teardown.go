package api

import (
	"context"
	"errors"
	"log"
	"strings"

	"mindfs/server/internal/api/usecase"
	"mindfs/server/internal/kanban"
)

// 收尾段成功之后的清场编排：拆 worktree → 删分支 → 把会话搬回主 checkout。
//
// 为什么在 api 层而不是 kanban 里：搬会话走的是 usecase.RepointSession，而
// kanban.Service 够不到 usecase.Service（AppContext 的字段里没有它）。
// api 层有现成的取法 —— (&HTTPHandler).service() 就是 &usecase.Service{Registry: AppContext}
// （http.go 的 service 方法），这里直接用 AppContext 造一份同样的。
//
// **顺序：先清场、后搬会话**，和直觉相反，但反过来会静默失败：
// RepointSession 最后一步会调 ClearTaskWorktree 清掉任务的 worktree_path
// （session_repoint.go 的 clearTaskWorktree）。而 FinishTaskWorktree 开头就判
// 「WorktreePath 为空 → 该任务还没有 worktree」直接返回 —— 于是目录不拆、分支不删，
// 还没有任何报错。这正是脚本把 repoint 放在 cleanup 之后的原因。
//
// 反过来还有一个好处：会话还钉在 worktree 上时它能被 kill 掉（repoint 第一步
// pool.Close）。此刻 agent 早已收工（收尾段 success 落库才走到这里），关的是一个
// 已经空闲的进程 —— 脚本要用 setsid + sleep 30 绕开的那个问题，在这里不存在。

// FinishWorktreeAndRepoint 清场 + 搬会话，并返回前端要显示的东西。
//
// 不返回 error：收尾段的成败已经由界面上的任务状态表达了，这里要做的是把
// 「实际做了什么」如实报回去 —— 失败也报，不吞。
func (s *AppContext) FinishWorktreeAndRepoint(rootID, taskID string) FinishTeardownReport {
	report := FinishTeardownReport{RootID: rootID, TaskID: taskID}
	svc, err := s.GetKanbanService()
	if err != nil {
		report.Error = err.Error()
		return report
	}
	// 搬会话要在清场**之前**把 key 读出来：清场第 4 步 ClearWorktreeRefs 只清 worktree
	// 归属，但 repoint 自己的 clearTaskWorktree 会清 main_session_key 之外的东西 ——
	// 顺序一旦调过来这里就拿不到 key 了。所以先读。
	detail, err := svc.GetTask(context.Background(), rootID, taskID)
	var sessionKey string
	if err == nil {
		sessionKey = strings.TrimSpace(detail.Task.MainSessionKey)
	}

	// ── 清场 ──
	result, finishErr := svc.FinishTaskWorktree(context.Background(), kanban.FinishWorktreeInput{
		RootID:       rootID,
		TaskID:       taskID,
		DeleteBranch: true,
		PruneOrphans: true,
	})
	report.Result = &result
	if finishErr != nil {
		var conflict *kanban.FinishWorktreeConflict
		if errors.As(finishErr, &conflict) {
			report.ConflictFiles = conflict.ConflictFiles
		}
		report.Error = finishErr.Error()
		// 清场失败（典型是 worktree 里有未提交改动、git 拒绝拆）时**不搬会话**：
		// 目录还在、会话就该留在那儿，硬搬过去会让用户在一个还在变化的工作树上继续聊。
		return report
	}

	// ── 搬会话 ──
	// 没有会话、或会话本来就不在 worktree 上：都不是错误。重复收尾必然走到这里。
	if sessionKey == "" {
		report.SessionNote = "该任务没有绑定会话，无需搬移"
		return report
	}
	uc := &usecase.Service{Registry: s}
	if _, repointErr := uc.RepointSession(context.Background(), usecase.RepointSessionInput{
		RootID: rootID,
		Key:    sessionKey,
	}); repointErr != nil {
		// 搬不动会话**不**回滚已经完成的清场：活已经合进主干、目录已经拆掉了，
		// 这时把会话搬回去是锦上添花，搬不过去也不该把成果变成错误。
		// 提示用户就行 —— 转录还在原处，可以单独再试一次 repoint。
		report.SessionWarning = repointErr.Error()
	}
	return report
}

// FinishTeardownReport 是收尾段跑完之后清场的结果。
type FinishTeardownReport struct {
	RootID string `json:"root_id"`
	TaskID string `json:"task_id"`
	// Result 是 FinishTaskWorktree 的原样结果（合了哪个提交、拆没拆目录、删没删分支、残留目录）。
	Result *kanban.FinishWorktreeResult `json:"result,omitempty"`
	// ConflictFiles 非空 = 合并撞上冲突，仓库停在 MERGE_HEAD 等人处理。
	ConflictFiles []string `json:"conflict_files,omitempty"`
	// Error 是清场本身的失败（与冲突分开：冲突要列文件，其它错只给一句）。
	Error string `json:"error,omitempty"`
	// SessionNote / SessionWarning 是搬会话的结果说明。Warning 不阻断清场。
	SessionNote    string `json:"session_note,omitempty"`
	SessionWarning string `json:"session_warning,omitempty"`
}

// BroadcastTaskFinishTeardown 把清场结论播给前端。
//
// 为什么必须有这一条：整个收尾流程里，前端唯一确定拿到的通知是 task.updated
// （发起点、阶段状态变化），清场本身是服务端的 goroutine 在后台跑的，谁也不通知它。
// 于是「收尾完成」这件事只表现为徽标换了个样子，用户没法区分「成了」和「炸了」——
// 而这两者的代价差着好几个数量级（代码合进主干 / 合并撞冲突停在 MERGE_HEAD）。
func (s *AppContext) BroadcastTaskFinishTeardown(rootID string, report FinishTeardownReport) {
	if s == nil {
		return
	}
	s.GetSessionStreamHub().BroadcastAll(WSResponse{
		Type: "task.finish_teardown",
		Payload: map[string]any{
			"root_id":         report.RootID,
			"task_id":         report.TaskID,
			"result":          report.Result,
			"conflict_files":  report.ConflictFiles,
			"error":           report.Error,
			"session_note":    report.SessionNote,
			"session_warning": report.SessionWarning,
		},
	})
}

// WireFinishStageTeardown 把收尾段完成回调挂到看板服务上。
//
// 回调**不能**直接跑清场：merge / worktree remove / pool.Close 要好几秒，
// 而它是被 kanban 的执行体 goroutine 调用的（notifyFinishStageOutcome 里已经 go 出去一层，
// 这里再加一层是为了把 panic 和耗时彻底挡在 kanban 之外）。
func (s *AppContext) WireFinishStageTeardown(svc *kanban.Service) {
	if svc == nil {
		return
	}
	svc.SetFinishStageFinished(func(rootID string, outcome kanban.FinishStageOutcome) {
		if !outcome.Succeeded {
			// 受阻 / 静默：什么都不做，让任务停在待审核等人处理。
			return
		}
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[kanban/finish] teardown panic root=%s task=%s: %v", rootID, outcome.Task.ID, r)
			}
		}()
		report := s.FinishWorktreeAndRepoint(rootID, outcome.Task.ID)
		// 清场把 worktree_path 清空了，前端光靠 task.updated 不知道「刚才那次收尾
		// 到底成没成」—— 它只看到徽标换了个样子。直接把结论播过去，界面才给得出
		// 一次「已完成 / 出错了」的反馈。
		s.BroadcastTaskFinishTeardown(rootID, report)
		switch {
		case report.Error != "":
			log.Printf("[kanban/finish] teardown failed root=%s task=%s err=%s", rootID, outcome.Task.ID, report.Error)
		case report.SessionWarning != "":
			log.Printf("[kanban/finish] teardown done but session not moved root=%s task=%s: %s",
				rootID, outcome.Task.ID, report.SessionWarning)
		default:
			log.Printf("[kanban/finish] teardown done root=%s task=%s", rootID, outcome.Task.ID)
		}
	})
}
