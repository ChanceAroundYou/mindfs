package usecase

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	rootfs "mindfs/server/internal/fs"
	"mindfs/server/internal/session"
)

// 这条用例治的是「归属目录已删、但 repoint 会在任何副作用之前 400」的那批会话：
// 它们既搬不动又没有清归属的入口。下面几条把该行为的两侧都钉住 ——
// 该清的时候必须真的清掉（并且运行时 cwd 跟着回到主 checkout），
// 不该清的时候（目录还活着）必须拒绝。

func newOwnershipService(t *testing.T) (Service, *session.Manager, string) {
	t.Helper()
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := session.NewManager(root)
	rootPath, err := root.RootDir()
	if err != nil {
		t.Fatalf("root dir: %v", err)
	}
	return Service{Registry: repointTestRegistry{root: root, manager: manager}}, manager, rootPath
}

// newOwnershipServiceWithExistingWorktree 建一条源为 worktree 的会话，
// 归属指向一个**存在**的目录（模拟还活着的工作树）。
func newOwnershipServiceWithExistingWorktree(t *testing.T) (Service, *session.Manager, *session.Session, string) {
	t.Helper()
	service, manager, rootPath := newOwnershipService(t)
	ctx := context.Background()
	created, err := manager.Create(ctx, session.CreateInput{
		Type: session.TypeChat, Agent: "claude", Source: "worktree", TaskID: "task-99", Name: "R",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	worktreePath := filepath.Join(t.TempDir(), "task-99")
	if err := os.MkdirAll(worktreePath, 0o755); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}
	if _, err := manager.RecordRelatedWorktree(ctx, created.Key, "mindfs", worktreePath, "task-99", "head"); err != nil {
		t.Fatalf("record worktree: %v", err)
	}
	return service, manager, created, rootPath
}

// 目录还在 = 那是一个活着的工作树。默认必须拒绝，而且拒绝要在任何副作用之前：
// 归属原样保留、运行时 cwd 不变。
func TestClearSessionWorktreeOwnershipRefusesWhenDirectoryStillExists(t *testing.T) {
	service, manager, created, _ := newOwnershipServiceWithExistingWorktree(t)
	ctx := context.Background()

	_, err := service.ClearSessionWorktreeOwnership(ctx, ClearSessionWorktreeOwnershipInput{
		RootID: "mindfs", Key: created.Key,
	})
	if err == nil {
		t.Fatal("cleared the ownership of a live worktree without force")
	}
	if !strings.Contains(err.Error(), "仍然存在") {
		t.Fatalf("error = %v, want it to say the directory still exists", err)
	}
	current, err := manager.Get(ctx, created.Key, 0)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if current.RelatedWorktree == nil || current.RelatedWorktree.Path == "" {
		t.Fatalf("refusal still mutated the session: %+v", current.RelatedWorktree)
	}
}

// force 是那个人明确要求的出口：目录还在也照清（收尾流程里可能先删了别的，
// 或用户就是要解绑）。清了就必须真的落到库里。
func TestClearSessionWorktreeOwnershipForceClearsLiveWorktree(t *testing.T) {
	service, manager, created, rootPath := newOwnershipServiceWithExistingWorktree(t)
	ctx := context.Background()

	out, err := service.ClearSessionWorktreeOwnership(ctx, ClearSessionWorktreeOwnershipInput{
		RootID: "mindfs", Key: created.Key, Force: true,
	})
	if err != nil {
		t.Fatalf("force clear: %v", err)
	}
	if !out.Cleared || out.WorktreeGone {
		t.Fatalf("force clear reported %+v, want cleared=true gone=false", out)
	}
	current, err := manager.Get(ctx, created.Key, 0)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if current.RelatedWorktree != nil {
		t.Fatalf("ownership survived a force clear: %+v", current.RelatedWorktree)
	}
	if out.RuntimeDir != rootPath {
		t.Fatalf("runtime dir after clear = %q, want the main checkout %q", out.RuntimeDir, rootPath)
	}
}

// 核心场景：目录早没了。清完之后运行时 cwd 必须回到主 checkout —— 否则徽标没了、
// 实际还是在往一个不存在的目录里干活。
func TestClearSessionWorktreeOwnershipClearsMissingWorktree(t *testing.T) {
	service, manager, rootPath := newOwnershipService(t)
	ctx := context.Background()
	created, err := manager.Create(ctx, session.CreateInput{
		Type: session.TypeChat, Agent: "claude", Source: "worktree", TaskID: "task-36", Name: "stuck",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	gone := filepath.Join(t.TempDir(), "task-36")
	if _, err := manager.RecordRelatedWorktree(ctx, created.Key, "mindfs", gone, "task-36", "head"); err != nil {
		t.Fatalf("record worktree: %v", err)
	}
	// 前提核对：归属存在时运行时 cwd 就已经指向那个不存在的目录（这才是要治的病）。
	if runtime := sessionRuntimeRootPathFor(mustGet(t, manager, created.Key)); runtime != gone {
		t.Fatalf("runtime before clear = %q, want %q", runtime, gone)
	}

	out, err := service.ClearSessionWorktreeOwnership(ctx, ClearSessionWorktreeOwnershipInput{
		RootID: "mindfs", Key: created.Key,
	})
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if !out.Cleared || !out.WorktreeGone {
		t.Fatalf("clear reported %+v, want cleared=true gone=true", out)
	}
	if out.Path != gone || out.Branch != "task-36" {
		t.Fatalf("cleared the wrong ownership: %+v", out)
	}
	current := mustGet(t, manager, created.Key)
	if current.RelatedWorktree != nil {
		t.Fatalf("ownership survived the clear: %+v", current.RelatedWorktree)
	}
	if runtime := sessionRuntimeRootPathFor(current); runtime != "" {
		t.Fatalf("runtime after clear = %q, want empty (= main checkout)", runtime)
	}
	if out.RuntimeDir != rootPath {
		t.Fatalf("reported runtime dir = %q, want %q", out.RuntimeDir, rootPath)
	}
}

// 只有 related_worktree 的 path 存在感是不够的：work_dir 优先于它。
// work_dir 恰好也是同一个已删目录时必须一并清掉，否则摘了归属运行时 cwd 还在原地。
func TestClearSessionWorktreeOwnershipClearsStaleWorkDir(t *testing.T) {
	service, manager, rootPath := newOwnershipService(t)
	ctx := context.Background()
	created, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Agent: "claude", Name: "wd"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	stale := filepath.Join(t.TempDir(), "gone")
	if _, err := manager.RecordRelatedWorktree(ctx, created.Key, "mindfs", stale, "b", "head"); err != nil {
		t.Fatalf("record worktree: %v", err)
	}
	if _, err := manager.SetWorkDir(ctx, created.Key, stale); err != nil {
		t.Fatalf("set work dir: %v", err)
	}

	out, err := service.ClearSessionWorktreeOwnership(ctx, ClearSessionWorktreeOwnershipInput{
		RootID: "mindfs", Key: created.Key,
	})
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if out.WorkDir != "" {
		t.Fatalf("stale work dir survived the clear: %q", out.WorkDir)
	}
	if current, _ := manager.Get(ctx, created.Key, 0); strings.TrimSpace(current.WorkDir) != "" {
		t.Fatalf("work dir in store = %q, want empty", current.WorkDir)
	}
	if out.RuntimeDir != rootPath {
		t.Fatalf("runtime dir = %q, want %q", out.RuntimeDir, rootPath)
	}
}

// 幂等：本来就没有归属时不是错误，脚本可以对每个会话无脑调用一遍。
func TestClearSessionWorktreeOwnershipIsIdempotent(t *testing.T) {
	service, manager, rootPath := newOwnershipService(t)
	ctx := context.Background()
	created, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Agent: "claude"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	out, err := service.ClearSessionWorktreeOwnership(ctx, ClearSessionWorktreeOwnershipInput{
		RootID: "mindfs", Key: created.Key,
	})
	if err != nil {
		t.Fatalf("clear on a session without ownership: %v", err)
	}
	if out.Cleared {
		t.Fatalf("reported cleared=true with nothing to clear: %+v", out)
	}
	if out.RuntimeDir != rootPath {
		t.Fatalf("runtime dir = %q, want %q", out.RuntimeDir, rootPath)
	}
	// 再调一次也不该变。
	again, err := service.ClearSessionWorktreeOwnership(ctx, ClearSessionWorktreeOwnershipInput{
		RootID: "mindfs", Key: created.Key,
	})
	if err != nil || again.Cleared {
		t.Fatalf("second clear = %+v err=%v, want idempotent no-op", again, err)
	}
}

// 旧工作树里的「相关文件」条目会指向一堆不存在的路径，必须跟着一起清。
// 只清前缀，主 checkout 之外别的东西不能误伤。
func TestClearSessionWorktreeOwnershipDropsRelatedFilesUnderOldWorktree(t *testing.T) {
	service, manager, _ := newOwnershipService(t)
	ctx := context.Background()
	created, err := manager.Create(ctx, session.CreateInput{
		Type: session.TypeChat, Agent: "claude", Source: "worktree", TaskID: "task-36",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	gone := filepath.Join(t.TempDir(), "task-36")
	if _, err := manager.RecordRelatedWorktree(ctx, created.Key, "mindfs", gone, "task-36", "head"); err != nil {
		t.Fatalf("record worktree: %v", err)
	}
	inside := filepath.Join(gone, "server", "main.go")
	outside := filepath.Join(t.TempDir(), "keep-me.txt")
	for _, path := range []string{inside, outside} {
		if err := manager.AddRelatedFile(ctx, created.Key, session.RelatedFile{
			Path: path, RepoPath: gone, Relation: "modified", Head: "head",
		}); err != nil {
			t.Fatalf("add related file %s: %v", path, err)
		}
	}

	out, err := service.ClearSessionWorktreeOwnership(ctx, ClearSessionWorktreeOwnershipInput{
		RootID: "mindfs", Key: created.Key,
	})
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if out.RelatedFilesRemoved == 0 {
		t.Fatalf("removed no related files: %+v", out)
	}
	current := mustGet(t, manager, created.Key)
	if len(current.RelatedFiles) != 0 {
		// RepoPath 也指在旧工作树里，所以两条都该被清。
		t.Fatalf("related files survived: %+v", current.RelatedFiles)
	}
}

// 脚本手里只有 CLAUDE_CODE_SESSION_ID：反查必须打到**它自己的**那条会话上，
// 打错了就是清掉别人工作树的归属 —— 数据看着成功、实际错位。
func TestClearSessionWorktreeOwnershipResolvesAgentSessionID(t *testing.T) {
	service, manager, rootPath := newOwnershipService(t)
	ctx := context.Background()
	target, err := manager.Create(ctx, session.CreateInput{
		Type: session.TypeChat, Agent: "claude", Source: "worktree", TaskID: "task-77",
	})
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	other, err := manager.Create(ctx, session.CreateInput{
		Type: session.TypeChat, Agent: "claude", Source: "worktree", TaskID: "task-78",
	})
	if err != nil {
		t.Fatalf("create other: %v", err)
	}
	const agentID = "6cfeb5a2-7b0f-4f69-90dd-9df2269a6956"
	if err := manager.UpsertAgentBinding(ctx, session.AgentBinding{
		SessionKey: target.Key, Agent: "claude", AgentSessionID: agentID,
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	for key, task := range map[string]string{target.Key: "task-77", other.Key: "task-78"} {
		path := filepath.Join(t.TempDir(), task)
		if _, err := manager.RecordRelatedWorktree(ctx, key, "mindfs", path, task, "head"); err != nil {
			t.Fatalf("record worktree %s: %v", task, err)
		}
	}

	out, err := service.ClearSessionWorktreeOwnership(ctx, ClearSessionWorktreeOwnershipInput{
		RootID: "mindfs", AgentSessionID: agentID,
	})
	if err != nil {
		t.Fatalf("clear by agent session id: %v", err)
	}
	if out.SessionKey != target.Key {
		t.Fatalf("cleared %q, want the bound session %q", out.SessionKey, target.Key)
	}
	if current := mustGet(t, manager, target.Key); current.RelatedWorktree != nil {
		t.Fatalf("target ownership survived: %+v", current.RelatedWorktree)
	}
	if current := mustGet(t, manager, other.Key); current.RelatedWorktree == nil {
		t.Fatal("cleared another session's ownership")
	}
	if out.RuntimeDir != rootPath {
		t.Fatalf("runtime dir = %q, want %q", out.RuntimeDir, rootPath)
	}
}

func TestClearSessionWorktreeOwnershipRejectsEmptyIdentifier(t *testing.T) {
	service, _, _ := newOwnershipService(t)
	_, err := service.ClearSessionWorktreeOwnership(context.Background(), ClearSessionWorktreeOwnershipInput{
		RootID: "mindfs",
	})
	if err == nil || !strings.Contains(err.Error(), "session key or agent session id") {
		t.Fatalf("error = %v, want it to name the missing identifier", err)
	}
}

func TestClearSessionWorktreeOwnershipRejectsUnknownAgentSessionID(t *testing.T) {
	service, _, _ := newOwnershipService(t)
	_, err := service.ClearSessionWorktreeOwnership(context.Background(), ClearSessionWorktreeOwnershipInput{
		RootID: "mindfs", AgentSessionID: "6cfeb5a2-7b0f-4f69-90dd-9df2269a6956",
	})
	if err == nil || !strings.Contains(err.Error(), "no mindfs session bound") {
		t.Fatalf("error = %v, want it to say no mindfs session is bound", err)
	}
}

func mustGet(t *testing.T, manager *session.Manager, key string) *session.Session {
	t.Helper()
	current, err := manager.Get(context.Background(), key, 0)
	if err != nil {
		t.Fatalf("get %s: %v", key, err)
	}
	return current
}
