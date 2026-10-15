package session

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	configpkg "mindfs/server/internal/config"

	_ "modernc.org/sqlite"
)

var openSQLiteDB = func(path string) (*sql.DB, error) {
	return sql.Open("sqlite", path)
}

var mindFSConfigDir = configpkg.MindFSConfigDir

func (m *Manager) ensureSessionMetaDBUnsafe() (*sql.DB, error) {
	if m.db != nil {
		return m.db, nil
	}
	metaDir, err := m.root.EnsureMetaDir()
	if err != nil {
		return nil, err
	}
	legacyDBFile := filepath.Join(metaDir, filepath.FromSlash(sessionDBPath))
	linkFile := legacyDBFile + sessionDBLinkExt
	if linkedDBFile, ok, err := readSessionDBLink(linkFile); err != nil {
		return nil, err
	} else if ok {
		db, err := openSessionMetaDB(linkedDBFile)
		if err != nil {
			return nil, err
		}
		m.db = db
		return m.db, nil
	}

	db, err := openSessionMetaDB(legacyDBFile)
	if err == nil {
		m.db = db
		return m.db, nil
	}
	legacyErr := err

	// 本地会话库已存在却打不开时，绝不回退到空库：回退会让全部历史会话在界面上凭空消失，
	// 而根因（多半是某条 schema 迁移/索引在旧库上报错）仍然留在原地，下次启动照样复现。
	// 只有本地库压根不存在时，"从零新建"才是正确行为。
	if _, statErr := os.Stat(legacyDBFile); statErr == nil {
		return nil, fmt.Errorf("open legacy session db: %w (refusing to fall back to an empty db: %s exists)", legacyErr, legacyDBFile)
	} else if !os.IsNotExist(statErr) {
		return nil, fmt.Errorf("open legacy session db: %w; stat %s: %v", legacyErr, legacyDBFile, statErr)
	}

	fallbackDBFile, err := userDataSessionDBFile(m.root.ID)
	if err != nil {
		return nil, fmt.Errorf("open legacy session db: %w; resolve fallback session db: %w", legacyErr, err)
	}
	db, err = openSessionMetaDB(fallbackDBFile)
	if err != nil {
		return nil, fmt.Errorf("open legacy session db: %w; open fallback session db: %w", legacyErr, err)
	}
	if err := writeSessionDBLink(linkFile, fallbackDBFile); err != nil {
		db.Close()
		return nil, fmt.Errorf("open legacy session db: %w; write session db link: %w", legacyErr, err)
	}
	log.Printf("[session/store] sqlite fallback root=%s legacy=%s fallback=%s err=%v", m.root.ID, legacyDBFile, fallbackDBFile, legacyErr)
	m.db = db
	return m.db, nil
}

