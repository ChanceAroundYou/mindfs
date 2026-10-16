package session

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
)

func (m *Manager) getSessionMetaUnsafe(key string) (*Session, error) {
	if strings.TrimSpace(key) == "" {
		return nil, errors.New("session key required")
	}
	db, err := m.ensureSessionMetaDBUnsafe()
	if err != nil {
		return nil, err
	}
	row := db.QueryRow(selectSessionSQL+`
WHERE key = ?`, key)
	session, err := scanSessionMetaRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errSessionNotFound
	}
	if err != nil {
		return nil, err
	}
	return session, nil
}

func (m *Manager) getSessionMetaWithBindingsUnsafe(key string) (*Session, error) {
	meta, err := m.getSessionMetaUnsafe(key)
	if err != nil {
		return nil, err
	}
	bindings, err := m.listAgentBindingsUnsafe(key)
	if err != nil {
		return nil, err
	}
	for _, binding := range bindings {
		if strings.TrimSpace(binding.Agent) == "" {
			continue
		}
		meta.AgentCtxSeq[binding.Agent] = binding.AgentCtxSeq
	}
	_ = m.applySessionNameAliasSingleUnsafe(meta)
	return meta, nil
}

func (m *Manager) upsertSessionMetaUnsafe(session *Session) error {
	db, err := m.ensureSessionMetaDBUnsafe()
	if err != nil {
		return err
	}
	if session == nil {
		return errors.New("session required")
	}
	normalizeSessionMeta(session)
	args, err := sessionMetaUpsertArgs(session)
	if err != nil {
		return err
	}
	_, err = db.Exec(upsertSessionMetaSQL, args...)
	if err != nil {
		return err
	}
	return nil
}

func sessionMetaUpsertArgs(session *Session) ([]any, error) {
	if session == nil {
		return nil, errors.New("session required")
	}
	relatedFilesJSON, err := json.Marshal(session.RelatedFiles)
	if err != nil {
		return nil, err
	}
	relatedWorktreeJSON := ""
	if session.RelatedWorktree != nil && strings.TrimSpace(session.RelatedWorktree.Path) != "" {
		payload, err := json.Marshal(session.RelatedWorktree)
		if err != nil {
			return nil, err
		}
		relatedWorktreeJSON = string(payload)
	}
	var closedAt any
	if session.ClosedAt != nil {
		closedAt = session.ClosedAt.UTC().Format(time.RFC3339Nano)
	}
	var archivedAt any
	if session.ArchivedAt != nil {
		archivedAt = session.ArchivedAt.UTC().Format(time.RFC3339Nano)
	}
	return []any{
		session.Key,
		session.Type,
		session.ParentSessionKey,
		session.ParentToolCallID,
		session.Source,
		session.TaskID,
		session.Model,
		session.Shell,
		boolToSQLiteInt(session.PlanMode),
		session.Name,
		string(relatedFilesJSON),
		relatedWorktreeJSON,
		strings.TrimSpace(session.WorkDir),
		boolToSQLiteInt(session.Aux.AskUserWaiting),
		boolToSQLiteInt(session.Aux.HasPlan),
		boolToSQLiteInt(session.Aux.HasTodos),
		boolToSQLiteInt(session.Aux.HasTask),
		strings.TrimSpace(session.Aux.SessionError),
		session.LastContextWindow.TotalTokens,
		session.LastContextWindow.ModelContextWindow,
		archivedAt,
		session.CreatedAt.UTC().Format(time.RFC3339Nano),
		session.UpdatedAt.UTC().Format(time.RFC3339Nano),
		closedAt,
	}, nil
}

func validContextWindow(contextWindow agenttypes.ContextWindow) bool {
	return contextWindow.TotalTokens > 0 && contextWindow.ModelContextWindow > 0
}

func boolToSQLiteInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanSessionMetaRow(scanner rowScanner) (*Session, error) {
	var (
		key                 string
		typ                 string
		parentSessionKey    string
		parentToolCallID    string
		source              string
		taskID              string
		model               string
		shell               string
		planMode            int
		name                string
		relatedFilesJSON    string
		relatedWorktreeJSON string
		workDir             string
		auxAskUserWaiting   int
		auxHasPlan          int
		auxHasTodos         int
		auxHasTask          int
		auxSessionError     string
		contextTotalTokens  int
		contextModelWindow  int
		archivedAtRaw       sql.NullString
		createdAtRaw        string
		updatedAtRaw        string
		closedAtRaw         sql.NullString
	)
	if err := scanner.Scan(
		&key,
		&typ,
		&parentSessionKey,
		&parentToolCallID,
		&source,
		&taskID,
		&model,
		&shell,
		&planMode,
		&name,
		&relatedFilesJSON,
		&relatedWorktreeJSON,
		&workDir,
		&auxAskUserWaiting,
		&auxHasPlan,
		&auxHasTodos,
		&auxHasTask,
		&auxSessionError,
		&contextTotalTokens,
		&contextModelWindow,
		&archivedAtRaw,
		&createdAtRaw,
		&updatedAtRaw,
		&closedAtRaw,
	); err != nil {
		return nil, err
	}
	session := &Session{
		Key:              key,
		Type:             typ,
		ParentSessionKey: parentSessionKey,
		ParentToolCallID: parentToolCallID,
		Source:           source,
		TaskID:           taskID,
		Model:            model,
		Shell:            shell,
		PlanMode:         planMode != 0,
		Name:             name,
		WorkDir:          workDir,
		Exchanges:        []Exchange{},
		RelatedFiles:     []RelatedFile{},
		Aux: SessionAuxFlags{
			AskUserWaiting: auxAskUserWaiting != 0,
			HasPlan:        auxHasPlan != 0,
			HasTodos:       auxHasTodos != 0,
			HasTask:        auxHasTask != 0,
			SessionError:   strings.TrimSpace(auxSessionError),
		},
		LastContextWindow: agenttypes.ContextWindow{
			TotalTokens:        contextTotalTokens,
			ModelContextWindow: contextModelWindow,
		},
	}
	if strings.TrimSpace(relatedFilesJSON) != "" {
		if err := json.Unmarshal([]byte(relatedFilesJSON), &session.RelatedFiles); err != nil {
			session.RelatedFiles = []RelatedFile{}
		}
	}
	if strings.TrimSpace(relatedWorktreeJSON) != "" {
		var relatedWorktree RelatedWorktree
		if err := json.Unmarshal([]byte(relatedWorktreeJSON), &relatedWorktree); err == nil && strings.TrimSpace(relatedWorktree.Path) != "" {
			session.RelatedWorktree = &relatedWorktree
		}
	}
	createdAt, err := time.Parse(time.RFC3339Nano, createdAtRaw)
	if err != nil {
		createdAt = time.Time{}
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, updatedAtRaw)
	if err != nil {
		updatedAt = createdAt
	}
	session.CreatedAt = createdAt
	session.UpdatedAt = updatedAt
	if archivedAtRaw.Valid && strings.TrimSpace(archivedAtRaw.String) != "" {
		archivedAt, err := time.Parse(time.RFC3339Nano, archivedAtRaw.String)
		if err == nil {
			session.ArchivedAt = &archivedAt
		}
	}
	if closedAtRaw.Valid && strings.TrimSpace(closedAtRaw.String) != "" {
		closedAt, err := time.Parse(time.RFC3339Nano, closedAtRaw.String)
		if err == nil {
			session.ClosedAt = &closedAt
		}
	}
	normalizeSessionMeta(session)
	return session, nil
}

func normalizeSessionMeta(s *Session) {
	if s.AgentCtxSeq == nil {
		s.AgentCtxSeq = map[string]int{}
	}
	if s.RelatedFiles == nil {
		s.RelatedFiles = []RelatedFile{}
	}
	for i := range s.RelatedFiles {
		s.RelatedFiles[i].RootID = strings.TrimSpace(s.RelatedFiles[i].RootID)
		s.RelatedFiles[i].RepoPath = strings.TrimSpace(s.RelatedFiles[i].RepoPath)
		s.RelatedFiles[i].RepoName = strings.TrimSpace(s.RelatedFiles[i].RepoName)
		s.RelatedFiles[i].RepoKind = strings.TrimSpace(s.RelatedFiles[i].RepoKind)
		s.RelatedFiles[i].Path = strings.TrimSpace(s.RelatedFiles[i].Path)
		s.RelatedFiles[i].Head = strings.TrimSpace(s.RelatedFiles[i].Head)
		s.RelatedFiles[i].Relation = strings.TrimSpace(s.RelatedFiles[i].Relation)
	}
	if s.RelatedWorktree != nil {
		s.RelatedWorktree.RootID = strings.TrimSpace(s.RelatedWorktree.RootID)
		s.RelatedWorktree.Path = strings.TrimSpace(s.RelatedWorktree.Path)
		s.RelatedWorktree.Branch = strings.TrimSpace(s.RelatedWorktree.Branch)
		s.RelatedWorktree.Head = strings.TrimSpace(s.RelatedWorktree.Head)
		if s.RelatedWorktree.Path == "" {
			s.RelatedWorktree = nil
		}
	}
	if s.Exchanges == nil {
		s.Exchanges = []Exchange{}
	}
	s.ParentSessionKey = strings.TrimSpace(s.ParentSessionKey)
	s.ParentToolCallID = strings.TrimSpace(s.ParentToolCallID)
	s.Source = strings.TrimSpace(s.Source)
	s.WorkDir = strings.TrimSpace(s.WorkDir)
}

var errSessionNotFound = errors.New("session not found")
