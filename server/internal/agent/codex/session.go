package codex

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"sync"
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
	turn          types.TurnCanceler
	contextWindow types.ContextWindow
	planTextByID  map[string]string

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
	streamed, err := s.thread.RunStreamed(content, codexsdk.TurnOptions{Context: turnCtx})
	if err != nil {
		log.Printf("[agent/codex] send.error session=%s err=%v", s.sessionKey, err)
		return err
	}

	completed, err := s.handleStreamedEvents(streamed.Events)
	if err != nil {
		return err
	}
	if !completed {
		// 通道关闭但没见到 TurnCompletedEvent：进程中途死亡，不能当成功。
		return errors.New("codex response stream ended unexpectedly")
	}
	s.updateThreadIDFromThread()
	return nil
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

func (s *session) setThreadID(id string) {
	if strings.TrimSpace(id) == "" {
		return
	}
	s.mu.Lock()
	s.threadID = id
	s.mu.Unlock()
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
