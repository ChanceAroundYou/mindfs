package kanban

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestRenameTask(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, nil)
	detail, err := svc.CreateTask(ctx, CreateTaskInput{RootID: root.ID, Stages: []StageTemplate{userStage("Describe")}, Name: "旧名", Input: "x"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	renamed, err := svc.RenameTask(ctx, root.ID, detail.Task.ID, "  修登录按钮  ")
	if err != nil {
		t.Fatalf("RenameTask: %v", err)
	}
	if renamed.Task.Name != "修登录按钮" {
		t.Fatalf("task name=%q, want trimmed new name", renamed.Task.Name)
	}
	got, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Task.Name != "修登录按钮" {
		t.Fatalf("rename not persisted: %q", got.Task.Name)
	}
	// 带本任务编号后缀的名字进来要剥掉：任务名不背后缀，后缀只归会话名。
	suffixed, err := svc.RenameTask(ctx, root.ID, detail.Task.ID, "修登录按钮 / #1")
	if err != nil {
		t.Fatalf("RenameTask(suffixed): %v", err)
	}
	if suffixed.Task.Name != "修登录按钮" {
		t.Fatalf("task name=%q, want suffix stripped", suffixed.Task.Name)
	}
}

// 会话名 ↔ 任务名双向绑定（kanban 侧）：main_session_key 反查任务后改名；
// 名字相同时不动，避免会话改名触发的回流造成事件抖动。
func TestTaskNameFromSession(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, nil)
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{userStage("Describe")},
		Name:   "初名",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	task, err := store.GetTask(ctx, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	task.MainSessionKey = "sess-1"
	if err := store.UpdateTask(ctx, task); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	if _, changed := svc.TaskNameFromSession(ctx, root.ID, "sess-1", "初名"); changed {
		t.Fatal("same name should be no-op")
	}
	got, changed := svc.TaskNameFromSession(ctx, root.ID, "sess-1", "新名")
	if !changed {
		t.Fatal("expected task renamed")
	}
	if got.Task.Name != "新名" {
		t.Fatalf("task name=%q", got.Task.Name)
	}
	if _, changed := svc.TaskNameFromSession(ctx, root.ID, "sess-other", "再新"); changed {
		t.Fatal("unbound session must not touch any task")
	}
}

// 会话名带着 " / #编号" 回流时，剥成 base 再比：原样重命名一个带后缀的会话
// 必须判成 no-op，否则每次都会白跑一遍 RenameTask + 广播。
func TestTaskNameFromSessionStripsNumberSuffix(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, nil)
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{userStage("Describe")},
		Name:   "初名",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	task, err := store.GetTask(ctx, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	task.MainSessionKey = "sess-1"
	if err := store.UpdateTask(ctx, task); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	number := task.TaskNumber

	// 会话现名「初名 / #N」，任务现名「初名」：剥完相等 → 不动。
	if _, changed := svc.TaskNameFromSession(ctx, root.ID, "sess-1", TaskSessionName("初名", number)); changed {
		t.Fatalf("renaming to the same suffixed name must be a no-op (task_number=%d)", number)
	}

	got, changed := svc.TaskNameFromSession(ctx, root.ID, "sess-1", TaskSessionName("新名", number))
	if !changed {
		t.Fatal("expected task renamed")
	}
	if got.Task.Name != "新名" {
		t.Fatalf("task name=%q, want %q (suffix must not land on the task)", got.Task.Name, "新名")
	}
}

// 删会话时清空任务链接：会话没了，任务不能再指向它，且要在 aux_session_error 留痕。
func TestDetachFromSessionClearsTaskLink(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, nil)
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{userStage("Describe")},
		Name:   "任务",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	task, err := store.GetTask(ctx, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	task.MainSessionKey = "sess-dead"
	if err := store.UpdateTask(ctx, task); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	// 让阶段运行也指向这个会话：任务面板的会话列表同时收 stage_run.session_key
	runs, err := store.ListStageRuns(ctx, task.ID)
	if err != nil || len(runs) == 0 {
		t.Fatalf("ListStageRuns: %v runs=%d", err, len(runs))
	}
	runs[0].SessionKey = "sess-dead"
	if err := store.UpdateStageRunExecution(ctx, runs[0]); err != nil {
		t.Fatalf("UpdateStageRunExecution: %v", err)
	}

	got, changed := svc.DetachFromSession(ctx, root.ID, []string{"sess-dead"})
	if !changed {
		t.Fatal("expected task updated")
	}
	if got.Task.MainSessionKey != "" {
		t.Fatalf("main_session_key should be cleared, got %q", got.Task.MainSessionKey)
	}
	if strings.TrimSpace(got.Task.AuxFlags.SessionError) == "" {
		t.Fatal("expected session_error trace left on the task")
	}
	// 留痕要能被前端解析：形状是 {"message":..., "data":[...]}
	var notice struct {
		Message string   `json:"message"`
		Data    []string `json:"data"`
	}
	if err := json.Unmarshal([]byte(got.Task.AuxFlags.SessionError), &notice); err != nil {
		t.Fatalf("session_error is not the expected JSON shape: %v (%q)", err, got.Task.AuxFlags.SessionError)
	}
	if strings.TrimSpace(notice.Message) == "" {
		t.Fatalf("session_error.message empty: %q", got.Task.AuxFlags.SessionError)
	}
	runs, err = store.ListStageRuns(ctx, task.ID)
	if err != nil {
		t.Fatalf("ListStageRuns: %v", err)
	}
	if runs[0].SessionKey != "" {
		t.Fatalf("stage run session_key should be cleared, got %q", runs[0].SessionKey)
	}
}

func TestDetachFromSessionIgnoresUnboundAndForeignSessions(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, nil)
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{userStage("Describe")},
		Name:   "任务",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	task, err := store.GetTask(ctx, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	task.MainSessionKey = "sess-alive"
	if err := store.UpdateTask(ctx, task); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	if _, changed := svc.DetachFromSession(ctx, root.ID, []string{"sess-unbound", ""}); changed {
		t.Fatal("unbound session must not touch any task")
	}
	after, err := store.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if after.MainSessionKey != "sess-alive" {
		t.Fatalf("main_session_key must survive unrelated detach, got %q", after.MainSessionKey)
	}
}

// ClearSessionRefs 只清确实指向该会话的引用：主会话被改成别的 key 后不该被误清。
func TestClearSessionRefsKeepsRepointedTask(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, nil)
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{userStage("Describe")},
		Name:   "任务",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	task, err := store.GetTask(ctx, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	task.MainSessionKey = "sess-new"
	if err := store.UpdateTask(ctx, task); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	if err := store.ClearSessionRefs(ctx, task.ID, "sess-old"); err != nil {
		t.Fatalf("ClearSessionRefs: %v", err)
	}
	after, err := store.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if after.MainSessionKey != "sess-new" {
		t.Fatalf("repointed task must keep its key, got %q", after.MainSessionKey)
	}
}

// 会话名的 " / #编号" 只有一处派生（TaskSessionName），建会话和两条改名路径都走它，
// 改名才不会把后缀丢掉。表驱动钉住建会话那侧一直以来的行为。
func TestTaskSessionName(t *testing.T) {
	cases := []struct {
		name       string
		base       string
		taskNumber int
		want       string
	}{
		{name: "名 + 编号", base: "登录页闪退", taskNumber: 8, want: "登录页闪退 / #8"},
		{name: "没名字只留编号", base: "", taskNumber: 7, want: "#7"},
		{name: "legacy 无编号", base: "名", taskNumber: 0, want: "名"},
		{name: "已带本编号后缀不叠加", base: "名 / #8", taskNumber: 8, want: "名 / #8"},
		{name: "尾随空白先去掉", base: "  名  ", taskNumber: 8, want: "名 / #8"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := TaskSessionName(tc.base, tc.taskNumber); got != tc.want {
				t.Fatalf("TaskSessionName(%q, %d) = %q, want %q", tc.base, tc.taskNumber, got, tc.want)
			}
		})
	}
}

// 剥离只认「自己的编号」：任务真名叫 foo / #3 而编号是 8 时不能被误伤。
func TestTrimTaskSessionNameSuffix(t *testing.T) {
	cases := []struct {
		name       string
		name_      string
		taskNumber int
		want       string
	}{
		{name: "剥掉本编号后缀", name_: "名 / #8", taskNumber: 8, want: "名"},
		{name: "编号不匹配保持原样", name_: "foo / #3", taskNumber: 8, want: "foo / #3"},
		{name: "不是数字保持原样", name_: "名 / #abc", taskNumber: 8, want: "名 / #abc"},
		{name: "无后缀原样返回", name_: "名", taskNumber: 8, want: "名"},
		{name: "legacy 无编号不动", name_: "名 / #8", taskNumber: 0, want: "名 / #8"},
		{name: "只剥最后一段", name_: "a / #1 / #8", taskNumber: 8, want: "a / #1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := TrimTaskSessionNameSuffix(tc.name_, tc.taskNumber); got != tc.want {
				t.Fatalf("TrimTaskSessionNameSuffix(%q, %d) = %q, want %q", tc.name_, tc.taskNumber, got, tc.want)
			}
		})
	}
}
