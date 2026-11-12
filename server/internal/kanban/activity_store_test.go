package kanban

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"mindfs/server/internal/fs"
)

// Task 4 (Phase 2): Activity 表和 Store.
//
// The plan describes `migrations/006_task_activities.sql`, but this repository
// runs schema changes as idempotent Go migrations (task_store_migrate.go) and
// already owns a `task_activities` table from the workflow prototype
// (migrateComments). The 006 equivalent is `migrateTaskActivities`, which
// extends the existing table in place. The workflow run link is the existing
// `run_id` column / TaskActivity.RunID; the plan's `workflow_run_id` names the
// same relation, so no second column is introduced (two columns would be two
// truths).
//
// These tests pin the observable contract the plan consumes: the
// conversation-model columns exist and ActivityStore provides
// Insert/Get/List/UpdateStatus/UpdateBody, including serialized concurrent
// inserts (Review Focus #2).

// legacyTaskActivitiesDDL mirrors the table as created by migrateComments
// before the conversation model added its columns.
const legacyTaskActivitiesDDL = `
CREATE TABLE task_activities (
	id TEXT NOT NULL,
	task_id TEXT NOT NULL,
	type TEXT NOT NULL DEFAULT 'user_comment',
	author TEXT NOT NULL DEFAULT '',
	role TEXT NOT NULL DEFAULT '',
	body TEXT NOT NULL DEFAULT '',
	workflow_choice TEXT NOT NULL DEFAULT 'empty',
	workflow_version_id TEXT NOT NULL DEFAULT '',
	run_id TEXT NOT NULL DEFAULT '',
	node_run_id TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	PRIMARY KEY (task_id, id)
);`

func TestMigration006TaskActivities(t *testing.T) {
	db, err := sql.Open("sqlite", t.TempDir()+"/legacy.db")
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(legacyTaskActivitiesDDL); err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO task_activities (id, task_id, type, author, role, body, created_at) VALUES ('legacy-1', 'task-1', 'user_comment', 'alice', 'user', '旧留言', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}

	if err := migrateTaskActivities(db); err != nil {
		t.Fatalf("migrateTaskActivities: %v", err)
	}
	// Idempotent: running the migration again must not fail or drop data.
	if err := migrateTaskActivities(db); err != nil {
		t.Fatalf("migrateTaskActivities second run: %v", err)
	}

	columns := tableColumns(t, db, "task_activities")
	required := []string{
		"id", "task_id", "root_id", "type", "role", "body",
		"author", "status", "summary", "run_id",
		"workflow_version_id", "workflow_choice", "session_key",
		"created_at", "updated_at",
	}
	for _, col := range required {
		if !columns[col] {
			t.Errorf("missing required column %q", col)
		}
	}

	indexes := tableIndexes(t, db, "task_activities")
	for _, idx := range []string{
		"idx_task_activities_task_created",
		"idx_task_activities_workflow_run",
		"idx_task_activities_id",
	} {
		if !indexes[idx] {
			t.Errorf("missing required index %q", idx)
		}
	}

	// The legacy row survives and the new columns read as empty, not NULL.
	var body, rootID, status, updatedAt string
	if err := db.QueryRow(`SELECT body, root_id, status, updated_at FROM task_activities WHERE task_id='task-1' AND id='legacy-1'`).
		Scan(&body, &rootID, &status, &updatedAt); err != nil {
		t.Fatalf("read legacy row: %v", err)
	}
	if body != "旧留言" || rootID != "" || status != "" || updatedAt != "" {
		t.Fatalf("legacy row = body %q root %q status %q updated_at %q, want preserved body and empty new columns", body, rootID, status, updatedAt)
	}
}