func openSessionMetaDB(dbFile string) (db *sql.DB, err error) {
	defer func() {
		if r := recover(); r != nil {
			if db != nil {
				db.Close()
			}
			err = fmt.Errorf("sqlite init panic for %s: %v", dbFile, r)
		}
	}()
	if err := os.MkdirAll(filepath.Dir(dbFile), 0o755); err != nil {
		return nil, err
	}
	db, err = openSQLiteDB(dbFile)
	if err != nil {
		return nil, err
	}
	// WAL：读写不互相阻塞，避免高频读（列表/搜索/Get）阻塞低频写；busy_timeout 处理写锁竞争。
	// 多读连接：列表/搜索等读操作可并发，写仍由 SQLite 锁 + busy_timeout 保证串行。
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(`PRAGMA busy_timeout=5000`); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(sessionTableSchema); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(agentBindingTableSchema); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(sessionNameAliasTableSchema); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(sessionExternalNameTableSchema); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(`ALTER TABLE sessions ADD COLUMN model TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(`ALTER TABLE sessions ADD COLUMN shell TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(`ALTER TABLE sessions ADD COLUMN plan_mode INTEGER NOT NULL DEFAULT 0`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
		db.Close()
		return nil, err
	}
	for _, stmt := range []string{
		`ALTER TABLE sessions ADD COLUMN parent_session_key TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sessions ADD COLUMN parent_tool_call_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sessions ADD COLUMN source TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sessions ADD COLUMN task_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sessions ADD COLUMN related_worktree_json TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sessions ADD COLUMN aux_ask_user_waiting INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE sessions ADD COLUMN aux_has_plan INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE sessions ADD COLUMN aux_has_todos INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE sessions ADD COLUMN aux_has_task INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE sessions ADD COLUMN aux_session_error TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sessions ADD COLUMN last_context_window_total_tokens INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE sessions ADD COLUMN last_context_window_model_context_window INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE sessions ADD COLUMN pinned_at TEXT`,
		`ALTER TABLE sessions ADD COLUMN archived_at TEXT`,
		`ALTER TABLE session_agent_bindings ADD COLUMN external_source_path TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE session_agent_bindings ADD COLUMN external_source_offset INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE session_agent_bindings ADD COLUMN external_source_mtime_ns INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE session_agent_bindings ADD COLUMN external_source_committed_offset INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE session_name_aliases ADD COLUMN agent TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE session_name_aliases ADD COLUMN agent_session_id TEXT NOT NULL DEFAULT ''`,
	} {
		if _, err := db.Exec(stmt); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
			db.Close()
			return nil, err
		}
	}
	// 索引必须建在列补齐之后：CREATE INDEX 引用尚不存在的列会报 "no such column"，
	// 让整个 openSessionMetaDB 失败，进而把完好的旧库误判成不可用并静默回退到空库。
	// 列表/搜索按 updated_at DESC 排序，无索引时全表排序。
	// 不再建 idx_sessions_pinned_at：该列已停用（见 selectSessionSQL 上的 CUSTOM(G-X)），
	// 索引只会给一个恒为 NULL 的列白占写入成本。存量索引由旧版建过，留着也无害。
	for _, stmt := range []string{
		`CREATE INDEX IF NOT EXISTS idx_sessions_updated_at ON sessions(updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_archived_at ON sessions(archived_at, updated_at DESC)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			db.Close()
			return nil, err
		}
	}
	if normalized, err := normalizeForkParentLinks(db); err != nil {
		db.Close()
		return nil, err
	} else if normalized > 0 {
		log.Printf("[session/store] normalized fork parent links db=%s count=%d", dbFile, normalized)
	}
	if err := normalizeExternalSessionNames(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func normalizeExternalSessionNames(db *sql.DB) error {
	_, err := db.Exec(`
		INSERT INTO session_external_names (agent, agent_session_id, name, updated_at)
		SELECT b.agent, b.agent_session_id,
			COALESCE(NULLIF(TRIM(a.name), ''), s.name), s.updated_at
		FROM session_agent_bindings b
		JOIN sessions s ON s.key = b.session_key
		LEFT JOIN session_name_aliases a ON a.key = s.key
		WHERE TRIM(b.agent) != ''
			AND TRIM(b.agent_session_id) != ''
			AND TRIM(COALESCE(NULLIF(TRIM(a.name), ''), s.name)) != ''
			AND TRIM(COALESCE(NULLIF(TRIM(a.name), ''), s.name)) != 'New Session'
		ORDER BY s.updated_at, s.key
		ON CONFLICT(agent, agent_session_id) DO UPDATE SET
			name = excluded.name,
			updated_at = excluded.updated_at
		WHERE excluded.updated_at >= session_external_names.updated_at`)
	return err
}

func normalizeForkParentLinks(db *sql.DB) (int, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()
	rows, err := tx.Query(`
		SELECT key, source
		FROM sessions
		WHERE parent_session_key != '' OR parent_tool_call_id != ''`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	keys := make([]string, 0)
	for rows.Next() {
		var key, source string
		if err := rows.Scan(&key, &source); err != nil {
			return 0, err
		}
		var metadata struct {
			Type string `json:"type"`
		}
		if json.Unmarshal([]byte(source), &metadata) == nil && metadata.Type == "fork" {
			keys = append(keys, key)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	for _, key := range keys {
		if _, err := tx.Exec(`
			UPDATE sessions
			SET parent_session_key = '', parent_tool_call_id = ''
			WHERE key = ?`, key); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	tx = nil
	return len(keys), nil
}

func readSessionDBLink(linkFile string) (string, bool, error) {
	payload, err := os.ReadFile(linkFile)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	dbFile := strings.TrimSpace(string(payload))
	if dbFile == "" {
		return "", false, fmt.Errorf("empty session db link: %s", linkFile)
	}
	if !filepath.IsAbs(dbFile) {
		return "", false, fmt.Errorf("session db link must be absolute: %s", linkFile)
	}
	return dbFile, true, nil
}

func writeSessionDBLink(linkFile, dbFile string) error {
	if strings.TrimSpace(dbFile) == "" {
		return errors.New("session db link target required")
	}
	if !filepath.IsAbs(dbFile) {
		return errors.New("session db link target must be absolute")
	}
	if err := os.MkdirAll(filepath.Dir(linkFile), 0o755); err != nil {
		return err
	}
	return os.WriteFile(linkFile, []byte(dbFile+"\n"), 0o644)
}

func userDataSessionDBFile(rootID string) (string, error) {
	configDir, err := mindFSConfigDir()
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(rootID)
	if id == "" {
		id = "default"
	}
	return filepath.Join(configDir, url.PathEscape(id), "session-list.db"), nil
}
