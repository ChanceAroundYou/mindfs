package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"mindfs/server/internal/agent/logs"
	types "mindfs/server/internal/agent/types"

	codexsdk "github.com/fanwenlin/codex-go-sdk/codex"
)

type session struct {
	client     *codexsdk.Codex
	thread     *codexsdk.Thread
	threadOpts codexsdk.ThreadOptions
	threadID   string
	sessionKey string
	planMode   bool

	mu            sync.RWMutex
	onUpdate      func(types.Event)
	onCompact     func(threadID string)
	onThreadStart func(threadID string)
	// onThreadRethreaded 与 onThreadStart 分开：换绑出来的线程除了「登记」，
	// 还必须在代理那边标成跨了供应商（旧供应商的 encrypted_content 新供应商
	// 解不开，G-PK）。两者都登记线程，但只有换绑要额外标记，所以不能共用一个
	// 回调 —— 池子侧只能从回调里知道「这是换绑，不是新线程」。
	onThreadRethreaded func(threadID string)
	turn               types.TurnCanceler
	contextWindow      types.ContextWindow
	planTextByID       map[string]string
	// emitted 是本会话累计发出的事件数。writer 冲突重试只在**一个事件都没发出去**
	// 时进行：那说明 thread/resume 就失败了，重发不会重复内容；一旦有 delta 落地，
	// 重发就可能把同一条用户消息写两遍。
	emitted atomic.Int64

	agentDebugLog *logs.AgentLogger

	questionMu    sync.Mutex
	questionWaits map[string]chan codexAskUserAnswerResult
}

func (s *session) SendMessage(ctx context.Context, content string) error {
	if s == nil || s.thread == nil {
		return errors.New("codex session not initialized")
	}
	turnCtx, turnID := s.turn.Begin(ctx)
	defer s.turn.End(turnID)
	log.Printf("[agent/codex] input session=%s content=%q", s.sessionKey, preview(content))

	var lastErr error
	rethreaded := false
	for attempt := 1; attempt <= codexWriterConflictAttempts; attempt++ {
		before := s.emitted.Load()
		streamed, err := s.thread.RunStreamed(content, codexsdk.TurnOptions{Context: turnCtx})
		if err != nil {
			log.Printf("[agent/codex] send.error session=%s err=%v", s.sessionKey, err)
			return err
		}

		completed, err := s.handleStreamedEvents(streamed.Events)
		if err == nil {
			if !completed {
				// 回合被取消时，SDK 往调用方转发事件的那个 select 会先走 ctx.Done()
				// 分支，事件通道直接关闭 —— 「这一轮是被停掉的」这个原因在流里就丢了。
				// 写成「流意外结束」会被恢复循环当成可重试的失败：停止按钮按下去，
				// agent 又被拉起来继续跑（2026-10-19 23:24 在 pc-win 上实测：codex
				// 连按七次停不下来，recovery 每次都 action=continue 重发）。
				// 取消的原因从 turnCtx 补回来：SDK 收到的就是这个 ctx。
				if cause := turnCtx.Err(); cause != nil {
					return cause
				}
				// 真·意外结束（进程中途死亡），不能当成功。
				return errors.New("codex response stream ended unexpectedly")
			}
			s.updateThreadIDFromThread()
			return nil
		}

		lastErr = err
		// 供应商不一致：线程记录的供应商已经不在配置里，重发只会再撞一次同样的
		// 错误。当场把线程 fork 到当前供应商再重试一轮（只做一次）。这条路径修
		// 的是「打开时没能重新绑定」留下的中毒会话 —— 池子里缓存着它，光靠
		// 打开路径的修复对已经中毒的会话不生效。
		if !rethreaded && isCodexProviderMismatch(err) && s.emitted.Load() == before {
			if forkErr := s.rethreadThread(turnCtx); forkErr != nil {
				log.Printf("[agent/codex] provider.rethread.error session=%s err=%v", s.sessionKey, forkErr)
				return fmt.Errorf("%w（线程仍记录着已不再配置的模型供应商，且换绑失败：%v）", err, forkErr)
			}
			rethreaded = true
			continue
		}
		// writer 冲突只在「本回合一个事件都没发出去」时重试（见 emitted 字段）。
		if !isCodexWriterConflict(err) || s.emitted.Load() != before {
			return err
		}
		if attempt == codexWriterConflictAttempts {
			break
		}
		log.Printf("[agent/codex] writer.conflict session=%s attempt=%d/%d action=retry", s.sessionKey, attempt, codexWriterConflictAttempts)
		select {
		case <-turnCtx.Done():
			return turnCtx.Err()
		case <-time.After(time.Duration(attempt) * codexWriterConflictBackoff):
		}
	}
	if !isCodexWriterConflict(lastErr) {
		// 换绑把这一轮用完了循环预算时，收尾不能报成 writer 冲突 —— 那会误导
		// 用户去关 Codex 桌面版，而真正的原因是供应商换绑。
		return lastErr
	}
	return fmt.Errorf("%w（另一个 Codex 客户端正占用该会话，通常是 Codex 桌面版打开了同一个对话；在那边关闭该会话后重试）", lastErr)
}

