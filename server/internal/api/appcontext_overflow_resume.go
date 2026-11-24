package api

import (
	"strings"

	"mindfs/server/internal/api/usecase"
)

// ResumeSessionOverflowTurn 实现 usecase.SessionOverflowResumeRunner（G-QT）：
// 把一次「溢出压缩成功后重发」排进会话队列。
//
// 这里刻意走 submitSessionMessage 而不是直接调 SendMessage：排队 / 实时广播 /
// 子会话跟随 / 收尾驱动全都由既有那条路径装配，后台回合因此与用户手打的回合
// 表现一致。队列里已经有用户消息时返回 false 让调用方丢弃 —— 用户已经改主意了，
// 不该把旧内容插到他前面。
func (s *AppContext) ResumeSessionOverflowTurn(spec usecase.OverflowResumeSpec) bool {
	if s == nil {
		return false
	}
	job := overflowResumeJob(spec)
	if job.Key == "" || job.ReuseUserTurn == nil {
		return false
	}
	hub := s.GetSessionStreamHub()
	if !overflowResumeAcceptable(hub, job.Key) {
		return false
	}
	h := &WSHandler{AppContext: s}
	h.submitSessionMessage(job)
	return true
}

// overflowResumeAcceptable 是「现在能不能把重发排进去」的唯一判据（纯函数，可单测）：
// 队列里已经有用户消息时不能 —— 用户已经改主意了，旧内容不该插到他前面。
func overflowResumeAcceptable(hub *StreamHub, sessionKey string) bool {
	if hub == nil {
		return false
	}
	return !hub.HasQueuedSessionMessage(sessionKey)
}

// overflowResumeJob 是 spec → 队列任务的唯一转换点（纯函数，可单测）。
func overflowResumeJob(spec usecase.OverflowResumeSpec) sessionMessageJob {
	key := strings.TrimSpace(spec.Key)
	turnID := strings.TrimSpace(spec.TurnID)
	planMode := false
	if spec.PlanMode != nil {
		planMode = *spec.PlanMode
	}
	job := sessionMessageJob{
		RootID:          spec.RootID,
		RuntimeRootPath: spec.RuntimeRootPath,
		Key:             key,
		SessionType:     spec.SessionType,
		SessionName:     spec.SessionName,
		User: PendingUserMessage{
			Agent:       spec.Agent,
			Model:       spec.Model,
			Mode:        spec.Mode,
			Effort:      spec.Effort,
			FastService: spec.FastService,
			PlanMode:    planMode,
			Content:     spec.Content,
			Timestamp:   spec.UserTimestamp,
		},
		ClientCtx:      spec.ClientCtx,
		OverflowResume: true,
	}
	if turnID != "" && spec.TurnSeq > 0 {
		job.ReuseUserTurn = &usecase.UserTurnIdentity{Seq: spec.TurnSeq, TurnID: turnID}
	}
	return job
}
