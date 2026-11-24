package usecase

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"mindfs/server/internal/agent"
	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/session"
)

func isCanceledTurnError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return true
	}
	value := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(value, "context canceled") ||
		strings.Contains(value, "context cancelled") ||
		strings.Contains(value, "turn canceled") ||
		strings.Contains(value, "turn cancelled") ||
		strings.Contains(value, "cancelled")
}

type SendRecoveryInput struct {
	RootID             string
	SessionKey         string
	Manager            *session.Manager
	Current            *session.Session
	AgentName          string
	Model              string
	Mode               string
	Effort             string
	FastService        string
	PlanMode           bool
	RootAbs            string
	CurrentSession     agenttypes.Session
	Prompt             string
	SawAssistantChunk  bool
	SendWithAttachment func(agenttypes.Session, string) error
}

// terminalTurnReasons 是「这一轮已终止，重试同一个请求只会原样再失败，且不该
// 连坐回收运行时」的 harness 判据。
//
// Claude CLI 上下文用满时给的是 result="Prompt is too long" +
// terminal_reason="blocking_limit"（2026-11-24 线上 journal 实测）；
// prompt_too_long 是 SDK 的同义取值。它们**不能**塞进
// isNonRecoverableAgentError —— 那条路径会 KillAgentProcess，把该 agent
// 名下所有会话一起杀掉。这里只停本轮。
var terminalTurnReasons = map[string]struct{}{
	"blocking_limit":  {},
	"prompt_too_long": {},
}

func isTerminalTurnError(err error) bool {
	if err == nil {
		return false
	}
	if reason := agenttypes.TurnTerminalReason(err); reason != "" {
		_, ok := terminalTurnReasons[reason]
		return ok
	}
	// 旧版 CLI / 其它 harness 只把这句话写进错误文本，没有 terminal_reason。
	return strings.Contains(strings.ToLower(err.Error()), "prompt is too long")
}

const (
	terminalOverflowMessage = "上下文已满，本轮不会自动重试；请先压缩或清理上下文，再重新发送"
	// 溢出后已经排了一次有界压缩：告诉用户等自动重发，而不是让他再发一条。
	// 用户重试会先取消压缩回合（现场 16:29:02 的 session.cancel），进而把 G-QT
	// 的待办当成「用户新回合」丢掉，正是上一版自动重发没闭环的原因。
	terminalOverflowCompactingMessage = "上下文已满，本轮不会自动重试；已开始压缩，完成后会自动重发，无需手动发送"
)

// overflowCompactScheduledError 标记「终端溢出后已经补了一次有界压缩」。
// 只用于选用户文案，原始错误仍在链上 —— TurnTerminalReason 照旧能读出
// blocking_limit，日志与详情不受影响。
type overflowCompactScheduledError struct{ err error }

func (e *overflowCompactScheduledError) Error() string { return e.err.Error() }

func (e *overflowCompactScheduledError) Unwrap() error { return e.err }

// explainTerminalTurnError 把机器判据翻成用户能照做的下一步，同时用 %w
// 保留原始错误，日志与前端详情仍能读到 harness 的原文。
func explainTerminalTurnError(err error) error {
	if !isTerminalTurnError(err) {
		return err
	}
	var scheduled *overflowCompactScheduledError
	if errors.As(err, &scheduled) {
		return fmt.Errorf("%s（%w）", terminalOverflowCompactingMessage, err)
	}
	return fmt.Errorf("%s（%w）", terminalOverflowMessage, err)
}

// ensureOverflowCompact 在「这一轮已经因上下文用满失败」之后补一次有界压缩。
//
// 复用 hygiene 自动压缩那条 /compact 通道：等待会话空闲、30 分钟封顶、胶囊里
// 能看到压缩中与失败原因、失败后不再重试。这里**不改** hygiene 分级 —— notice
// 档仍然永不因「大」自动压缩；本次只处理已经撞墙之后的出口。
//
// 返回值表示「此刻确实有一次压缩在途（刚排上或原本就在跑）」，供文案二选一。
func (s *Service) ensureOverflowCompact(in sendFailureContext) bool {
	// OverflowResume：这一轮本身就是「压缩成功后自动重发」的回合。它再撞墙说明
	// 压完仍然放不下，再排一次压缩只会换来同样的结局 —— 一次为限，按普通终端失败收尾。
	if in.HygieneCompact || in.OverflowResume || in.Current == nil {
		return false
	}
	harness := hygieneHarnessForSession(in.Current)
	if !hygieneAppliesToSession(in.Current) || !hygieneAutoCompactSupported(harness) {
		return false
	}
	tracker := s.hygieneTracker()
	view := tracker.Snapshot(in.RootID, in.SessionKey, harness, 0, in.Current.LastContextWindow.ModelContextWindow)
	if view.AutoFailed {
		return false
	}
	// 压缩在途或刚排上，都登记重发待办：用户不用再手动发一次。
	s.markOverflowResume(in)
	if view.CompactPending {
		return true
	}
	tracker.MarkCompactPending(in.RootID, in.SessionKey)
	log.Printf("[session] turn.overflow.compact.schedule root=%s session=%s agent=%s", in.RootID, in.SessionKey, harness)
	go s.runHygieneAutoCompact(SendMessageInput{
		RootID:          in.RootID,
		RuntimeRootPath: in.RootAbs,
		Key:             in.SessionKey,
		Agent:           harness,
		Content:         hygieneCompactCommand,
		hygieneCompact:  true,
	})
	return true
}