func TestActivityStore_InsertAndGet(t *testing.T) {
	store := setupActivityStore(t)
	ctx := context.Background()
	created := time.Date(2026, 11, 12, 10, 0, 0, 123456789, time.UTC)
	want := &TaskActivity{
		ID:             "act-1",
		TaskID:         "task-1",
		RootID:         "root-1",
		Type:           ActivityTypeUserMessage,
		Role:           RoleUser,
		Body:           "请修复 bug",
		Author:         "alice",
		WorkflowChoice: CommentWorkflowChoiceEmpty,
		SessionKey:     "session-1",
		CreatedAt:      created,
	}
	if err := store.Insert(ctx, want); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if want.UpdatedAt.IsZero() {
		t.Fatal("Insert must set UpdatedAt when it is zero")
	}

	got, err := store.Get(ctx, "act-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.TaskID != "task-1" || got.RootID != "root-1" || got.Type != ActivityTypeUserMessage {
		t.Fatalf("identity fields = %+v", got)
	}
	if got.Body != "请修复 bug" || got.Author != "alice" || got.Role != RoleUser {
		t.Fatalf("content fields = %+v", got)
	}
	if got.WorkflowChoice != CommentWorkflowChoiceEmpty || got.SessionKey != "session-1" {
		t.Fatalf("workflow fields = %+v", got)
	}
	if !got.CreatedAt.Equal(created) {
		t.Fatalf("CreatedAt = %v, want %v", got.CreatedAt, created)
	}
}

func TestActivityStore_ListOrdersByCreatedAt(t *testing.T) {
	store := setupActivityStore(t)
	ctx := context.Background()
	base := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)

	// Insert out of order: List must return creation order, not insert order.
	if err := store.Insert(ctx, &TaskActivity{ID: "act-2", TaskID: "task-1", Type: ActivityTypeUserMessage, Role: RoleUser, Body: "second", CreatedAt: base}); err != nil {
		t.Fatalf("Insert act-2: %v", err)
	}
	if err := store.Insert(ctx, &TaskActivity{ID: "act-1", TaskID: "task-1", Type: ActivityTypeUserMessage, Role: RoleUser, Body: "first", CreatedAt: base.Add(-time.Hour)}); err != nil {
		t.Fatalf("Insert act-1: %v", err)
	}
	if err := store.Insert(ctx, &TaskActivity{ID: "act-x", TaskID: "task-2", Type: ActivityTypeUserMessage, Role: RoleUser, Body: "other task"}); err != nil {
		t.Fatalf("Insert act-x: %v", err)
	}

	list, err := store.List(ctx, "task-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("List len = %d, want 2", len(list))
	}
	if list[0].ID != "act-1" || list[1].ID != "act-2" {
		t.Fatalf("List order = [%s, %s], want [act-1, act-2]", list[0].ID, list[1].ID)
	}

	empty, err := store.List(ctx, "no-such-task")
	if err != nil {
		t.Fatalf("List empty: %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("List empty = %#v, want non-nil empty slice", empty)
	}
}

