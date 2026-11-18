package usecase

import (
	"context"
	"errors"
	"strings"

	"mindfs/server/internal/agent/proxy"
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
func (s *Service) GetSessionContextSaving(ctx context.Context, in GetSessionContextSavingInput) (proxy.ContextSaving, error) {
	if err := s.ensureRegistry(); err != nil {
		return proxy.ContextSaving{}, err
	}
	key := strings.TrimSpace(in.Key)
	if key == "" {
		return proxy.ContextSaving{}, errors.New("session key required")
	}
	manager, err := s.Registry.GetSessionManager(in.RootID)
	if err != nil {
		return proxy.ContextSaving{}, err
	}
	current, err := manager.Get(ctx, key, 0)
	if err != nil {
		return proxy.ContextSaving{}, err
	}
	pool := s.Registry.GetAgentPool()
	if pool == nil {
		return proxy.ContextSaving{}, nil
	}
	agentName := strings.TrimSpace(session.InferAgentFromSession(current))
	return pool.SessionContextSaving(agentPoolSessionKey(key, agentName)), nil
}
