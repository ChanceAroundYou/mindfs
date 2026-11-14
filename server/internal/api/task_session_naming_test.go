package api

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"mindfs/server/internal/fs"
	"mindfs/server/internal/kanban"
)

func tempDirForSessionNaming(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

func mkdirAllForSessionNaming(path string) error {
	return os.MkdirAll(path, 0o755)
}

func setupSessionNamingApp(t *testing.T) (*AppContext, *kanban.Service, fs.RootInfo) {
	t.Helper()
	parent := tempDirForSessionNaming(t)
	registry := fs.NewRegistry(filepath.Join(parent, "registry.json"))
	projectPath := filepath.Join(parent, "project")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	root, err := registry.Upsert(projectPath)
	if err != nil {
		t.Fatalf("Upsert returned error: %v", err)
	}
	app := &AppContext{Dirs: registry, Prober: testProberWithAgents("claude")}
	svc := kanban.NewService(app)
	app.Kanban = svc
	return app, svc, root
}

// 对话式任务建主会话时用任务名；没名字才退回「#编号」。
func TestEnsureDirectSessionNamesSessionAfterTaskName(t *testing.T) {
	app, svc, root := setupSessionNamingApp(t)
	ctx := context.Background()
	detail, err := svc.CreateTask(ctx, kanban.CreateTaskInput{RootID: root.ID, Name: "登录页闪退"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	key, _, err := app.ensureDirectSession(ctx, detail.Task)
	if err != nil {
		t.Fatalf("ensureDirectSession: %v", err)
	}
	if got := sessionNameForTest(t, app, root.ID, key); got != "登录页闪退" {
		t.Fatalf("session name = %q, want %q", got, "登录页闪退")
	}
}

func TestEnsureDirectSessionFallsBackToTaskNumber(t *testing.T) {
	app, svc, root := setupSessionNamingApp(t)
	ctx := context.Background()
	detail, err := svc.CreateTask(ctx, kanban.CreateTaskInput{RootID: root.ID})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	key, _, err := app.ensureDirectSession(ctx, detail.Task)
	if err != nil {
		t.Fatalf("ensureDirectSession: %v", err)
	}
	want := kanban.TaskSessionName("", detail.Task.TaskNumber)
	if got := sessionNameForTest(t, app, root.ID, key); got != want {
		t.Fatalf("session name = %q, want %q", got, want)
	}
}

// 任务改名同步到会话时后缀必须还在：bindTaskSessionNames 拿的是调用方传进来的
// 任务名，改名一次就把建会话时拼上的 " / #编号" 抹掉了。
func TestBindTaskSessionNamesKeepsNumberSuffix(t *testing.T) {
	app, svc, root := setupSessionNamingApp(t)
	ctx := context.Background()
	created, err := svc.CreateTask(ctx, kanban.CreateTaskInput{RootID: root.ID, Name: "旧名"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if created.Task.TaskNumber <= 0 {
		t.Fatalf("task_number = %d, want a positive number to build the suffix from", created.Task.TaskNumber)
	}
	key, _, err := app.ensureDirectSession(ctx, created.Task)
	if err != nil {
		t.Fatalf("ensureDirectSession: %v", err)
	}
	renamed, err := svc.RenameTask(ctx, root.ID, created.Task.ID, "新名")
	if err != nil {
		t.Fatalf("RenameTask: %v", err)
	}
	renamed.Task.MainSessionKey = key

	want := kanban.TaskSessionName("新名", created.Task.TaskNumber)
	h := &HTTPHandler{AppContext: app}
	h.bindTaskSessionNames(ctx, root.ID, renamed)

	if got := sessionNameForTest(t, app, root.ID, key); got != want {
		t.Fatalf("session name after task rename = %q, want %q", got, want)
	}
	// 再同步一次不能叠成 "新名 / #8 / #8"。
	h.bindTaskSessionNames(ctx, root.ID, renamed)
	if got := sessionNameForTest(t, app, root.ID, key); got != want {
		t.Fatalf("session name after second sync = %q, want %q (no accumulation)", got, want)
	}
}

func sessionNameForTest(t *testing.T, app *AppContext, rootID, key string) string {
	t.Helper()
	manager, err := app.GetSessionManager(rootID)
	if err != nil {
		t.Fatalf("GetSessionManager returned error: %v", err)
	}
	sess, err := manager.Get(context.Background(), key, 0)
	if err != nil {
		t.Fatalf("manager.Get returned error: %v", err)
	}
	return sess.Name
}
