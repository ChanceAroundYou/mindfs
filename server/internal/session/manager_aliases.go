package session

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

func (m *Manager) upsertSessionNameAliasUnsafe(key, name string) error {
	key = strings.TrimSpace(key)
	name = strings.TrimSpace(name)
	if key == "" || name == "" {
		return nil
	}
	db, err := m.ensureSessionMetaDBUnsafe()
	if err != nil {
		return err
	}
	// Persist alias keyed by internal key, plus best-known agent external identity
	// so a later re-import (new key, same agent_session_id) still resumes the alias.
	agent := ""
	agentSessionID := ""
	if rows, qErr := db.Query(`SELECT agent, agent_session_id FROM session_agent_bindings WHERE session_key = ? LIMIT 1`, key); qErr == nil {
		if rows.Next() {
			_ = rows.Scan(&agent, &agentSessionID)
		}
		_ = rows.Close()
	}
	_, err = db.Exec(upsertSessionNameAliasSQL, key, name, strings.TrimSpace(agent), strings.TrimSpace(agentSessionID), m.now().UTC().Format(time.RFC3339Nano))
	return err
}

func (m *Manager) upsertExternalSessionNameUnsafe(agent, agentSessionID, name string) error {
	agent = strings.TrimSpace(agent)
	agentSessionID = strings.TrimSpace(agentSessionID)
	name = strings.TrimSpace(name)
	if agent == "" || agentSessionID == "" || name == "" || name == "New Session" {
		return nil
	}
	db, err := m.ensureSessionMetaDBUnsafe()
	if err != nil {
		return err
	}
	_, err = db.Exec(upsertExternalSessionNameSQL, agent, agentSessionID, name, m.now().UTC().Format(time.RFC3339Nano))
	return err
}

func (m *Manager) agentSessionIDsUnsafe(key string) map[string]string {
	db, err := m.ensureSessionMetaDBUnsafe()
	if err != nil {
		return nil
	}
	rows, err := db.Query(`SELECT agent, agent_session_id FROM session_agent_bindings WHERE session_key = ?`, strings.TrimSpace(key))
	if err != nil {
		return nil
	}
	defer rows.Close()
	ids := make(map[string]string)
	for rows.Next() {
		var agent, agentSessionID string
		if err := rows.Scan(&agent, &agentSessionID); err != nil {
			return nil
		}
		ids[strings.TrimSpace(agent)] = strings.TrimSpace(agentSessionID)
	}
	if err := rows.Err(); err != nil {
		return nil
	}
	return ids
}

func (m *Manager) lookupSessionAliasForAgentUnsafe(agent, agentSessionID string) (string, bool) {
	agent = strings.TrimSpace(agent)
	agentSessionID = strings.TrimSpace(agentSessionID)
	if agent == "" || agentSessionID == "" {
		return "", false
	}
	db, err := m.ensureSessionMetaDBUnsafe()
	if err != nil {
		return "", false
	}
	var name string
	if err := db.QueryRow(selectExternalSessionNameSQL, agent, agentSessionID).Scan(&name); err != nil {
		return "", false
	}
	if strings.TrimSpace(name) == "" {
		return "", false
	}
	return strings.TrimSpace(name), true
}

func (m *Manager) LookupAliasForAgent(agent, agentSessionID string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lookupSessionAliasForAgentUnsafe(agent, agentSessionID)
}

func (m *Manager) applySessionNameAliasesUnsafe(items []*Session) error {
	if len(items) == 0 {
		return nil
	}
	db, err := m.ensureSessionMetaDBUnsafe()
	if err != nil {
		return err
	}
	keys := make([]any, 0, len(items))
	for _, it := range items {
		keys = append(keys, it.Key)
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(keys)), ",")
	rows, err := db.Query(`SELECT key, name FROM session_name_aliases WHERE key IN (`+ph+`)`, keys...)
	if err != nil {
		return err
	}
	defer rows.Close()
	byKey := make(map[string]string, len(items))
	for rows.Next() {
		var k, n string
		if err := rows.Scan(&k, &n); err != nil {
			return err
		}
		if v := strings.TrimSpace(n); v != "" {
			byKey[strings.TrimSpace(k)] = v
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, it := range items {
		if v := byKey[strings.TrimSpace(it.Key)]; v != "" {
			it.Name = v
		}
	}
	// Also overlay aliases that were stored by agent_session_id for re-imported sessions
	// whose internal key changed, but never overwrite an existing session name.
	for _, it := range items {
		if byKey[strings.TrimSpace(it.Key)] != "" || strings.TrimSpace(it.Name) != "" {
			continue
		}
		// Prefer bindings already loaded by fillSessionBindingsUnsafe; fallback to DB lookup
		agent := InferAgentFromSession(it)
		var agentSessionID string
		if rows2, qErr := db.Query(`SELECT agent_session_id FROM session_agent_bindings WHERE session_key = ? LIMIT 1`, it.Key); qErr == nil {
			if rows2.Next() {
				_ = rows2.Scan(&agentSessionID)
			}
			_ = rows2.Close()
		}
		if v, ok := m.lookupSessionAliasForAgentUnsafe(agent, agentSessionID); ok {
			it.Name = v
		}
	}
	return nil
}

func (m *Manager) applySessionNameAliasSingleUnsafe(s *Session) error {
	if s == nil || strings.TrimSpace(s.Key) == "" {
		return nil
	}
	db, err := m.ensureSessionMetaDBUnsafe()
	if err != nil {
		return err
	}
	var alias string
	err = db.QueryRow(selectSessionNameAliasSQL, strings.TrimSpace(s.Key)).Scan(&alias)
	if err == nil {
		if v := strings.TrimSpace(alias); v != "" {
			s.Name = v
			return nil
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	// Fallback: alias was stored under external identity (re-import after delete retains manual name)
	agent := InferAgentFromSession(s)
	var agentSessionID string
	if rows, qErr := db.Query(`SELECT agent_session_id FROM session_agent_bindings WHERE session_key = ? LIMIT 1`, s.Key); qErr == nil {
		if rows.Next() {
			_ = rows.Scan(&agentSessionID)
		}
		_ = rows.Close()
	}
	if v, ok := m.lookupSessionAliasForAgentUnsafe(agent, agentSessionID); ok {
		s.Name = v
	}
	return nil
}