// ProcessID returns the app-server PID behind this session. The pool uses it
// to attach the process to the MindFS lifetime guard as soon as it opens.
func (s *session) ProcessID() int {
	if s == nil || s.client == nil {
		return 0
	}
	return s.client.ProcessID()
}

// codex 的 thread writer 是**跨进程独占文件锁**（rollout/src/writer_lock.rs）。
// 同一个 CODEX_HOME 下任何另一个 codex 进程（最典型的是 Codex 桌面版）把该 thread
// 加载成 live recorder，就会按住锁直到它自己退出；此时 thread/resume 返回
// "already has an active writer"。对方也可能只是在跑一个回合、很快就释放，
// 所以先短暂重试；仍失败再把原因说清楚，而不是把 codex 的原始英文抛给用户。
const codexWriterConflictAttempts = 4

// var 而非 const：测试要把它压到毫秒级。
var codexWriterConflictBackoff = time.Second

func isCodexWriterConflict(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "already has an active writer") ||
		strings.Contains(msg, "already has a live local writer")
}

func (s *session) LoginChatGPTDeviceCode(ctx context.Context) error {
	if s == nil || s.client == nil {
		return errors.New("codex session not initialized")
	}
	turnCtx, turnID := s.turn.Begin(ctx)
	defer s.turn.End(turnID)
	events, err := s.client.LoginChatGPTDeviceCode(turnCtx)
	if err != nil {
		return err
	}
	for event := range events {
		notice := types.LoginNotice{
			Status:          strings.TrimSpace(event.Status),
			LoginID:         strings.TrimSpace(event.LoginID),
			VerificationURL: strings.TrimSpace(event.VerificationURL),
			UserCode:        strings.TrimSpace(event.UserCode),
			Error:           strings.TrimSpace(event.Error),
		}
		if event.AccountUpdated != nil {
			if event.AccountUpdated.AuthMode != nil {
				notice.AuthMode = strings.TrimSpace(*event.AccountUpdated.AuthMode)
			}
			if event.AccountUpdated.PlanType != nil {
				notice.PlanType = strings.TrimSpace(string(*event.AccountUpdated.PlanType))
			}
		}
		if notice.Status == "" {
			notice.Status = "running"
		}
		if notice.Status == "error" && notice.Error == "" {
			notice.Error = "login failed"
		}
		s.emit(types.Event{Type: types.EventTypeLogin, SessionID: s.SessionID(), Data: notice})
		if notice.Status == "error" {
			return errors.New(notice.Error)
		}
	}
	return nil
}

func (s *session) SubscribeThreadEvents(ctx context.Context) error {
	if s == nil || s.client == nil {
		return errors.New("codex session not initialized")
	}
	turnCtx, turnID := s.turn.Begin(ctx)
	defer s.turn.End(turnID)
	threadID := strings.TrimSpace(s.SessionID())
	if threadID == "" {
		s.updateThreadIDFromThread()
		threadID = strings.TrimSpace(s.SessionID())
	}
	if threadID == "" {
		return errors.New("codex thread id required")
	}
	streamed, err := s.client.SubscribeThreadEvents(turnCtx, threadID, s.threadOpts)
	if err != nil {
		return err
	}
	// 长连接订阅：通道关闭是正常终止，不要求 TurnCompleted。
	_, err = s.handleStreamedEvents(streamed.Events)
	return err
}

func (s *session) emitMessageDelta(msg *codexsdk.AgentMessageItem, textByID map[string]string) {
	delta := messageDelta(textByID[msg.ID], msg.Text)
	textByID[msg.ID] = msg.Text
	if delta == "" {
		return
	}
	s.emit(types.Event{Type: types.EventTypeMessageChunk, SessionID: s.SessionID(), Data: types.MessageChunk{Content: delta}})
}

func (s *session) emit(event types.Event) {
	s.emitted.Add(1)
	s.mu.RLock()
	handler := s.onUpdate
	s.mu.RUnlock()
	if handler == nil {
		return
	}
	handler(event)
}

func (s *session) OnUpdate(onUpdate func(types.Event)) {
	s.mu.Lock()
	s.onUpdate = onUpdate
	s.mu.Unlock()
}

func (s *session) CancelCurrentTurn() error {
	s.cancelPendingQuestions(errors.New("turn canceled"))
	s.turn.Cancel()
	return nil
}

func (s *session) SessionID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.threadID
}

func (s *session) ContextWindow(_ context.Context) (types.ContextWindow, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.contextWindow, nil
}

func (s *session) Close() error {
	if s == nil || s.thread == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.thread.Close(ctx)
}

