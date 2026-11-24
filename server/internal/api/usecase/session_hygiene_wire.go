package usecase

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"mindfs/server/internal/session"
)

// 会话卫生接线：把纯状态机（session_hygiene.go）挂到真实的发送路径上。
//
// 三个接入点，缺一不可：
//   - 回合开始 ObserveAt：活跃时长按「相邻观测的间隔」累计，只有起止都观测，
//     两个回合之间的空档才不会丢。
//   - 压缩事件 MarkCompacted：harness 自己的 /compact（自动或手动）都要把提示
//     与计数清掉，否则压缩完还挂着「即将自动压缩」。
//   - 回合结束 Observe(turnEnded=true)：推进提示轮数，并在到期时派发一次自动
//     压缩。
const hygieneCompactCommand = "/compact"

// hygieneCompactTimeout 是自动压缩回合自己的上限：它包含排队等待用户回合
// 结束的时间，所以要给得比普通回合宽。
const hygieneCompactTimeout = 30 * time.Minute

// hygieneIdlePollInterval 是等待会话空闲的轮询间隔。
var hygieneIdlePollInterval = 2 * time.Second

// hygieneCompactGrace 是「压缩回合已经结束、harness 的压缩事件还没到」时的
// 宽限窗口。
//
// Codex 的 compacted 事件与流结束是两个独立回调，先后没有保证：线上实测两次
// 压缩事件都在流错误之后几十毫秒才落盘（00:35:20.826、10:51:57.686）。没有这个
// 窗口时，成功压缩会被记成失败并 MarkAutoFailed，胶囊永远显示错误、后续也不再
// 自动重试。窗口只影响收尾判定，不影响压缩本身。
var hygieneCompactGrace = 3 * time.Second

// hygieneProvider 是 Registry 的可选能力：AppContext 提供它，测试替身可以不提供。
type hygieneProvider interface {
	SessionHygiene() *HygieneTracker
}

func (s *Service) hygieneTracker() *HygieneTracker {
	if s == nil || s.Registry == nil {
		return nil
	}
	provider, ok := s.Registry.(hygieneProvider)
	if !ok {
		return nil
	}
	return provider.SessionHygiene()
}

// hygieneHarnessForSession 返回判定与触发都要用的 harness 名。
func hygieneHarnessForSession(current *session.Session) string {
	if current == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(session.InferAgentFromSession(current)))
}

// hygieneAppliesToSession 只对真正的对话会话生效：Router / command / view
// 会话要么不是用户对话，要么根本没有上下文可压。
func hygieneAppliesToSession(current *session.Session) bool {
	if current == nil {
		return false
	}
	switch current.Type {
	case "", session.TypeChat:
		return true
	default:
		return false
	}
}

// hygieneAutoCompactSupported 限定在两家有本地 /compact 通道的 harness 上。
// 往别的 harness 发 "/compact" 只会变成一句普通消息 —— 模型回一句「好的」，
// 上下文一点没少，还会被记成一次成功回合。
func hygieneAutoCompactSupported(harness string) bool {
	switch strings.ToLower(strings.TrimSpace(harness)) {
	case "claude", "codex":
		return true
	default:
		return false
	}
}

// observeHygieneTurnStart 记一次「会话此刻有活动」。观测间隔超过 idle gap 的
// 那一段在 tracker 里会被丢弃。
func (s *Service) observeHygieneTurnStart(in SendMessageInput, current *session.Session) {
	if in.hygieneCompact || !hygieneAppliesToSession(current) {
		return
	}
	harness := hygieneHarnessForSession(current)
	if !hygieneAutoCompactSupported(harness) {
		return
	}
	s.hygieneTracker().ObserveAt(in.RootID, in.Key, harness, time.Now())
}

