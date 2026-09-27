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

// 任务改名同步到会话时后缀必须还在：以前 bindTaskSessionNames 拿的是调用方传进来的
// 裸任务名，改名一次就把建会话时拼上的 " / #编号" 抹掉了。
func TestBindTaskSessionNamesKeepsNumberSuffix(t *testing.T) {
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
	app.Kanban = kanban.NewService(kanban.NewTemplateStoreAt(parent), app)
	ctx := context.Background()

	// 建会话：拿到带后缀的名字。
	key, err := app.EnsureAgentSession(ctx, kanbanExecForSessionNaming(root.ID, "旧名", 8, "bugfix", "提示"))
	if err != nil {
		t.Fatalf("EnsureAgentSession returned error: %v", err)
	}
	if got := sessionNameForTest(t, app, root.ID, key); got != "旧名 / #8" {
		t.Fatalf("created session name = %q, want %q", got, "旧名 / #8")
	}

	// 造一个绑定了该会话的任务（task_number=8），再走任务改名同步。
	svc, err := app.GetKanbanService()
	if err != nil {
		t.Fatalf("GetKanbanService: %v", err)
	}
	created, err := svc.CreateTask(ctx, kanban.CreateTaskInput{
		RootID: root.ID,
		Name:   "旧名",
		Input:  "提示",
		Stages: []kanban.StageTemplate{{Name: "任务输入", Role: kanban.RoleUser, PromptTemplate: "提示"}},
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if created.Task.TaskNumber <= 0 {
		t.Fatalf("task_number = %d, want a positive number to build the suffix from", created.Task.TaskNumber)
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