// sendFailureContext 是一次回合失败后决定「补发 / 不补发 / 回收运行时」所需的
// 全部输入。抽出来是为了让 session_send.go 的主路径只留一句调用，避免继续膨胀。
type sendFailureContext struct {
	Err               error
	TurnCtx           context.Context
	RootID            string
	SessionKey        string
	AgentName         string
	Model             string
	Mode              string
	Effort            string
	FastService       string
	Manager           *session.Manager
	Current           *session.Session
	CurrentSession    agenttypes.Session
	AgentPool         *agent.Pool
	PlanMode          bool
	RootAbs           string
	Prompt            string
	SawAssistantChunk bool
	// 溢出自动重发（G-QT）需要的东西：那次失败回合的原始输入与回合身份。
	// 重发必须逐项复现第一轮的提示词前提，不能到重发时再猜。
	Content       string
	UserTimestamp time.Time
	ClientCtx     ClientContext
	TurnSeq       int
	TurnID        string
	// OverflowResume 为真表示失败的是重发回合本身：不再排压缩、不再登记重发。
	OverflowResume bool
	// HygieneCompact 为真表示失败的这一轮本身就是压缩回合：不能再排压缩。
	HygieneCompact     bool
	SendWithAttachment func(agenttypes.Session, string) error
	OnUpdate           func(agenttypes.Event)
}

// applySendFailure 按 planSendFailure 的判据执行失败后的动作，返回可能被替换的
// 运行时（补发成功时新会话）与最终错误。
func (s *Service) applySendFailure(in sendFailureContext) (agenttypes.Session, error) {
	sess := in.CurrentSession
	sendErr := in.Err
	plan := planSendFailure(sendErr, in.SawAssistantChunk)
	switch {
	case plan.RecycleRuntime:
		log.Printf("[session] turn.send.non_recoverable root=%s session=%s agent=%s action=fail_without_recovery err=%v", in.RootID, in.SessionKey, in.AgentName, sendErr)
		cancelRuntimeAfterNonRecoverableError(sess, in.AgentPool, in.AgentName, sendErr)
	case plan.Reason == "terminal_turn":
		// 上下文用满：重试同一个请求不会变，也不该连坐回收运行时 —— 同 agent
		// 名下别的会话是好的，KillAgentProcess 会把它们一起杀掉。
		log.Printf("[session] turn.send.terminal root=%s session=%s agent=%s action=fail_without_recovery reason=%s err=%v", in.RootID, in.SessionKey, in.AgentName, agenttypes.TurnTerminalReason(sendErr), sendErr)
		if s.ensureOverflowCompact(in) {
			sendErr = &overflowCompactScheduledError{err: sendErr}
		}
	case plan.Reason == "no_assistant_output":
		log.Printf("[session] turn.send.no_response root=%s session=%s agent=%s action=fail_without_recovery", in.RootID, in.SessionKey, in.AgentName)
	case plan.RecoverTurn:
		if in.OnUpdate != nil {
			in.OnUpdate(agenttypes.Event{
				Type: agenttypes.EventTypeRecovery,
				Data: agenttypes.RecoveryStatus{Message: "遇到错误，重试中..."},
			})
		}
		recoveredSess, recoveredErr := s.recoverAgentTurn(in.TurnCtx, SendRecoveryInput{
			RootID:             in.RootID,
			SessionKey:         in.SessionKey,
			Manager:            in.Manager,
			Current:            in.Current,
			AgentName:          in.AgentName,
			Model:              in.Model,
			Mode:               in.Mode,
			Effort:             in.Effort,
			FastService:        in.FastService,
			PlanMode:           in.PlanMode,
			RootAbs:            in.RootAbs,
			CurrentSession:     sess,
			Prompt:             in.Prompt,
			SawAssistantChunk:  in.SawAssistantChunk,
			SendWithAttachment: in.SendWithAttachment,
		})
		if recoveredErr != nil {
			sendErr = recoveredErr
		} else {
			sess = recoveredSess
			sendErr = nil
		}
	}
	return sess, sendErr
}

