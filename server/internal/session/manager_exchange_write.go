package session

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	agenttypes "mindfs/server/internal/agent/types"

	_ "modernc.org/sqlite"
)

func (m *Manager) AddExchangeForAgent(ctx context.Context, session *Session, role, content, agent, mode, effort, fastService string) error {
	_, err := m.AddExchangeForAgentAndGetSeq(ctx, session, role, content, agent, mode, effort, fastService)
	return err
}

func (m *Manager) AddExchangeForAgentAt(ctx context.Context, session *Session, role, content, agent, mode, effort, fastService string, timestamp time.Time) error {
	_, err := m.addExchangeForAgentAtWithIdentityFactory(session, role, content, agent, exchangeModelDisplayNameFromContext(ctx), exchangeTokenUsageFromContext(ctx), exchangeSourceFromContext(ctx), exchangeIdentityFromContext(ctx), nil, mode, effort, fastService, timestamp)
	return err
}

// AddExchangeForAgentAndGetSeq appends an exchange and returns the seq allocated
// from the manager's current, locked view of the session. Callers that attach
// blocks or auxiliary data must use this value instead of inspecting a possibly
// stale caller-owned Session pointer.
func (m *Manager) AddExchangeForAgentAndGetSeq(ctx context.Context, session *Session, role, content, agent, mode, effort, fastService string) (int, error) {
	return m.addExchangeForAgentAtWithIdentityFactory(session, role, content, agent, exchangeModelDisplayNameFromContext(ctx), exchangeTokenUsageFromContext(ctx), exchangeSourceFromContext(ctx), exchangeIdentityFromContext(ctx), nil, mode, effort, fastService, time.Time{})
}

// AddExchangeForAgentAtWithIdentityFactory allocates the row seq under the
// manager lock, then builds identity from that exact seq before persisting it.
// This keeps deterministic user-turn IDs and turn_seq aligned with the row even
// if the caller's session snapshot was stale.
func (m *Manager) AddExchangeForAgentAtWithIdentityFactory(ctx context.Context, session *Session, role, content, agent, mode, effort, fastService string, timestamp time.Time, identityForSeq func(int) ExchangeIdentity) (int, error) {
	if identityForSeq == nil {
		return 0, errors.New("exchange identity factory required")
	}
	return m.addExchangeForAgentAtWithIdentityFactory(session, role, content, agent, exchangeModelDisplayNameFromContext(ctx), exchangeTokenUsageFromContext(ctx), exchangeSourceFromContext(ctx), exchangeIdentityFromContext(ctx), identityForSeq, mode, effort, fastService, timestamp)
}

func (m *Manager) addExchangeForAgentAtWithIdentityFactory(session *Session, role, content, agent, modelDisplayName string, tokenUsage *agenttypes.TokenUsage, source string, identity ExchangeIdentity, identityForSeq func(int) ExchangeIdentity, mode, effort, fastService string, timestamp time.Time) (int, error) {
	if session == nil || strings.TrimSpace(session.Key) == "" {
		return 0, errors.New("session required")
	}
	callerSession := session
	// S2d-2 起正文归块流，主日志一行的 `content` 恒空 —— 这里再按「空正文」跳过就把
	// **每一行 user**（用户消息不再带正文）静默丢掉：轮次元数据（mindfs_id / agent_id）
	// 跟着没，前端按 seq 合并时那句话不显示，搜索的 user 命中优先也失效。
	// 该拦的是「什么都没带」的纯空行：空正文 + 空 agent + 无任何轮次身份。
	if strings.TrimSpace(content) == "" && strings.TrimSpace(agent) == "" &&
		identity.MindFSID == "" && identity.AgentID == "" && identityForSeq == nil {
		return 0, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current, err := m.getSessionUnsafe(session.Key, 0)
	if err != nil {
		return 0, err
	}
	session = current
	if session.ClosedAt != nil {
		session.ClosedAt = nil
	}
	resolvedAgent := strings.TrimSpace(agent)
	// seq 按「最大 seq + 1」分配，不按缓存长度：文件与缓存一旦漂移（外部写入、手工修文件、
	// 缓存陈旧），长度+1 会撞上已有 seq（前端按 seq 合并 → 消息直接不显示）。
	nextSeq := maxExchangeSeq(session.Exchanges) + 1
	if identityForSeq != nil {
		identity = identityForSeq(nextSeq)
	}
	ts := timestamp.UTC()
	if ts.IsZero() {
		ts = m.now().UTC()
	}
	record := Exchange{
		Seq:              nextSeq,
		Role:             role,
		Source:           source,
		Agent:            resolvedAgent,
		Model:            session.Model,
		ModelDisplayName: strings.TrimSpace(modelDisplayName),
		Mode:             strings.TrimSpace(mode),
		Effort:           strings.TrimSpace(effort),
		FastService:      fastService,
		LegacyContent:    content,
		TokenUsage:       tokenUsage,
		Timestamp:        ts,
		// 轮次元数据（方案 §3.1）。MindFSID 是权威身份，TurnSeq 是人读排序键，
		// AgentID 是 agent 侧桥梁键（可空、可后补、fork 后可变更）。
		MindFSID: identity.MindFSID,
		TurnSeq:  identity.TurnSeq,
		AgentID:  identity.AgentID,
		Partial:  identity.Partial,
	}
	if err := m.appendExchange(session.Key, record); err != nil {
		log.Printf("[session/store] append.error session=%s seq=%d role=%s agent=%s err=%v", session.Key, record.Seq, role, resolvedAgent, err)
		return 0, err
	}
	session.Exchanges = append(session.Exchanges, record)
	session.UpdatedAt = record.Timestamp
	if resolvedAgent != "" {
		if session.AgentCtxSeq == nil {
			session.AgentCtxSeq = map[string]int{}
		}
		if _, ok := session.AgentCtxSeq[resolvedAgent]; !ok {
			session.AgentCtxSeq[resolvedAgent] = 0
		}
	}
	if err := m.upsertSessionMetaUnsafe(session); err != nil {
		if callerSession != session {
			*callerSession = *session
		}
		return record.Seq, err
	}
	// getSessionUnsafe may have replaced a stale cache entry with a freshly loaded
	// pointer. Keep the caller's snapshot usable after a successful append while
	// retaining the manager's canonical cached pointer.
	if callerSession != session {
		*callerSession = *session
	}
	return record.Seq, nil
}

func (m *Manager) AddExchangeAux(_ context.Context, sessionKey string, aux ExchangeAux) error {
	if strings.TrimSpace(sessionKey) == "" {
		return errors.New("session key required")
	}
	if aux.Seq <= 0 {
		return errors.New("aux seq required")
	}
	if aux.ToolCall == nil && strings.TrimSpace(aux.Thought) == "" && aux.Todo == nil && aux.Plan == nil && aux.Compact == nil {
		return errors.New("aux content required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.appendExchangeAux(sessionKey, aux)
}
