package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	agenttypes "mindfs/server/internal/agent/types"
)

func (m *Manager) Close(ctx context.Context, key string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closeSessionUnsafe(key)
}

func (m *Manager) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.deleteSessionUnsafe(key)
}

func (m *Manager) Rename(_ context.Context, key, name string) (*Session, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return nil, errors.New("session name required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	session, err := m.getSessionUnsafe(key, 0)
	if err != nil {
		return nil, err
	}
	if session.Name == trimmed {
		// Still persist to alias so a prior alias from an earlier rename survives.
		if err := m.upsertSessionNameAliasUnsafe(key, trimmed); err != nil {
			return nil, err
		}
		for agent, agentSessionID := range m.agentSessionIDsUnsafe(session.Key) {
			if err := m.upsertExternalSessionNameUnsafe(agent, agentSessionID, trimmed); err != nil {
				return nil, err
			}
		}
		return session, nil
	}
	session.Name = trimmed
	session.UpdatedAt = m.now().UTC()
	if err := m.upsertSessionMetaUnsafe(session); err != nil {
		return nil, err
	}
	if err := m.upsertSessionNameAliasUnsafe(key, trimmed); err != nil {
		return nil, err
	}
	for agent, agentSessionID := range m.agentSessionIDsUnsafe(session.Key) {
		if err := m.upsertExternalSessionNameUnsafe(agent, agentSessionID, trimmed); err != nil {
			return nil, err
		}
	}
	return session, nil
}

// SetArchived 归档/取消归档一个会话。归档只打标记：JSONL 正文一行不动，
// 深链接仍能打开、搜索仍搜得到，只有 Delete 才真正清内容。
// 与 SetPinned 同形状：已是目标态就早返回，不白写一次库（也就不会改动 updated_at）。
func (m *Manager) SetArchived(_ context.Context, key string, archived bool) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, err := m.getSessionUnsafe(key, 0)
	if err != nil {
		return nil, err
	}
	if archived {
		if session.ArchivedAt != nil {
			return session, nil
		}
		now := m.now().UTC()
		session.ArchivedAt = &now
	} else {
		if session.ArchivedAt == nil {
			return session, nil
		}
		session.ArchivedAt = nil
	}
	if err := m.upsertSessionMetaUnsafe(session); err != nil {
		return nil, err
	}
	return session, nil
}

func (m *Manager) UpdateModel(_ context.Context, session *Session, model string) error {
	if session == nil || strings.TrimSpace(session.Key) == "" {
		return errors.New("session required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current, err := m.getSessionUnsafe(session.Key, 0)
	if err != nil {
		return err
	}
	model = strings.TrimSpace(model)
	if current.Model == model {
		return nil
	}
	current.Model = model
	current.UpdatedAt = m.now().UTC()
	return m.upsertSessionMetaUnsafe(current)
}

func (m *Manager) UpdateShell(_ context.Context, session *Session, shell string) error {
	if session == nil || strings.TrimSpace(session.Key) == "" {
		return errors.New("session required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current, err := m.getSessionUnsafe(session.Key, 0)
	if err != nil {
		return err
	}
	shell = strings.TrimSpace(shell)
	if current.Shell == shell {
		return nil
	}
	current.Shell = shell
	current.UpdatedAt = m.now().UTC()
	session.Shell = shell
	session.UpdatedAt = current.UpdatedAt
	return m.upsertSessionMetaUnsafe(current)
}

func (m *Manager) UpdatePlanMode(_ context.Context, session *Session, enabled bool) error {
	if session == nil || strings.TrimSpace(session.Key) == "" {
		return errors.New("session required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current, err := m.getSessionUnsafe(session.Key, 0)
	if err != nil {
		return err
	}
	if current.PlanMode == enabled {
		session.PlanMode = enabled
		return nil
	}
	current.PlanMode = enabled
	current.UpdatedAt = m.now().UTC()
	session.PlanMode = enabled
	session.UpdatedAt = current.UpdatedAt
	return m.upsertSessionMetaUnsafe(current)
}

func (m *Manager) UpdateLastContextWindow(_ context.Context, session *Session, contextWindow agenttypes.ContextWindow) error {
	if session == nil || strings.TrimSpace(session.Key) == "" {
		return errors.New("session required")
	}
	if !validContextWindow(contextWindow) {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current, err := m.getSessionUnsafe(session.Key, 0)
	if err != nil {
		return err
	}
	if current.LastContextWindow == contextWindow {
		session.LastContextWindow = contextWindow
		return nil
	}
	current.LastContextWindow = contextWindow
	session.LastContextWindow = contextWindow
	return m.upsertSessionMetaUnsafe(current)
}

func (m *Manager) closeSessionUnsafe(key string) (*Session, error) {
	session, err := m.getSessionUnsafe(key, 0)
	if err != nil {
		return nil, err
	}
	if session.ClosedAt != nil {
		return session, nil
	}
	now := m.now().UTC()
	session.ClosedAt = &now
	if err := m.upsertSessionMetaUnsafe(session); err != nil {
		return nil, err
	}
	return session, nil
}

func (m *Manager) deleteSessionUnsafe(key string) error {
	if strings.TrimSpace(key) == "" {
		return errors.New("session key required")
	}
	db, err := m.ensureSessionMetaDBUnsafe()
	if err != nil {
		return err
	}
	result, err := db.Exec(deleteSessionSQL, key)
	if err != nil {
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return errSessionNotFound
	}
	if _, err := db.Exec(deleteBindingsBySessionSQL, key); err != nil {
		return err
	}
	delete(m.sessions, key)
	path, err := m.exchangePath(key)
	if err != nil {
		return err
	}
	delete(m.exchangeCursors, path)
	metaDir, err := m.root.EnsureMetaDir()
	if err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(metaDir, filepath.FromSlash(path))); err != nil && !os.IsNotExist(err) {
		return err
	}
	auxPath, err := m.auxPath(key)
	if err != nil {
		return err
	}
	delete(m.exchangeCursors, auxPath)
	if err := os.Remove(filepath.Join(metaDir, filepath.FromSlash(auxPath))); err != nil && !os.IsNotExist(err) {
		return err
	}
	// 块流是正文载体（CUSTOM(G-BH)）：删会话必须连它一起删，否则正文留在盘上，
	// 既是隐私残留，也会在会话 key 复用时被当成「这个会话本来就有正文」读出来。
	blocksPath, err := m.blocksPath(key)
	if err != nil {
		return err
	}
	delete(m.exchangeCursors, blocksPath)
	if err := os.Remove(filepath.Join(metaDir, filepath.FromSlash(blocksPath))); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (m *Manager) Shutdown() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.db == nil {
		return nil
	}
	db := m.db
	m.db = nil
	return db.Close()
}