// sendFailurePlan 把「失败之后做什么」收成一个纯判据，顺序即优先级：
//   - 非可恢复错误（限流/配额/远端压缩失败…）：不重试且回收运行时，现状不变；
//   - 终端错误（上下文用满）：不重试、不回收运行时；
//   - 一个字都没出：不重试（重复发同一 prompt 只会再失败）；
//   - 其余：补发 continue。
//
// 抽成纯函数是为了让四类失败的分界可以被表驱动测试钉住，而不是散在
// if/else 里靠阅读顺序保证。
type sendFailurePlan struct {
	RecycleRuntime bool
	RecoverTurn    bool
	Reason         string
}

func planSendFailure(err error, sawAssistantChunk bool) sendFailurePlan {
	if err == nil {
		return sendFailurePlan{Reason: "none"}
	}
	if isNonRecoverableAgentError(err) {
		return sendFailurePlan{RecycleRuntime: true, Reason: "non_recoverable"}
	}
	if isTerminalTurnError(err) {
		return sendFailurePlan{Reason: "terminal_turn"}
	}
	if !sawAssistantChunk {
		return sendFailurePlan{Reason: "no_assistant_output"}
	}
	return sendFailurePlan{RecoverTurn: true, Reason: "recover"}
}

func (s *Service) recoverAgentTurn(ctx context.Context, in SendRecoveryInput) (agenttypes.Session, error) {
	if s == nil {
		return nil, errors.New("services not configured")
	}
	if in.Current == nil {
		return nil, errors.New("session required")
	}
	if in.Manager == nil {
		return nil, errors.New("session manager required")
	}
	if in.SendWithAttachment == nil {
		return nil, errors.New("send function required")
	}
	if in.CurrentSession == nil {
		return nil, errors.New("current session required")
	}

	var lastErr error
	for attempt := 1; attempt <= sessionRecoveryAttempts; attempt++ {
		if attempt > 1 {
			log.Printf("[session/recovery] wait root=%s session=%s agent=%s attempt=%d/%d delay=%s", in.RootID, in.SessionKey, in.AgentName, attempt, sessionRecoveryAttempts, sessionRecoveryDelay)
			if err := waitForRecoveryDelay(ctx, sessionRecoveryDelay); err != nil {
				return nil, err
			}
		}

		sess := in.CurrentSession
		recoveryMessage := in.Prompt
		recoveryAction := "resend_prompt"
		if in.SawAssistantChunk {
			recoveryMessage = "continue"
			recoveryAction = "continue"
		}
		log.Printf("[session/recovery] send.start root=%s session=%s agent=%s attempt=%d/%d action=%s", in.RootID, in.SessionKey, in.AgentName, attempt, sessionRecoveryAttempts, recoveryAction)
		if err := in.SendWithAttachment(sess, recoveryMessage); err != nil {
			if isCanceledTurnError(err) || ctx.Err() != nil {
				return nil, err
			}
			if isNonRecoverableAgentError(err) {
				log.Printf("[session/recovery] send.non_recoverable root=%s session=%s agent=%s attempt=%d/%d action=%s err=%v", in.RootID, in.SessionKey, in.AgentName, attempt, sessionRecoveryAttempts, recoveryAction, err)
				return nil, err
			}
			if isTerminalTurnError(err) {
				log.Printf("[session/recovery] send.terminal root=%s session=%s agent=%s attempt=%d/%d action=%s reason=%s err=%v", in.RootID, in.SessionKey, in.AgentName, attempt, sessionRecoveryAttempts, recoveryAction, agenttypes.TurnTerminalReason(err), err)
				return nil, err
			}
			lastErr = err
			log.Printf("[session/recovery] send.failed root=%s session=%s agent=%s attempt=%d/%d action=%s err=%v", in.RootID, in.SessionKey, in.AgentName, attempt, sessionRecoveryAttempts, recoveryAction, err)
			continue
		}
		log.Printf("[session/recovery] send.done root=%s session=%s agent=%s attempt=%d/%d action=%s", in.RootID, in.SessionKey, in.AgentName, attempt, sessionRecoveryAttempts, recoveryAction)
		return sess, nil
	}
	if lastErr == nil {
		lastErr = errors.New("agent recovery failed")
	}
	return nil, lastErr
}

func waitForRecoveryDelay(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func randomHex(bytes int) string {
	if bytes <= 0 {
		return ""
	}
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

// staleCancelTarget 判断一个取消请求是否已经过期：调用方点名要停的那一轮不是此刻
// 在跑的这一轮（含「当时根本没在跑」）。
func staleCancelTarget(in CancelSessionTurnInput, active *activeTurnState) bool {
	if !in.TargetPinned {
		return false
	}
	if active != nil && active.id == in.TargetTurnID && in.TargetTurnID != 0 {
		return false
	}
	log.Printf("[session] turn.cancel.stale target_turn=%d active_turn=%v action=drop",
		in.TargetTurnID, activeTurnIDOrNil(active))
	return true
}
