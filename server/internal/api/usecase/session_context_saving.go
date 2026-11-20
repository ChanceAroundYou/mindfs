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

// GetSessionContextSaving returns what live-compact folded for this session.
//
// The ledger is keyed by the pool session key, not by the MindFS session key
// the UI uses: the same conversation can be served by different agents, and
// each agent prefix gets its own pool session (and therefore its own fold
// history). The summary returned is always the pool key the current session
// would use, so the chip describes what this conversation is sending *now*.
//
// It never fails for an unconfigured or folded-never session: that is an empty
// summary, not an error. The only real error is an unknown session key.
func (s *Service) GetSessionContextSaving(ctx context.Context, in GetSessionContextSavingInput) (fold.ContextSaving, error) {
	if err := s.ensureRegistry(); err != nil {
		return fold.ContextSaving{}, err
	}
	key := strings.TrimSpace(in.Key)
	if key == "" {
		return fold.ContextSaving{}, errors.New("session key required")
	}
	manager, err := s.Registry.GetSessionManager(in.RootID)
	if err != nil {
		return fold.ContextSaving{}, err
	}
	current, err := manager.Get(ctx, key, 0)
	if err != nil {
		return fold.ContextSaving{}, err
	}
	pool := s.Registry.GetAgentPool()
	if pool == nil {
		return fold.ContextSaving{}, nil
	}
	agentName := strings.TrimSpace(session.InferAgentFromSession(current))
	return pool.SessionContextSaving(agentPoolSessionKey(key, agentName)), nil
}