func TestActivityStore_UpdateStatus(t *testing.T) {
	store := setupActivityStore(t)
	ctx := context.Background()
	created := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Insert(ctx, &TaskActivity{
		ID: "act-1", TaskID: "task-1", Type: ActivityTypeAgentReply, Role: RoleAgent,
		Body: "处理中", Status: ActivityStatusPending, Summary: "keep me", CreatedAt: created,
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	summary := "已完成"
	if err := store.UpdateStatus(ctx, "act-1", ActivityStatusCompleted, &summary); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	got, err := store.Get(ctx, "act-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != ActivityStatusCompleted {
		t.Fatalf("Status = %q, want %q", got.Status, ActivityStatusCompleted)
	}
	if got.Summary != "已完成" {
		t.Fatalf("Summary = %q, want 已完成", got.Summary)
	}
	if !got.UpdatedAt.After(created) {
		t.Fatalf("UpdatedAt = %v, want after %v", got.UpdatedAt, created)
	}

	// nil summary leaves the existing summary untouched.
	if err := store.UpdateStatus(ctx, "act-1", ActivityStatusRunning, nil); err != nil {
		t.Fatalf("UpdateStatus nil summary: %v", err)
	}
	got, _ = store.Get(ctx, "act-1")
	if got.Status != ActivityStatusRunning || got.Summary != "已完成" {
		t.Fatalf("after nil summary: status %q summary %q", got.Status, got.Summary)
	}

	if err := store.UpdateStatus(ctx, "act-1", "bogus", nil); !errors.Is(err, ErrInvalidActivityStatus) {
		t.Fatalf("UpdateStatus bogus = %v, want ErrInvalidActivityStatus", err)
	}
	if err := store.UpdateStatus(ctx, "missing", ActivityStatusRunning, nil); !errors.Is(err, ErrActivityNotFound) {
		t.Fatalf("UpdateStatus missing = %v, want ErrActivityNotFound", err)
	}
}

func TestActivityStore_UpdateBody(t *testing.T) {
	store := setupActivityStore(t)
	ctx := context.Background()
	created := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Insert(ctx, &TaskActivity{ID: "act-1", TaskID: "task-1", Type: ActivityTypeUserMessage, Role: RoleUser, Body: "old", CreatedAt: created}); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := store.UpdateBody(ctx, "act-1", "new body"); err != nil {
		t.Fatalf("UpdateBody: %v", err)
	}
	got, _ := store.Get(ctx, "act-1")
	if got.Body != "new body" {
		t.Fatalf("Body = %q, want new body", got.Body)
	}
	if !got.UpdatedAt.After(created) {
		t.Fatalf("UpdatedAt = %v, want after %v", got.UpdatedAt, created)
	}
	if err := store.UpdateBody(ctx, "act-1", "  "); err == nil {
		t.Fatal("UpdateBody with blank body should fail")
	}
	if err := store.UpdateBody(ctx, "missing", "x"); !errors.Is(err, ErrActivityNotFound) {
		t.Fatalf("UpdateBody missing = %v, want ErrActivityNotFound", err)
	}
}

func TestActivityStore_GetNotFound(t *testing.T) {
	store := setupActivityStore(t)
	if _, err := store.Get(context.Background(), "missing"); !errors.Is(err, ErrActivityNotFound) {
		t.Fatalf("Get missing = %v, want ErrActivityNotFound", err)
	}
}

func TestActivityStore_InsertRequiresIdentity(t *testing.T) {
	store := setupActivityStore(t)
	ctx := context.Background()
	cases := []*TaskActivity{
		nil,
		{TaskID: "task-1", Type: ActivityTypeUserMessage},
		{ID: "act-1", Type: ActivityTypeUserMessage},
		{ID: "act-1", TaskID: "task-1"},
	}
	for i, in := range cases {
		if err := store.Insert(ctx, in); err == nil {
			t.Fatalf("case %d: Insert should reject missing identity fields", i)
		}
	}
}

// TestActivityStore_ConcurrentInsert is Review Focus #2: two comments submitted
// for the same task at the same time must both be stored, never lost. The store
// shares the TaskStore's single-connection pool, so writes serialize.
func TestActivityStore_ConcurrentInsert(t *testing.T) {
	store := setupActivityStore(t)
	ctx := context.Background()
	const n = 10
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- store.Insert(ctx, &TaskActivity{
				ID:     fmt.Sprintf("act-%d", i),
				TaskID: "task-1",
				RootID: "root-1",
				Type:   ActivityTypeUserMessage,
				Role:   RoleUser,
				Body:   fmt.Sprintf("message %d", i),
			})
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Insert: %v", err)
		}
	}

	list, err := store.List(ctx, "task-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != n {
		t.Fatalf("List len = %d, want %d (concurrent inserts lost)", len(list), n)
	}
	seen := make(map[string]bool, n)
	for _, a := range list {
		seen[a.ID] = true
	}
	for i := 0; i < n; i++ {
		if !seen[fmt.Sprintf("act-%d", i)] {
			t.Fatalf("missing concurrent activity act-%d", i)
		}
	}
}

func setupActivityStore(t *testing.T) *ActivityStore {
	t.Helper()
	store, err := NewTaskStore(fs.NewRootInfo("root", "root", t.TempDir()))
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return NewActivityStore(store.db)
}

func tableColumns(t *testing.T, db *sql.DB, table string) map[string]bool {
	t.Helper()
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		t.Fatalf("PRAGMA table_info(%s): %v", table, err)
	}
	defer rows.Close()
	columns := map[string]bool{}
	for rows.Next() {
		var (
			cid       int
			name      string
			ctype     string
			notNull   int
			dfltValue any
			pk        int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dfltValue, &pk); err != nil {
			t.Fatalf("scan table_info: %v", err)
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("table_info rows: %v", err)
	}
	return columns
}

func tableIndexes(t *testing.T, db *sql.DB, table string) map[string]bool {
	t.Helper()
	rows, err := db.Query(`PRAGMA index_list(` + table + `)`)
	if err != nil {
		t.Fatalf("PRAGMA index_list(%s): %v", table, err)
	}
	defer rows.Close()
	indexes := map[string]bool{}
	for rows.Next() {
		var (
			seq     int
			name    string
			unique  int
			origin  string
			partial int
		)
		if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			t.Fatalf("scan index_list: %v", err)
		}
		indexes[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("index_list rows: %v", err)
	}
	return indexes
}
