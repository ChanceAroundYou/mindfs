package session

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
)

func (m *Manager) UpdateAgentState(_ context.Context, session *Session, agent string, lastCtxSeq int, agentSessionID string) error {
	if session == nil || strings.TrimSpace(session.Key) == "" {
		return errors.New("session required")
	}
	if strings.TrimSpace(agent) == "" {
		return errors.New("agent required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current, err := m.getSessionUnsafe(session.Key, 0)
	if err != nil {
		return err
	}
	session = current
	if session.AgentCtxSeq == nil {
		session.AgentCtxSeq = map[string]int{}
	}
	if lastCtxSeq >= 0 {
		session.AgentCtxSeq[agent] = lastCtxSeq
	}
	if strings.TrimSpace(agentSessionID) == "" {
		return nil
	}
	if err := m.upsertAgentBindingUnsafe(AgentBinding{
		SessionKey:     strings.TrimSpace(session.Key),
		Agent:          strings.TrimSpace(agent),
		AgentSessionID: strings.TrimSpace(agentSessionID),
		AgentCtxSeq:    lastCtxSeq,
	}); err != nil {
		return err
	}
	return m.upsertExternalSessionNameUnsafe(agent, agentSessionID, session.Name)
}

func (m *Manager) UpsertAgentBinding(_ context.Context, binding AgentBinding) error {
	if strings.TrimSpace(binding.SessionKey) == "" {
		return errors.New("session key required")
	}
	if strings.TrimSpace(binding.Agent) == "" {
		return errors.New("agent required")
	}
	if strings.TrimSpace(binding.AgentSessionID) == "" {
		return errors.New("agent session id required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.upsertAgentBindingUnsafe(binding); err != nil {
		return err
	}
	current, err := m.getSessionUnsafe(binding.SessionKey, 0)
	if err != nil {
		return err
	}
	return m.upsertExternalSessionNameUnsafe(binding.Agent, binding.AgentSessionID, current.Name)
}

// RepointAgentBinding 在**同一个事务**里换 agent_session_id 并改写游标。
//
// 为什么不复用 UpsertAgentBinding + UpdateExternalSessionCursor 分两次写：repoint 的
// 语义要求「换 id」和「游标指向新转录的末尾」同时成立。分两次写时，若第二次失败，
// binding 已经是新 id、游标却还指着旧路径的冻结偏移——导入器的 externalSessionFileCursor
// 按 SourcePath 判「变没变」，路径一变就算变了，于是从 committed（live-owned 会话为 0）
// 整份重读，正是 2026-09-16「ask 下面又渲染了一轮出现过的文字」的成因。合成一个事务后
// 要么都成、要么都没动，不存在这个中间态。

func (m *Manager) UpdateExternalSessionCursor(_ context.Context, sessionKey, agent string, cursor agenttypes.ExternalSessionCursor) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	db, err := m.ensureSessionMetaDBUnsafe()
	if err != nil {
		return err
	}
	_, err = db.Exec(`UPDATE session_agent_bindings SET external_source_path = ?, external_source_offset = ?, external_source_mtime_ns = ?, external_source_committed_offset = ? WHERE session_key = ? AND agent = ?`,
		strings.TrimSpace(cursor.SourcePath), cursor.Offset, cursor.ModTimeUnixNano, cursor.CommittedOffset, strings.TrimSpace(sessionKey), strings.TrimSpace(agent))
	return err
}

func (m *Manager) GetAgentBinding(_ context.Context, sessionKey, agent string) (*AgentBinding, error) {
	if strings.TrimSpace(sessionKey) == "" {
		return nil, errors.New("session key required")
	}
	if strings.TrimSpace(agent) == "" {
		return nil, errors.New("agent required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	db, err := m.ensureSessionMetaDBUnsafe()
	if err != nil {
		return nil, err
	}
	row := db.QueryRow(selectAgentBindingSQL, strings.TrimSpace(sessionKey), strings.TrimSpace(agent))
	var binding AgentBinding
	if err := scanAgentBinding(row, &binding); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errSessionNotFound
		}
		return nil, err
	}
	return &binding, nil
}

func (m *Manager) FindAgentBinding(ctx context.Context, sessionKey, agent string) (*AgentBinding, error) {
	binding, err := m.GetAgentBinding(ctx, sessionKey, agent)
	if errors.Is(err, errSessionNotFound) {
		return nil, nil
	}
	return binding, err
}

// EnsureAgentBinding 记下「这个会话在用哪个 agent」，幂等。
// 已有绑定行时（不论 agent_session_id 是否为空）原样返回，绝不覆盖真实转录 id。
//
// 约定：空 agent_session_id 表示「agent 已选定、尚无转录」——所有读者本来就把空 id
// 当成无绑定处理（别名回填/外部名同步/repoint/fork 都有空 id 守卫），唯一的可见效果
// 是列表路径拿到 AgentCtxSeq 单键 → InferAgentFromSession 能报出 agent，徽标不再落回「AI」。
func (m *Manager) EnsureAgentBinding(_ context.Context, sessionKey, agent string) error {
	if strings.TrimSpace(sessionKey) == "" {
		return errors.New("session key required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ensureAgentBindingUnsafe(sessionKey, agent)
}

func (m *Manager) ensureAgentBindingUnsafe(sessionKey, agent string) error {
	agent = strings.TrimSpace(agent)
	if agent == "" {
		return nil
	}
	sessionKey = strings.TrimSpace(sessionKey)
	db, err := m.ensureSessionMetaDBUnsafe()
	if err != nil {
		return err
	}
	var existing AgentBinding
	err = scanAgentBinding(db.QueryRow(selectAgentBindingSQL, sessionKey, agent), &existing)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	// agent_ctx_seq 必须留 0：非 0 会被 prependSwitchHint 当成「已同步到此行」而吞掉 agent 切换提示。
	_, err = db.Exec(upsertAgentBindingSQL, sessionKey, agent, "", 0)
	return err
}

func (m *Manager) FindAgentBindingByAgentSession(_ context.Context, agent, agentSessionID string) (*AgentBinding, error) {
	if strings.TrimSpace(agent) == "" {
		return nil, errors.New("agent required")
	}
	if strings.TrimSpace(agentSessionID) == "" {
		return nil, errors.New("agent session id required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	db, err := m.ensureSessionMetaDBUnsafe()
	if err != nil {
		return nil, err
	}
	var binding AgentBinding
	row := db.QueryRow(
		selectBindingByAgentSessionSQL,
		strings.TrimSpace(agent),
		strings.TrimSpace(agentSessionID),
	)
	err = scanAgentBinding(row, &binding)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &binding, nil
}

func (m *Manager) HasAgentBinding(ctx context.Context, agent, agentSessionID string) (bool, error) {
	binding, err := m.FindAgentBindingByAgentSession(ctx, agent, agentSessionID)
	if err != nil {
		return false, err
	}
	return binding != nil, nil
}

func (m *Manager) listAgentBindingsUnsafe(sessionKey string) ([]AgentBinding, error) {
	db, err := m.ensureSessionMetaDBUnsafe()
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(selectAgentBindingsBySessionSQL, strings.TrimSpace(sessionKey))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	bindings := make([]AgentBinding, 0)
	for rows.Next() {
		var binding AgentBinding
		if err := scanAgentBinding(rows, &binding); err != nil {
			return nil, err
		}
		bindings = append(bindings, binding)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return bindings, nil
}

func scanAgentBinding(scanner rowScanner, binding *AgentBinding) error {
	return scanner.Scan(&binding.SessionKey, &binding.Agent, &binding.AgentSessionID, &binding.AgentCtxSeq, &binding.ExternalSourcePath, &binding.ExternalSourceOffset, &binding.ExternalSourceMtimeNS, &binding.ExternalSourceCommittedOffset)
}

func (m *Manager) upsertAgentBindingUnsafe(binding AgentBinding) error {
	db, err := m.ensureSessionMetaDBUnsafe()
	if err != nil {
		return err
	}
	agentCtxSeq := 0
	if binding.AgentCtxSeq > 0 {
		agentCtxSeq = binding.AgentCtxSeq
	}
	_, err = db.Exec(
		upsertAgentBindingSQL,
		strings.TrimSpace(binding.SessionKey),
		strings.TrimSpace(binding.Agent),
		strings.TrimSpace(binding.AgentSessionID),
		agentCtxSeq,
	)
	if err != nil {
		return err
	}
	// Rename may have happened before the first agent binding existed, leaving the alias
	// stored with empty agent identity. Backfill it now so a later delete+reimport via
	// LookupAliasForAgent still finds the manual name.
	if key := strings.TrimSpace(binding.SessionKey); key != "" {
		_, _ = db.Exec(
			`UPDATE session_name_aliases SET agent = ?, agent_session_id = ?, updated_at = ? WHERE key = ?`,
			strings.TrimSpace(binding.Agent),
			strings.TrimSpace(binding.AgentSessionID),
			m.now().UTC().Format(time.RFC3339Nano),
			key,
		)
	}
	return nil
}
