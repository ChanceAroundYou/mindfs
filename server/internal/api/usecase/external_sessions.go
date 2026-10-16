package usecase

import (
	"context"
	"errors"
	"strings"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/apperr"
	"mindfs/server/internal/session"
)

type ListExternalSessionsInput struct {
	RootID      string
	Agent       string
	BeforeTime  time.Time
	AfterTime   time.Time
	Limit       int
	FilterBound bool
}

type ListExternalSessionsOutput struct {
	Items []agenttypes.ExternalSessionSummary `json:"items"`
}

type ImportExternalSessionInput struct {
	RootID         string
	Agent          string
	AgentSessionID string
}

type ImportExternalSessionOutput struct {
	SessionKey     string `json:"session_key"`
	Agent          string `json:"agent"`
	AgentSessionID string `json:"agent_session_id"`
	ImportedCount  int    `json:"imported_count"`
}

type ImportExternalSessionsBatchInput struct {
	RootID          string
	Agent           string
	AgentSessionIDs []string
}

type ImportExternalSessionsBatchItem struct {
	AgentSessionID string `json:"agent_session_id"`
	SessionKey     string `json:"session_key,omitempty"`
	ImportedCount  int    `json:"imported_count,omitempty"`
	Success        bool   `json:"success"`
	Error          string `json:"error,omitempty"`
	ErrorCode      string `json:"error_code,omitempty"`
	ErrorDetail    string `json:"error_detail,omitempty"`
	ErrorPath      string `json:"error_path,omitempty"`
	ErrorOperation string `json:"error_operation,omitempty"`
}

type ImportExternalSessionsBatchOutput struct {
	Items []ImportExternalSessionsBatchItem `json:"items"`
}

type SyncExternalSessionDeltaInput struct {
	RootID string
	Key    string
	Full   bool
}

type SyncExternalSessionDeltaOutput struct {
	ImportedCount int
	LastTimestamp time.Time
}

