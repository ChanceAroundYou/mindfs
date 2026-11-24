package usecase

import (
	"log"
	"strings"
	"time"
)

// OverflowResumeSpec 是「上下文溢出 → 压缩在途 → 压缩成功后自动重发」的待办。
//
// 它保存的是**那条失败回合**的全部输入，而不是重发时现场再猜一遍：模型、模式、
// 计划模式、客户端上下文都会影响提示词，重发必须与第一次逐项一致，否则用户看到的
// 是「同一条消息但回答前提变了」。
//
// TurnSeq / TurnID 是被复用回合的身份。重发不新增 user 行，块流与前端气泡因此都只有
// 一份；这也是它敢在「每次首次发送都失败」的场景里默认开启的前提。
type OverflowResumeSpec struct {
	RootID          string
	RuntimeRootPath string
	Key             string
	SessionType     string
	SessionName     string
	Agent           string
	Model           string
	Mode            string
	Effort          string
	FastService     string
	PlanMode        *bool
	Content         string
	UserTimestamp   time.Time
	ClientCtx       ClientContext
	TurnSeq         int
	TurnID          string
}

// UserTurnIdentity 是复用已落盘用户回合所需的身份：Seq 是 user 行的 seq，
// TurnID 是它的 mindfs_id。两者都来自第一次发送时写入的那一行。
type UserTurnIdentity struct {
	Seq    int
	TurnID string
}

// SessionOverflowResumeRunner 是 Registry 的可选能力：把一次自动重发排进会话队列。
//
// 放在 api 层实现（而不是 usecase 里直接再调一次 SendMessage）有两个原因：
//  1. SendMessage 整轮持会话发送锁，在失败调用里同步等待压缩再重发会死锁；
//  2. 后台回合的实时 UI（正文、工具卡、子会话、任务状态跟随）全部由 WS handler 装配，
//     绕过它用户只会看到一个不动的界面。
//
// 返回 false 表示这次重发被丢弃（会话忙、队列里已有用户消息、实现不可用）。
type SessionOverflowResumeRunner interface {
	ResumeSessionOverflowTurn(spec OverflowResumeSpec) bool
}

// overflowResumeRunner 取可选能力；空实现、非 api 实现都返回 nil。
func (s *Service) overflowResumeRunner() SessionOverflowResumeRunner {
	if s == nil || s.Registry == nil {
		return nil
	}
	runner, _ := s.Registry.(SessionOverflowResumeRunner)
	return runner
}

// markOverflowResume 在判定「这一轮是终端溢出、且压缩在途」后登记待办。
func (s *Service) markOverflowResume(in sendFailureContext) {
	tracker := s.hygieneTracker()
	if tracker == nil || in.Current == nil {
		return
	}
	if strings.TrimSpace(in.Content) == "" || strings.TrimSpace(in.TurnID) == "" {
		// 没有内容或没有回合身份就不登记：否则重发只能猜一份内容，或者再写一条 user 行。
		return
	}
	harness := hygieneHarnessForSession(in.Current)
	planMode := in.PlanMode
	spec := OverflowResumeSpec{
		RootID:          in.RootID,
		RuntimeRootPath: in.RootAbs,
		Key:             in.SessionKey,
		SessionType:     in.Current.Type,
		SessionName:     in.Current.Name,
		Agent:           harness,
		Model:           in.Model,
		Mode:            in.Mode,
		Effort:          in.Effort,
		FastService:     in.FastService,
		PlanMode:        &planMode,
		Content:         in.Content,
		UserTimestamp:   in.UserTimestamp,
		ClientCtx:       in.ClientCtx,
		TurnSeq:         in.TurnSeq,
		TurnID:          in.TurnID,
	}
	tracker.MarkOverflowResume(in.RootID, in.SessionKey, spec)
	log.Printf("[session] turn.overflow.resume.armed root=%s session=%s turn=%s content_bytes=%d",
		in.RootID, in.SessionKey, in.TurnID, len(in.Content))
}

// consumeOverflowResume 是压缩成功的收口：会话仍空闲才把待办交给 runner。
//
// 忙就丢：说明用户已经用自己的回合接手了，拿旧内容插回去只会打乱顺序。
func (s *Service) consumeOverflowResume(rootID, sessionKey string) {
	tracker := s.hygieneTracker()
	runner := s.overflowResumeRunner()
	if tracker == nil {
		return
	}
	spec, ok := tracker.TakeOverflowResume(rootID, sessionKey)
	if !ok {
		return
	}
	if runner == nil {
		log.Printf("[session] turn.overflow.resume.drop root=%s session=%s reason=no_runner", rootID, sessionKey)
		return
	}
	if SessionTurnActive(rootID, sessionKey) {
		log.Printf("[session] turn.overflow.resume.drop root=%s session=%s reason=session_busy", rootID, sessionKey)
		return
	}
	if !runner.ResumeSessionOverflowTurn(spec) {
		log.Printf("[session] turn.overflow.resume.drop root=%s session=%s reason=runner_declined", rootID, sessionKey)
		return
	}
	log.Printf("[session] turn.overflow.resume.scheduled root=%s session=%s turn=%s", rootID, sessionKey, spec.TurnID)
}

// dropOverflowResume 在两处调用：压缩失败，或用户自己发起了新回合。
func (s *Service) dropOverflowResume(rootID, sessionKey, reason string) {
	tracker := s.hygieneTracker()
	if tracker == nil {
		return
	}
	if tracker.DropOverflowResume(rootID, sessionKey) {
		log.Printf("[session] turn.overflow.resume.drop root=%s session=%s reason=%s", rootID, sessionKey, reason)
	}
}

// maybeDropOverflowResumeForUserTurn 是「用户又说话了」的唯一判据：
// 只有用户自己发起的回合才作废待办；压缩回合与重发回合不是用户发起。
func (s *Service) maybeDropOverflowResumeForUserTurn(in SendMessageInput) {
	if in.hygieneCompact || in.OverflowResume {
		return
	}
	s.dropOverflowResume(in.RootID, in.Key, "user_turn")
}

// reuseUserTurnIdentity 判定这一轮是否复用已落盘的用户回合（不新增 user 行）：
// 用户看到的仍然是原来那条消息，助手正文接在它下面。
//
// 纯函数：落盘点留在 SendMessage 里（user 行必须先于 runtime.SendMessage 写下去），
// 这里只回答「要不要跳过那次落盘」。
func reuseUserTurnIdentity(in SendMessageInput) (int, string, bool) {
	reuse := in.ReuseUserTurn
	if reuse == nil || reuse.Seq <= 0 {
		return 0, "", false
	}
	turnID := strings.TrimSpace(reuse.TurnID)
	if turnID == "" {
		return 0, "", false
	}
	return reuse.Seq, turnID, true
}
