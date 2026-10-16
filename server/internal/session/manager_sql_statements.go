package session

import (
	_ "modernc.org/sqlite"
)

const (
	sessionDBPath    = "sessions/session-list.db"
	sessionDBLinkExt = ".link"
	exchangeFileTpl  = "sessions/%s.jsonl"
	auxFileTpl       = "sessions/%s.aux.jsonl"
	// maxExchangeLineBytes 单条 JSONL 上限（tool call 大 content），bufio.Scanner 兜底。
	maxExchangeLineBytes = 64 << 20
	// CUSTOM(G-X): 不再 SELECT/写入 pinned_at —— 置顶的唯一权威是主节点的 pins 表
	// （server/internal/pins）。两处一起断：只断一半会留下「写进去但读不回」的假退役。
	selectSessionSQL = `
	SELECT key, type, parent_session_key, parent_tool_call_id, source, task_id, model, shell, plan_mode, name, related_files_json, related_worktree_json, aux_ask_user_waiting, aux_has_plan, aux_has_todos, aux_has_task, aux_session_error, last_context_window_total_tokens, last_context_window_model_context_window, archived_at, created_at, updated_at, closed_at
	FROM sessions`
	deleteSessionSQL = `
DELETE FROM sessions
WHERE key = ?`
	deleteBindingsBySessionSQL = `
DELETE FROM session_agent_bindings
WHERE session_key = ?`
	upsertSessionMetaSQL = `
INSERT INTO sessions (
		key, type, parent_session_key, parent_tool_call_id, source, task_id, model, shell, plan_mode, name, related_files_json, related_worktree_json, aux_ask_user_waiting, aux_has_plan, aux_has_todos, aux_has_task, aux_session_error, last_context_window_total_tokens, last_context_window_model_context_window, archived_at, created_at, updated_at, closed_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(key) DO UPDATE SET
	type = excluded.type,
	parent_session_key = excluded.parent_session_key,
	parent_tool_call_id = excluded.parent_tool_call_id,
	source = excluded.source,
	task_id = excluded.task_id,
	model = excluded.model,
	shell = excluded.shell,
	plan_mode = excluded.plan_mode,
	name = excluded.name,
	related_files_json = excluded.related_files_json,
	related_worktree_json = excluded.related_worktree_json,
	aux_ask_user_waiting = excluded.aux_ask_user_waiting,
	aux_has_plan = excluded.aux_has_plan,
	aux_has_todos = excluded.aux_has_todos,
	aux_has_task = excluded.aux_has_task,
	aux_session_error = excluded.aux_session_error,
	last_context_window_total_tokens = excluded.last_context_window_total_tokens,
	last_context_window_model_context_window = excluded.last_context_window_model_context_window,
	archived_at = excluded.archived_at,
	created_at = excluded.created_at,
	updated_at = excluded.updated_at,
	closed_at = excluded.closed_at`
	sessionTableSchema = `
CREATE TABLE IF NOT EXISTS sessions (
	key TEXT PRIMARY KEY,
	type TEXT NOT NULL,
	parent_session_key TEXT NOT NULL DEFAULT '',
	parent_tool_call_id TEXT NOT NULL DEFAULT '',
	source TEXT NOT NULL DEFAULT '',
	task_id TEXT NOT NULL DEFAULT '',
	model TEXT NOT NULL DEFAULT '',
	shell TEXT NOT NULL DEFAULT '',
	plan_mode INTEGER NOT NULL DEFAULT 0,
	name TEXT NOT NULL,
	related_files_json TEXT NOT NULL,
	related_worktree_json TEXT NOT NULL DEFAULT '',
	aux_ask_user_waiting INTEGER NOT NULL DEFAULT 0,
	aux_has_plan INTEGER NOT NULL DEFAULT 0,
	aux_has_todos INTEGER NOT NULL DEFAULT 0,
	aux_has_task INTEGER NOT NULL DEFAULT 0,
	aux_session_error TEXT NOT NULL DEFAULT '',
	last_context_window_total_tokens INTEGER NOT NULL DEFAULT 0,
	last_context_window_model_context_window INTEGER NOT NULL DEFAULT 0,
	pinned_at TEXT,
	archived_at TEXT,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	closed_at TEXT
);`
	sessionNameAliasTableSchema = `
CREATE TABLE IF NOT EXISTS session_name_aliases (
	key TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	agent TEXT NOT NULL DEFAULT '',
	agent_session_id TEXT NOT NULL DEFAULT '',
	updated_at TEXT NOT NULL
);`
	sessionExternalNameTableSchema = `
CREATE TABLE IF NOT EXISTS session_external_names (
	agent TEXT NOT NULL,
	agent_session_id TEXT NOT NULL,
	name TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	PRIMARY KEY (agent, agent_session_id)
);`
	agentBindingTableSchema = `
CREATE TABLE IF NOT EXISTS session_agent_bindings (
	session_key TEXT NOT NULL,
	agent TEXT NOT NULL,
	agent_session_id TEXT NOT NULL,
	agent_ctx_seq INTEGER NOT NULL DEFAULT 0,
	external_source_path TEXT NOT NULL DEFAULT '',
	external_source_offset INTEGER NOT NULL DEFAULT 0,
	external_source_mtime_ns INTEGER NOT NULL DEFAULT 0,
	external_source_committed_offset INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (session_key, agent)
);`
	upsertAgentBindingSQL = `
INSERT INTO session_agent_bindings (
	session_key, agent, agent_session_id, agent_ctx_seq
) VALUES (?, ?, ?, ?)
ON CONFLICT(session_key, agent) DO UPDATE SET
	agent_session_id = excluded.agent_session_id,
	agent_ctx_seq = excluded.agent_ctx_seq`
	selectAllAgentBindingsSQL = `
SELECT session_key, agent, agent_session_id, agent_ctx_seq, external_source_path, external_source_offset, external_source_mtime_ns, external_source_committed_offset
FROM session_agent_bindings`
	selectAgentBindingSQL = `
SELECT session_key, agent, agent_session_id, agent_ctx_seq, external_source_path, external_source_offset, external_source_mtime_ns, external_source_committed_offset
FROM session_agent_bindings
WHERE session_key = ? AND agent = ?`
	selectAgentBindingsBySessionSQL = `
SELECT session_key, agent, agent_session_id, agent_ctx_seq, external_source_path, external_source_offset, external_source_mtime_ns, external_source_committed_offset
FROM session_agent_bindings
WHERE session_key = ?`
	selectBindingByAgentSessionSQL = `
SELECT session_key, agent, agent_session_id, agent_ctx_seq, external_source_path, external_source_offset, external_source_mtime_ns, external_source_committed_offset
FROM session_agent_bindings
WHERE agent = ? AND agent_session_id = ?
LIMIT 1`
	selectSessionNameAliasSQL = `
SELECT name FROM session_name_aliases WHERE key = ?`
	selectSessionNameAliasByAgentSQL = `
SELECT name FROM session_name_aliases WHERE agent = ? AND agent_session_id = ? LIMIT 1`
	upsertSessionNameAliasSQL = `
INSERT INTO session_name_aliases (key, name, agent, agent_session_id, updated_at) VALUES (?, ?, ?, ?, ?)
ON CONFLICT(key) DO UPDATE SET name = excluded.name, agent = excluded.agent, agent_session_id = excluded.agent_session_id, updated_at = excluded.updated_at`
	upsertExternalSessionNameSQL = `
INSERT INTO session_external_names (agent, agent_session_id, name, updated_at) VALUES (?, ?, ?, ?)
ON CONFLICT(agent, agent_session_id) DO UPDATE SET
	name = excluded.name,
	updated_at = excluded.updated_at`
	selectExternalSessionNameSQL = `
SELECT name FROM session_external_names WHERE agent = ? AND agent_session_id = ?`
)
