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

// kanbanExecForSessionNaming 造一个首段执行上下文：模板名 + 任务名 + 任务号 + 提示。
func kanbanExecForSessionNaming(rootID, taskName string, taskNumber int, templateName, prompt string) kanban.AgentStageExecution {
	return kanban.AgentStageExecution{
		RootID: rootID,
		Task: kanban.Task{
			ID:               "task_" + rootID,
			TaskNumber:       taskNumber,
			Name:             taskName,
			TaskTemplateName: templateName,
		},
		Stage:  kanban.StageTemplate{Agent: "codex", SessionReusePolicy: kanban.SessionReuseAlwaysNew},
		Prompt: prompt,
	}
}

// 任务首段跑起来时建出的会话必须顶着任务名：任务名在会话存在之前就定了，
// 改名同步（handleKanbanTaskRename 里的 bindTaskSessionNames）当时没会话可改，
// 只能靠建会话这步兜底，否则会话会一直挂着模板名。
func TestEnsureAgentSessionNamesSessionAfterTaskName(t *testing.T) {
	parent := tempDirForSessionNaming(t)
	registry := fs.NewRegistry(filepath.Join(parent, "registry.json"))
	projectPath := filepath.Join(parent, "project")
	if err := mkdirAllForSessionNaming(projectPath); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	root, err := registry.Upsert(projectPath)
	if err != nil {
		t.Fatalf("Upsert returned error: %v", err)
	}
	app := &AppContext{Dirs: registry}
	ctx := context.Background()

	// 没有任务名：沿用模板名 / #编号 / prompt 回退的既有行为。
	fallback, err := app.EnsureAgentSession(ctx, kanbanExecForSessionNaming(root.ID, "", 7, "bugfix", "模板里的提示"))
	if err != nil {
		t.Fatalf("EnsureAgentSession(fallback) returned error: %v", err)
	}
	if got := sessionNameForTest(t, app, root.ID, fallback); got != "bugfix / #7" {
		t.Fatalf("fallback session name = %q, want %q", got, "bugfix / #7")
	}

	// 有任务名：任务名优先，#编号仍然跟在后面。
	named, err := app.EnsureAgentSession(ctx, kanbanExecForSessionNaming(root.ID, "登录页闪退", 8, "bugfix", "模板里的提示"))
	if err != nil {
		t.Fatalf("EnsureAgentSession(named) returned error: %v", err)
	}
	if got := sessionNameForTest(t, app, root.ID, named); got != "登录页闪退 / #8" {
		t.Fatalf("named session name = %q, want %q", got, "登录页闪退 / #8")
	}
}

// 任务没给名字时不能因为有 TaskID 就凭空造名字（TaskID 只做绑定，不参与命名）。
func TestEnsureAgentSessionIgnoresTaskIDWhenNameMissing(t *testing.T) {
	parent := tempDirForSessionNaming(t)
	registry := fs.NewRegistry(filepath.Join(parent, "registry.json"))
	projectPath := filepath.Join(parent, "project")
	if err := mkdirAllForSessionNaming(projectPath); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	root, err := registry.Upsert(projectPath)
	if err != nil {
		t.Fatalf("Upsert returned error: %v", err)
	}
	app := &AppContext{Dirs: registry}

	key, err := app.EnsureAgentSession(context.Background(), kanbanExecForSessionNaming(root.ID, "", 0, "", "把登录页的闪退复现出来"))
	if err != nil {
		t.Fatalf("EnsureAgentSession returned error: %v", err)
	}
	got := sessionNameForTest(t, app, root.ID, key)
	if got == "" {
		t.Fatal("session name is empty, want prompt fallback")
	}
	if got == "New Session" {
		t.Fatal("session name fell through to the default instead of the prompt fallback")
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
