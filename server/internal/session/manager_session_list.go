package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (m *Manager) List(_ context.Context, opts ListOptions) ([]*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.listSessionsUnsafe(opts)
}

// ListByKeys 按 key 取会话元数据（不读转录正文），按传入顺序返回。
//
// 与 Get 的区别是刻意不碰转录：置顶表里可能装着**别的机器**上的会话键，
// 而本机的会话库里根本没有那些行。Get 会去 loadSession 读 JSONL，
// 对一个本机不存在的 key 那是白读一趟；对存在的 key 又太重（要按需拉正文）。
// 这里只查 session meta 表，命中不了就是命中不了，交给调用方丢弃。
func (m *Manager) ListByKeys(_ context.Context, keys []string) ([]*Session, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.ensureSessionMetaDBUnsafe(); err != nil {
		return nil, err
	}
	unique := make([]string, 0, len(keys))
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, key)
	}
	if len(unique) == 0 {
		return nil, nil
	}
	query := selectSessionSQL + `
WHERE key IN (?` + strings.Repeat(`,?`, len(unique)-1) + `)`
	args := make([]any, 0, len(unique))
	for _, key := range unique {
		args = append(args, key)
	}
	found, err := m.querySessionMetasUnsafe(query, args)
	if err != nil {
		return nil, err
	}
	byKey := make(map[string]*Session, len(found))
	for _, s := range found {
		if s != nil {
			byKey[s.Key] = s
		}
	}
	out := make([]*Session, 0, len(found))
	for _, key := range unique {
		if s, ok := byKey[key]; ok {
			out = append(out, s)
		}
	}
	return out, nil
}

func (m *Manager) Count(_ context.Context, opts ListOptions) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.countSessionsUnsafe(opts)
}

// LatestSessionUpdatedAt returns the newest visible session timestamp in this
// root, including child sessions. It reads the indexed metadata table, so the
// multi-root sidebar does not have to load every transcript just to rank roots.
func (m *Manager) LatestSessionUpdatedAt(_ context.Context) (time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	db, err := m.ensureSessionMetaDBUnsafe()
	if err != nil {
		return time.Time{}, err
	}
	var latest sql.NullString
	if err := db.QueryRow(`
		SELECT updated_at
		FROM sessions
		WHERE archived_at IS NULL
		ORDER BY julianday(updated_at) DESC
		LIMIT 1
	`).Scan(&latest); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, err
	}
	if !latest.Valid || strings.TrimSpace(latest.String) == "" {
		return time.Time{}, nil
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, latest.String)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse latest session timestamp %q: %w", latest.String, err)
	}
	return updatedAt, nil
}

func (m *Manager) ListMetas(_ context.Context) ([]*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.listSessionMetasUnsafe()
}

func (m *Manager) listSessionMetasUnsafe() ([]*Session, error) {
	return m.querySessionMetasUnsafe(selectSessionSQL+`
ORDER BY updated_at DESC`, nil)
}

// querySessionMetasUnsafe 一次拉取 session meta（不含 exchanges 文件），并单次 JOIN 填充 agent bindings。
// 列表/搜索只需 meta；此前逐 key 调 getSessionUnsafe 会导致每个会话全量读 JSONL。
func (m *Manager) querySessionMetasUnsafe(query string, args []any) ([]*Session, error) {
	db, err := m.ensureSessionMetaDBUnsafe()
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	items := make([]*Session, 0)
	for rows.Next() {
		item, err := scanSessionMetaRow(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if len(items) > 0 {
		if err := m.fillSessionBindingsUnsafe(items); err != nil {
			return nil, err
		}
		if err := m.applySessionNameAliasesUnsafe(items); err != nil {
			return nil, err
		}
	}
	return items, nil
}

// fillSessionBindingsUnsafe 用单个 IN 查询拉取一批 session 的 bindings，替代逐 session N+1 查询。
func (m *Manager) fillSessionBindingsUnsafe(items []*Session) error {
	db, err := m.ensureSessionMetaDBUnsafe()
	if err != nil {
		return err
	}
	keys := make([]any, 0, len(items))
	for _, item := range items {
		keys = append(keys, item.Key)
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(keys)), ",")
	rows, err := db.Query(selectAllAgentBindingsSQL+` WHERE session_key IN (`+placeholders+`)`, keys...)
	if err != nil {
		return err
	}
	defer rows.Close()
	byKey := make(map[string][]AgentBinding, len(items))
	for rows.Next() {
		var binding AgentBinding
		if err := scanAgentBinding(rows, &binding); err != nil {
			return err
		}
		byKey[binding.SessionKey] = append(byKey[binding.SessionKey], binding)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, item := range items {
		for _, binding := range byKey[item.Key] {
			if strings.TrimSpace(binding.Agent) == "" {
				continue
			}
			item.AgentCtxSeq[binding.Agent] = binding.AgentCtxSeq
		}
	}
	return nil
}

func (m *Manager) listSessionsUnsafe(opts ListOptions) ([]*Session, error) {
	query := selectSessionSQL
	where, args := sessionListWhere(opts)
	if len(where) > 0 {
		query += `
WHERE ` + strings.Join(where, " AND ")
	}
	query += `
ORDER BY updated_at DESC`
	if opts.Limit > 0 {
		query += `
LIMIT ?`
		args = append(args, opts.Limit)
	}
	return m.querySessionMetasUnsafe(query, args)
}

func (m *Manager) countSessionsUnsafe(opts ListOptions) (int, error) {
	db, err := m.ensureSessionMetaDBUnsafe()
	if err != nil {
		return 0, err
	}
	query := `
SELECT COUNT(*) FROM sessions`
	where, args := sessionListWhere(opts)
	if len(where) > 0 {
		query += `
WHERE ` + strings.Join(where, " AND ")
	}
	var count int
	if err := db.QueryRow(query, args...).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func sessionListWhere(opts ListOptions) ([]string, []any) {
	args := make([]any, 0, 2)
	where := make([]string, 0, 2)
	if parentKey := strings.TrimSpace(opts.ParentSessionKey); parentKey != "" {
		where = append(where, "parent_session_key = ?")
		args = append(args, parentKey)
	} else if opts.TopLevelOnly {
		where = append(where, "parent_session_key = ''")
	}
	if !opts.BeforeTime.IsZero() {
		where = append(where, "updated_at < ?")
		args = append(args, opts.BeforeTime.UTC().Format(time.RFC3339Nano))
	} else if !opts.AfterTime.IsZero() {
		where = append(where, "updated_at > ?")
		args = append(args, opts.AfterTime.UTC().Format(time.RFC3339Nano))
	}
	// 归档过滤放在这里（唯一的 WHERE 构造器），四个列表入口一次覆盖：
	// 主列表、置顶列表、计数、子会话列表。置顶的归档会话会从置顶区掉进归档区 ——
	// 这是期望行为，归档就该从主视图消失。
	if strings.TrimSpace(opts.ArchivedMode) == "only" {
		where = append(where, "archived_at IS NOT NULL")
	} else {
		where = append(where, "archived_at IS NULL")
	}
	return where, args
}