func (s *Service) ListExternalSessions(ctx context.Context, in ListExternalSessionsInput) (ListExternalSessionsOutput, error) {
	if err := s.ensureRegistry(); err != nil {
		return ListExternalSessionsOutput{}, err
	}
	root, err := s.Registry.GetRoot(in.RootID)
	if err != nil {
		return ListExternalSessionsOutput{}, err
	}
	manager, err := s.Registry.GetSessionManager(in.RootID)
	if err != nil {
		return ListExternalSessionsOutput{}, err
	}
	importer, err := s.resolveExternalSessionImporter(in.Agent)
	if err != nil {
		return ListExternalSessionsOutput{}, err
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	rootPath := normalizeExternalSessionPath(root.RootPath)
	items := make([]agenttypes.ExternalSessionSummary, 0, limit)
	seen := make(map[string]struct{})
	visit := func(item agenttypes.ExternalSessionSummary) (bool, error) {
		if _, ok := seen[item.AgentSessionID]; ok {
			return true, nil
		}
		seen[item.AgentSessionID] = struct{}{}
		if normalizeExternalSessionPath(item.Cwd) != rootPath {
			return true, nil
		}
		firstUserText := strings.TrimSpace(item.FirstUserText)
		if strings.HasPrefix(firstUserText, buildSessionNamePrompt("")) {
			return true, nil
		}
		if in.FilterBound {
			bound, err := manager.HasAgentBinding(ctx, in.Agent, item.AgentSessionID)
			if err != nil {
				return false, err
			}
			if bound {
				return true, nil
			}
		}
		item.FirstUserText = stripExternalSessionPrefix(item.FirstUserText)
		if alias, ok := manager.LookupAliasForAgent(in.Agent, item.AgentSessionID); ok {
			item.Title = alias
		} else if strings.TrimSpace(item.Title) == "" {
			// No alias & no claude resume title: use stripped lastUserText front 20 chars (no REPLY_TIPS).
			if short := shortExternalSessionTitle(item.FirstUserText); short != "" {
				item.Title = short
			}
		}
		items = append(items, item)
		return len(items) < limit, nil
	}
	if streaming, ok := importer.(agenttypes.StreamingExternalSessionImporter); ok {
		err := streaming.ScanExternalSessions(ctx, agenttypes.ListExternalSessionsInput{
			RootPath:    root.RootPath,
			Agent:       in.Agent,
			BeforeTime:  in.BeforeTime,
			AfterTime:   in.AfterTime,
			Limit:       limit,
			FilterBound: false,
		}, visit)
		if err != nil {
			return ListExternalSessionsOutput{}, err
		}
		return ListExternalSessionsOutput{Items: items}, nil
	}
	result, err := importer.ListExternalSessions(ctx, agenttypes.ListExternalSessionsInput{
		RootPath:    root.RootPath,
		Agent:       in.Agent,
		BeforeTime:  in.BeforeTime,
		AfterTime:   in.AfterTime,
		Limit:       limit,
		FilterBound: false,
	})
	if err != nil {
		return ListExternalSessionsOutput{}, err
	}
	for _, item := range result.Items {
		shouldContinue, err := visit(item)
		if err != nil {
			return ListExternalSessionsOutput{}, err
		}
		if !shouldContinue {
			break
		}
	}
	return ListExternalSessionsOutput{Items: items}, nil
}

func (s *Service) ImportExternalSession(ctx context.Context, in ImportExternalSessionInput) (ImportExternalSessionOutput, error) {
	if err := s.ensureRegistry(); err != nil {
		return ImportExternalSessionOutput{}, err
	}
	root, err := s.Registry.GetRoot(in.RootID)
	if err != nil {
		return ImportExternalSessionOutput{}, err
	}
	manager, err := s.Registry.GetSessionManager(in.RootID)
	if err != nil {
		return ImportExternalSessionOutput{}, err
	}
	importer, err := s.resolveExternalSessionImporter(in.Agent)
	if err != nil {
		return ImportExternalSessionOutput{}, err
	}
	imported, err := importer.ImportExternalSession(ctx, agenttypes.ImportExternalSessionInput{
		RootPath:       root.RootPath,
		Agent:          in.Agent,
		AgentSessionID: in.AgentSessionID,
	})
	if err != nil {
		return ImportExternalSessionOutput{}, err
	}

	name := buildImportedSessionName(imported)
	if alias, ok := manager.LookupAliasForAgent(in.Agent, imported.AgentSessionID); ok {
		name = alias
	}
	created, err := manager.Create(ctx, session.CreateInput{
		Type:  session.TypeChat,
		Agent: in.Agent,
		Name:  name,
	})
	if err != nil {
		return ImportExternalSessionOutput{}, err
	}
	for _, exchange := range imported.Exchanges {
		if _, err := appendImportedExchange(ctx, manager, created, in.Agent, exchange); err != nil {
			return ImportExternalSessionOutput{}, err
		}
	}
	current, err := manager.Get(ctx, created.Key, 0)
	if err != nil {
		return ImportExternalSessionOutput{}, err
	}
	importedCount := len(current.Exchanges)
	if err := manager.UpdateAgentState(ctx, created, in.Agent, importedCount, imported.AgentSessionID); err != nil {
		return ImportExternalSessionOutput{}, err
	}
	if _, err := syncImportedSubagentSessions(ctx, manager, created, in.Agent, imported.Subagents); err != nil {
		return ImportExternalSessionOutput{}, err
	}
	return ImportExternalSessionOutput{
		SessionKey:     created.Key,
		Agent:          in.Agent,
		AgentSessionID: imported.AgentSessionID,
		ImportedCount:  importedCount,
	}, nil
}

func (s *Service) ImportExternalSessionsBatch(ctx context.Context, in ImportExternalSessionsBatchInput) (ImportExternalSessionsBatchOutput, error) {
	seen := make(map[string]struct{})
	ids := make([]string, 0, len(in.AgentSessionIDs))
	for _, id := range in.AgentSessionIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return ImportExternalSessionsBatchOutput{}, errors.New("agent_session_ids are required")
	}

	out := ImportExternalSessionsBatchOutput{Items: make([]ImportExternalSessionsBatchItem, 0, len(ids))}
	for _, id := range ids {
		imported, err := s.ImportExternalSession(ctx, ImportExternalSessionInput{
			RootID:         in.RootID,
			Agent:          in.Agent,
			AgentSessionID: id,
		})
		item := ImportExternalSessionsBatchItem{AgentSessionID: id}
		if err != nil {
			item.Success = false
			item.Error = err.Error()
			if appErr, ok := apperr.Classify(err); ok {
				item.ErrorCode = appErr.Code
				item.ErrorDetail = appErr.Detail
				item.ErrorPath = appErr.Path
				item.ErrorOperation = appErr.Op
			}
			out.Items = append(out.Items, item)
			continue
		}
		item.Success = true
		item.SessionKey = imported.SessionKey
		item.ImportedCount = imported.ImportedCount
		out.Items = append(out.Items, item)
	}
	return out, nil
}
