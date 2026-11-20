package usecase

import (
	"context"
	"errors"
	"strings"

	"mindfs/server/internal/agent"
	"mindfs/server/internal/session"
)

// GetSessionFoldUsage returns the live-compact ledger rows for the pool session
// this MindFS session is using right now (Plan 2026-11-20 Task 13).
//
// The ledger is keyed by the pool session key, not the MindFS key: the same
// conversation can be served by different agents and each agent prefix has its
// own fold history. Reading the bare key silently returns nothing, which the
// completion record would report as "no usage" — that is why this resolution
// lives here, next to GetSessionContextSaving, instead of at the call site.
//
// An unknown session key is a real error (same contract as
// GetSessionContextSaving). A known session that never folded returns no rows,
// which the caller reports as "not measured", never as zero.
func (s *Service) GetSessionFoldUsage(ctx context.Context, rootID, key string) ([]agent.FoldUsageRow, error) {
	if err := s.ensureRegistry(); err != nil {
		return nil, err
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, errors.New("session key required")
	}
	manager, err := s.Registry.GetSessionManager(rootID)
	if err != nil {
		return nil, err
	}
	current, err := manager.GetMeta(ctx, key)
	if err != nil {
		return nil, err
	}
	pool := s.Registry.GetAgentPool()
	if pool == nil {
		return nil, nil
	}
	agentName := strings.TrimSpace(session.InferAgentFromSession(current))
	return pool.SessionFoldUsage(agentPoolSessionKey(key, agentName)), nil
}