// finishHygieneTurn 在成功回合结束时推进状态，并返回是否要派发自动压缩。
func (s *Service) finishHygieneTurn(in SendMessageInput, current *session.Session, contextTokens, contextWindow int) bool {
	if in.hygieneCompact || !hygieneAppliesToSession(current) {
		return false
	}
	// 斜杠命令不是一次对话回合：/compact 自己会清状态，/clear、/model 之类的
	// 也不该推进「还差几轮自动压缩」。
	if strings.HasPrefix(strings.TrimSpace(in.Content), "/") {
		return false
	}
	harness := hygieneHarnessForSession(current)
	if !hygieneAutoCompactSupported(harness) {
		return false
	}
	tracker := s.hygieneTracker()
	if tracker == nil {
		return false
	}
	view := tracker.Observe(in.RootID, in.Key, harness, contextTokens, contextWindow, true)
	if !view.AutoCompactDue {
		return false
	}
	log.Printf("[session-hygiene] auto_compact.due root=%s session=%s agent=%s active_seconds=%d context_tokens=%d context_limit=%d turns_over_line=%d",
		in.RootID, in.Key, harness, view.ActiveSeconds, view.ContextTokens, view.ContextLimit, view.TurnsOverLine)
	return true
}

// markHygieneCompacted 在任意一次 harness 压缩之后调用。
//
// 自动触发的那一轮同样要清：它本身就是 / 开头的命令、不参与观测，若这里再跳过，
// 压缩完成后状态会永远停在 hint，胶囊一直提示、计数也不再前进。
func (s *Service) markHygieneCompacted(in SendMessageInput) {
	s.hygieneTracker().MarkCompacted(in.RootID, in.Key)
}

// markHygieneContextStale / clearHygieneContextStale 把「压缩事件已到但新 token
// 数未到」这段窗口告诉状态机，避免用旧 context_tokens 再触发一次压缩。
func (s *Service) markHygieneContextStale(in SendMessageInput) {
	s.hygieneTracker().MarkContextStale(in.RootID, in.Key)
}

func (s *Service) clearHygieneContextStale(in SendMessageInput) {
	s.hygieneTracker().ClearContextStale(in.RootID, in.Key)
}

// scheduleHygieneAutoCompact 异步补一个 /compact 回合。
//
// 为什么不在当前回合里同步发：本回合的 defer 还握着发送锁，同步重入会自锁；
// 而放到请求返回之后，用户下一条消息又可能先到。这里先等会话空闲（避免把压缩
// 排在一个长回合后面白等），再走同一条 SendMessage 路径 —— 这样压缩事件、助手
// 行、上下文窗口与胶囊读口都与手动回合完全一致。
func (s *Service) scheduleHygieneAutoCompact(in SendMessageInput, harness string) {
	compactIn := SendMessageInput{
		RootID:          in.RootID,
		RuntimeRootPath: in.RuntimeRootPath,
		Key:             in.Key,
		Agent:           harness,
		Content:         hygieneCompactCommand,
		hygieneCompact:  true,
	}
	s.hygieneTracker().MarkCompactPending(in.RootID, in.Key)
	go s.runHygieneAutoCompact(compactIn)
}

// sendHygieneCompact 是自动压缩回合的发送入口。生产恒为 SendMessage；测试注入
// 假实现，避免为了验证「失败分支」而起一个真 agent。
func (s *Service) sendHygieneCompact(ctx context.Context, in SendMessageInput) error {
	if s.hygieneSend != nil {
		return s.hygieneSend(ctx, in)
	}
	return s.SendMessage(ctx, in)
}

