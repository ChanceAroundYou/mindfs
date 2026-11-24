package usecase

import (
	"context"
	"errors"
	"strings"

	"mindfs/server/internal/agent/fold"
	"mindfs/server/internal/session"
)

type GetSessionContextSavingInput struct {
	RootID string
	Key    string
}

// SessionContextSavingView 是上下文胶囊的完整读口：折叠账本（内嵌成扁平字段，
// 老客户端的读法不变）+ 会话卫生判定。
type SessionContextSavingView struct {
	fold.ContextSaving
	Hygiene HygieneView `json:"hygiene"`
}

// GetSessionContextSaving returns what live-compact folded for this session,
// plus the session-hygiene verdict that drives the long-session hint.
//
// The ledger is keyed by the pool session key, not by the MindFS session key
// the UI uses: the same conversation can be served by different agents, and
// each agent prefix gets its own pool session (and therefore its own fold
// history). The summary returned is always the pool key the current session
// would use, so the chip describes what this conversation is sending *now*.
//
// It never fails for an unconfigured or folded-never session: that is an empty
// summary, not an error. The only real error is an unknown session key.
func (s *Service) GetSessionContextSaving(ctx context.Context, in GetSessionContextSavingInput) (SessionContextSavingView, error) {
	if err := s.ensureRegistry(); err != nil {
		return SessionContextSavingView{}, err
	}
	key := strings.TrimSpace(in.Key)
	if key == "" {
		return SessionContextSavingView{}, errors.New("session key required")
	}
	manager, err := s.Registry.GetSessionManager(in.RootID)
	if err != nil {
		return SessionContextSavingView{}, err
	}
	current, err := manager.Get(ctx, key, 0)
	if err != nil {
		return SessionContextSavingView{}, err
	}
	view := SessionContextSavingView{Hygiene: s.hygieneViewForSession(in.RootID, current)}
	pool := s.Registry.GetAgentPool()
	if pool == nil {
		return view, nil
	}
	agentName := strings.TrimSpace(session.InferAgentFromSession(current))
	view.ContextSaving = pool.SessionContextSaving(agentPoolSessionKey(key, agentName))
	return view, nil
}

// hygieneViewForSession 是只读判定：不推进轮数、不创建任何后台动作。
func (s *Service) hygieneViewForSession(rootID string, current *session.Session) HygieneView {
	tokens, window := 0, 0
	if current != nil {
		tokens = current.LastContextWindow.TotalTokens
		window = current.LastContextWindow.ModelContextWindow
	}
	if !hygieneAppliesToSession(current) {
		return EvaluateHygiene(HygieneConfig{}, HygieneSnapshot{ContextTokens: tokens, ContextWindow: window})
	}
	harness := hygieneHarnessForSession(current)
	return s.hygieneTracker().Snapshot(rootID, current.Key, harness, tokens, window)
}
