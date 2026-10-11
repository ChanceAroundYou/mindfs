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
// **顺序：先清场、后搬会话**。清场第 4 步 ClearWorktreeRefs 会把任务的 worktree 归属
// 清空，而搬会话要读 main_session_key —— 所以先把 key 读出来兜住。
//
// 之所以敢让这两件事各管各的（2026-10-01 起）：repoint 曾经顺手调 ClearTaskWorktree，
// 于是「搬会话」和「任务收工」被焊死 —— 一次纯搬会话的 repoint 把还在用的任务目录记录
// 清成了空，卡片随即显示「已收尾」。现在 repoint 只清会话侧归属，任务侧由清场自己清，
// 两边语义对等，谁也不替谁代劳。

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
	// 搬会话要在清场**之前**把 key 读出来：清场第 4 步 ClearWorktreeRefs 会清 worktree
	// 归属。key 读不出来也不要紧 —— 那意味着没有会话可搬，清场照做，最后报一句。
	detail, err := svc.GetTask(context.Background(), rootID, taskID)
	var sessionKey string
	alreadyFinished := false
	if err == nil {
		sessionKey = strings.TrimSpace(detail.Task.MainSessionKey)
		// 「建过树、路径已清」= 清场第 4 步 ClearWorktreeRefs 已经跑过 —— 这次是重复
		// 收尾。跳过清场（下层对空路径是严格报错的，那是为了守住「先清归属再拆目录」
		// 的顺序不变量），但**仍然往下走搬会话**：会话可能还钉着那个已经不存在的目录。
		alreadyFinished = detail.Task.CreateWorktree &&
			detail.Task.WorktreeBuilt &&
			strings.TrimSpace(detail.Task.WorktreePath) == ""
	}

	// ── 清场 ──
	if alreadyFinished {
		report.Note = "该任务已经收过尾，本次没有可清场的活"
	} else {
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
				report.Output = conflict.Output
			}
			var dirty *kanban.FinishWorktreeDirty
			if errors.As(finishErr, &dirty) {
				report.DirtyFiles = dirty.Files
			}
			var blocked *kanban.FinishWorktreeUserChanges
			if errors.As(finishErr, &blocked) {
				report.UserChanges = blocked.Files
			}
			report.Error = finishErr.Error()
			// 清场失败（典型是 worktree 里有未提交改动、git 拒绝拆）时**不搬会话**：
			// 目录还在、会话就该留在那儿，硬搬过去会让用户在一个还在变化的工作树上继续聊。
			return report
		}
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
	// Output 是 git 自己的原话（冲突时给「怎么解」提供上下文）。
	Output string `json:"output,omitempty"`
	// DirtyFiles 非空 = 主 checkout 有未提交改动，git 不敢替用户合并。
	DirtyFiles []string `json:"dirty_files,omitempty"`
	// UserChanges 非空 = 合并已成功、但 worktree 里还有用户没提交的东西，git 拒绝拆目录。
	UserChanges []string `json:"user_changes,omitempty"`
	// Error 是清场本身的失败（与冲突分开：冲突要列文件，其它错只给一句）。
	Error string `json:"error,omitempty"`
	// Note 是「这次没做什么、为什么」的说明（幂等跳过等）。与 Error 分开：
	// Note 非空但 Error 为空 = 一切正常，只是本来就没活可干。
	Note string `json:"note,omitempty"`
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
			"root_id":        report.RootID,
			"task_id":        report.TaskID,
			"result":         report.Result,
			"conflict_files": report.ConflictFiles,
			"output":         report.Output,
			// 清单必须跟到 WS：异步那条路（收尾段跑完的钩子）没有 HTTP 响应可带，
			// 清单丢了就只能报一句没有清单的错，而用户恰恰需要那份清单去处理。
			"dirty_files":     report.DirtyFiles,
			"user_changes":    report.UserChanges,
			"error":           report.Error,
			"note":            report.Note,
			"session_note":    report.SessionNote,
			"session_warning": report.SessionWarning,
		},
	})
}

// WireSessionRunningProbe 把「会话是否在回复」的探针挂到看板服务上。
//
// 收尾要拆掉 agent 的 cwd，判据必须是「agent 此刻是否真的在动」—— 而任务状态骗人：
// 任务可以长时间停在 waiting_user（等审核），也可能卡在 running 却早就没 agent 了。
// 会话在不在回复是唯一直接读数（StreamHub 按 pendingSessions 的 Active 位算）。
//
// 与 WireFinishStageTeardown 分开两个函数：那个管「段成功后清场」，这个管「收尾前的
// 准入判据」，生命周期不同，装配点也不一样。
func (s *AppContext) WireSessionRunningProbe(svc *kanban.Service) {
	if svc == nil {
		return
	}
	svc.SetSessionRunningProbe(func(sessionKey string) bool {
		hub := s.GetSessionStreamHub()
		if hub == nil {
			return false
		}
		return hub.IsSessionReplying(sessionKey)
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