// runHygieneAutoCompact 是自动压缩回合的执行体。
func (s *Service) runHygieneAutoCompact(in SendMessageInput) {
	// 不管从哪条路径进来，这一轮都必须被识别成自动压缩回合：内容固定是
	// 压缩命令，且不参与观测、不再触发下一次压缩。
	in.Content = hygieneCompactCommand
	in.hygieneCompact = true
	seen := make(chan struct{})
	var seenOnce sync.Once
	in.onCompactSeen = func() { seenOnce.Do(func() { close(seen) }) }
	ctx, cancel := context.WithTimeout(context.Background(), hygieneCompactTimeout)
	defer cancel()
	if !s.waitSessionIdle(ctx, in.RootID, in.Key) {
		s.finishHygieneCompact(in, "等待会话空闲超时，压缩没有发出")
		log.Printf("[session-hygiene] auto_compact.give_up root=%s session=%s reason=session_still_busy", in.RootID, in.Key)
		return
	}
	log.Printf("[session-hygiene] auto_compact.start root=%s session=%s agent=%s", in.RootID, in.Key, in.Agent)
	if err := s.sendHygieneCompact(ctx, in); err != nil {
		if waitHygieneCompactSeen(ctx, seen, hygieneCompactGrace) {
			// Codex 的 /compact 回合会在压缩落盘后才关流，关流异常不等于压缩失败。
			// 事件与流结束的先后没有保证，所以错误分支也要等一个宽限窗口。
			log.Printf("[session-hygiene] auto_compact.done root=%s session=%s agent=%s reason=stream_error_after_compact err=%v", in.RootID, in.Key, in.Agent, err)
			s.finishHygieneCompact(in, "")
			return
		}
		s.finishHygieneCompact(in, err.Error())
		log.Printf("[session-hygiene] auto_compact.error root=%s session=%s err=%v", in.RootID, in.Key, err)
		return
	}
	if !waitHygieneCompactSeen(ctx, seen, hygieneCompactGrace) {
		// 命令被当普通消息发了出去：上下文没变，按失败处理，胶囊里给出可见提示。
		s.finishHygieneCompact(in, "harness 没有报告压缩事件，这次压缩没有生效")
		log.Printf("[session-hygiene] auto_compact.no_notice root=%s session=%s agent=%s", in.RootID, in.Key, in.Agent)
		return
	}
	s.finishHygieneCompact(in, "")
	log.Printf("[session-hygiene] auto_compact.done root=%s session=%s agent=%s", in.RootID, in.Key, in.Agent)
}

// waitHygieneCompactSeen 等压缩事件；事件已经到时立即返回，不会为空等。
func waitHygieneCompactSeen(ctx context.Context, seen <-chan struct{}, grace time.Duration) bool {
	select {
	case <-seen:
		return true
	default:
	}
	if grace <= 0 {
		return false
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-seen:
		return true
	case <-timer.C:
		return false
	case <-ctx.Done():
		return false
	}
}

// finishHygieneCompact 是压缩回合唯一的收尾闸：失败分支同时停掉自动重试，
// 成功分支只清 pending。读口拿到的 CompactError 就是这里的 errText。
func (s *Service) finishHygieneCompact(in SendMessageInput, errText string) {
	if errText != "" {
		s.hygieneTracker().MarkAutoFailed(in.RootID, in.Key)
	}
	s.hygieneTracker().FinishCompact(in.RootID, in.Key, errText)
}

// waitSessionIdle 等到这个会话没有在跑的回合为止。
func (s *Service) waitSessionIdle(ctx context.Context, rootID, sessionKey string) bool {
	for {
		if !SessionTurnActive(rootID, sessionKey) {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(hygieneIdlePollInterval):
		}
	}
}

// CompactSession 是手动入口：胶囊上的「立即压缩」。它只负责把回合放出去，
// 请求立刻返回，结果通过正常的会话事件流回到界面。
func (s *Service) CompactSession(ctx context.Context, in GetSessionContextSavingInput) error {
	if err := s.ensureRegistry(); err != nil {
		return err
	}
	key := strings.TrimSpace(in.Key)
	if key == "" {
		return errors.New("session key required")
	}
	manager, err := s.Registry.GetSessionManager(in.RootID)
	if err != nil {
		return err
	}
	current, err := manager.Get(ctx, key, 0)
	if err != nil {
		return err
	}
	harness := hygieneHarnessForSession(current)
	if !hygieneAutoCompactSupported(harness) || !hygieneAppliesToSession(current) {
		return errors.New("this session's agent has no local compaction command")
	}
	s.scheduleHygieneAutoCompact(SendMessageInput{RootID: in.RootID, Key: key}, harness)
	return nil
}
