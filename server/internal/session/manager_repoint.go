package session

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"

	agenttypes "mindfs/server/internal/agent/types"

	_ "modernc.org/sqlite"
)

func (m *Manager) RecordRelatedWorktree(_ context.Context, key, rootID, path, branch, head string) (bool, error) {
	rootID = strings.TrimSpace(rootID)
	path = strings.TrimSpace(path)
	if rootID == "" {
		return false, errors.New("root id required")
	}
	if path == "" {
		return false, errors.New("worktree path required")
	}
	if !filepath.IsAbs(path) {
		return false, errors.New("worktree path must be absolute")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	session, err := m.getSessionUnsafe(key, 0)
	if err != nil {
		return false, err
	}
	if session.RelatedWorktree != nil && strings.TrimSpace(session.RelatedWorktree.Path) != "" {
		return false, nil
	}
	session.RelatedWorktree = &RelatedWorktree{
		RootID:    rootID,
		Path:      filepath.Clean(path),
		Branch:    strings.TrimSpace(branch),
		Head:      strings.TrimSpace(head),
		UpdatedAt: m.now().UTC(),
	}
	if err := m.upsertSessionMetaUnsafe(session); err != nil {
		return false, err
	}
	return true, nil
}

// UpdateSessionAuxFlags 部分更新会话行的内容徽标（G-AD）：nil = 该位不动。
// 与 kanban.TaskStore.UpdateTaskAuxFlags 同构但互不调用 —— usecase 禁直调别域
// 存储，任务行与会话行各写各的（见 layering.md 分层铁律）。清零就是全传
// false + 空串的 patch（ClearTaskAuxFlagsForSession 那条调用链复用它）。
func (m *Manager) UpdateSessionAuxFlags(_ context.Context, key string, patch SessionAuxFlagsPatch) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("session key required")
	}
	if patch.AskUserWaiting == nil && patch.HasPlan == nil && patch.HasTodos == nil && patch.HasTask == nil && patch.SessionError == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	session, err := m.getSessionUnsafe(key, 0)
	if err != nil {
		return err
	}
	if patch.AskUserWaiting != nil {
		session.Aux.AskUserWaiting = *patch.AskUserWaiting
	}
	if patch.HasPlan != nil {
		session.Aux.HasPlan = *patch.HasPlan
	}
	if patch.HasTodos != nil {
		session.Aux.HasTodos = *patch.HasTodos
	}
	if patch.HasTask != nil {
		session.Aux.HasTask = *patch.HasTask
	}
	if patch.SessionError != nil {
		session.Aux.SessionError = strings.TrimSpace(*patch.SessionError)
	}
	if err := m.upsertSessionMetaUnsafe(session); err != nil {
		return err
	}
	return nil
}

// ClearRelatedWorktree 解除会话与工作树的归属关系。
//
// 为什么不能靠 RecordRelatedWorktree 做到：它拒绝空 path（worktree path required），
// 而且已有归属时直接返回 false —— 那是「只记一次」的语义，repoint 需要的是**清除**。
// 清空后 sessionRuntimeRootPath 返回空，运行时 cwd 回到主 checkout。
func (m *Manager) ClearRelatedWorktree(_ context.Context, key string) (bool, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return false, errors.New("session key required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	session, err := m.getSessionUnsafe(key, 0)
	if err != nil {
		return false, err
	}
	if session.RelatedWorktree == nil || strings.TrimSpace(session.RelatedWorktree.Path) == "" {
		return false, nil
	}
	session.RelatedWorktree = nil
	if err := m.upsertSessionMetaUnsafe(session); err != nil {
		return false, err
	}
	return true, nil
}

func (m *Manager) RepointAgentBinding(sessionKey, agent, previousAgentSessionID, agentSessionID string, cursor agenttypes.ExternalSessionCursor) error {
	sessionKey = strings.TrimSpace(sessionKey)
	agent = strings.TrimSpace(agent)
	agentSessionID = strings.TrimSpace(agentSessionID)
	if sessionKey == "" {
		return errors.New("session key required")
	}
	if agent == "" {
		return errors.New("agent required")
	}
	if agentSessionID == "" {
		return errors.New("agent session id required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	db, err := m.ensureSessionMetaDBUnsafe()
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// ctx_seq 必须**原样保留**。它是 Full 同步的幂等护栏：externalSessionDeltaAfterCtxSeq
	// 用它切掉「库里已有」的前缀，写 0 会让 `agentCtxSeq <= 0` 分支直接放行全部，
	// 下次点「同步」就把早已渲染过的回合重导一遍 —— 2026-09-16 那个 bug 的形态。
	// repoint 只换 id 和游标、不增删 exchange，所以原值就是正确值。
	// 读和写都在事务内，避免并发同步在这两步之间改掉它。
	var existing AgentBinding
	if err := scanAgentBinding(tx.QueryRow(selectAgentBindingSQL, sessionKey, agent), &existing); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err := tx.Exec(upsertAgentBindingSQL, sessionKey, agent, agentSessionID, existing.AgentCtxSeq); err != nil {
		return err
	}

	if _, err := tx.Exec(
		`UPDATE session_agent_bindings SET external_source_path = ?, external_source_offset = ?, external_source_mtime_ns = ?, external_source_committed_offset = ? WHERE session_key = ? AND agent = ?`,
		strings.TrimSpace(cursor.SourcePath), cursor.Offset, cursor.ModTimeUnixNano, cursor.CommittedOffset, sessionKey, agent,
	); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// 名字跟着换 id，否则重开会话时按新 agent_session_id 查不到名字、会退回默认名。
	// 这条走 session_external_names —— 它才是 LookupAliasForAgent 真正读的表
	//（selectExternalSessionNameSQL）。放在事务外：它写的是另一张表，失败不影响
	// 「id + 游标」这个必须原子的一致性。
	//
	// 旧 id 的那条必须删掉：upsertExternalSessionNameUnsafe 只 upsert 新 id，旧行会一直
	// 留着并继续被旧 id 查到。两个 id 同时能查到同一个名字，下次按旧 id 导入时就会
	// 认领到一条本该属于别的会话的名字。

	if previousAgentSessionID = strings.TrimSpace(previousAgentSessionID); previousAgentSessionID != "" && previousAgentSessionID != agentSessionID {
		if _, err := db.Exec(`DELETE FROM session_external_names WHERE agent = ? AND agent_session_id = ?`, agent, previousAgentSessionID); err != nil {
			return err
		}
	}
	current, err := m.getSessionUnsafe(sessionKey, 0)
	if err != nil {
		return err
	}
	return m.upsertExternalSessionNameUnsafe(agent, agentSessionID, current.Name)
}