// ensureThreadProvider 检查线程记录的供应商是否仍是当前配置，不一致就当场换绑。
//
// 所有「重新 ResumeThread 一条已有线程」的路径都要先走它：SetModel 与
// SetPlanMode 会重建 thread 对象，旧供应商的线程在那里被原样留下时，用户之后
// 每发一条消息都撞 -32600，而这条路径不经过打开逻辑，池子里的缓存会话也救不到。
//
// 换绑失败不阻断设置本身（模型 / plan 模式与供应商无关），但必须记日志说明
// 线程仍是旧供应商：接下来的回合会在 SendMessage 里再撞一次并尝试自己修。
func (s *session) ensureThreadProvider(ctx context.Context) error {
	if s == nil || s.client == nil {
		return errors.New("codex session not initialized")
	}
	threadID := strings.TrimSpace(s.SessionID())
	if threadID == "" {
		s.updateThreadIDFromThread()
		threadID = strings.TrimSpace(s.SessionID())
	}
	if threadID == "" {
		return nil
	}
	s.mu.RLock()
	opts := s.threadOpts
	s.mu.RUnlock()
	forked, rethreaded, err := rethreadCodexResumeOnProviderMismatch(ctx, s.client, threadID, opts, opts.WorkingDirectory)
	if err != nil {
		log.Printf("[agent/codex] provider.rethread.error session=%s path=settings err=%v", s.sessionKey, err)
		return nil
	}
	if !rethreaded || forked == "" {
		return nil
	}
	opts.ModelProvider = ""
	thread := s.client.ResumeThread(forked, opts)
	s.mu.Lock()
	s.thread = thread
	s.threadOpts = opts
	s.threadID = forked
	s.mu.Unlock()
	log.Printf("[agent/codex] provider.rethreaded session=%s path=settings source=%s forked=%s", s.sessionKey, threadID, forked)
	if notify := s.onThreadRethreaded; notify != nil {
		notify(forked)
	} else if notify := s.onThreadStart; notify != nil {
		notify(forked)
	}
	return nil
}

// rethreadThread 在会话已经打开之后把线程换绑到当前配置的供应商。
//
// 打开路径（session_open.go）在 resume 之前就会做同样的检查，所以正常情况走不到
// 这里。它修的是另一条：会话在**旧**版本下打开过、池子里缓存着那条旧供应商线程
// （-32600 之后每一轮都失败），或者在打开之后供应商又被切走。这类中毒会话光修
// 打开路径没用 —— pool.GetOrCreate 命中缓存就直接返回（pool.go 的 sessions 短路），
// 根本不会再打开一次。
//
// 换绑后线程 id 会变，必须同步：s.thread/threadOpts/threadID 与池子里的绑定
// （onThreadStart 回调 → RegisterThread）都换到新线程。
func (s *session) rethreadThread(ctx context.Context) error {
	if s == nil || s.client == nil {
		return errors.New("codex session not initialized")
	}
	threadID := strings.TrimSpace(s.SessionID())
	if threadID == "" {
		s.updateThreadIDFromThread()
		threadID = strings.TrimSpace(s.SessionID())
	}
	if threadID == "" {
		return errors.New("codex thread id unavailable")
	}
	s.mu.RLock()
	opts := s.threadOpts
	s.mu.RUnlock()
	forked, rethreaded, err := rethreadCodexResumeOnProviderMismatch(ctx, s.client, threadID, opts, opts.WorkingDirectory)
	if err != nil {
		return err
	}
	if !rethreaded || forked == "" {
		// 走到这里说明线程其实不需要换绑（判定在更近的一次读取里不成立），
		// 或者上一轮已经把线程换成了当前供应商。原样重试即可。
		return nil
	}
	opts.ModelProvider = ""
	thread := s.client.ResumeThread(forked, opts)
	s.mu.Lock()
	s.thread = thread
	s.threadOpts = opts
	s.threadID = forked
	s.mu.Unlock()
	log.Printf("[agent/codex] provider.rethreaded session=%s source=%s forked=%s", s.sessionKey, threadID, forked)
	// 让池子把新线程登记到代理上，并标记这条线程跨了供应商。这里不能直接调
	// RegisterThread：那是 Pool 的方法，而 session 只持有回调。优先用换绑回调；
	// 没接（单独构造的 session）时退回普通登记回调，至少不让代理丢映射。
	if notify := s.onThreadRethreaded; notify != nil {
		notify(forked)
	} else if notify := s.onThreadStart; notify != nil {
		notify(forked)
	}
	return nil
}

func (s *session) updateThreadIDFromThread() {
	if s == nil || s.thread == nil {
		return
	}
	id := s.thread.ID()
	if id == nil {
		return
	}
	s.setThreadID(*id)
}

// setThreadID records the thread id and, the first time it becomes known,
// tells the pool. A new thread has no id until `thread/started` arrives, so
// this callback is what binds the thread to its MindFS session on the proxy.
func (s *session) setThreadID(id string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	s.mu.Lock()
	changed := s.threadID != id
	s.threadID = id
	notify := s.onThreadStart
	s.mu.Unlock()
	if changed && notify != nil {
		notify(id)
	}
}

func (s *session) logRawToolItem(item codexsdk.ThreadItem) {
	if s == nil || s.agentDebugLog == nil || !isToolItem(item) {
		return
	}
	raw, err := json.Marshal(item)
	if err != nil {
		return
	}
	s.agentDebugLog.AppendRaw(raw)
}
