package kanban

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 收尾要**删掉整个 worktree 目录**，连 git 不跟踪的工具状态目录一起。
//
// 两个形态都要覆盖：
//   - 还是活 worktree：`git worktree remove` 会因为未跟踪文件拒绝，收尾先清掉工具
//     状态目录再拆，拆完目录也没了。
//   - 已经不是活 worktree（git 登记没了 / 被手工 prune）但目录还在：只剩一个空壳，
//     收尾必须把它删干净 —— 否则 .worktree/ 会越积越多。
func TestFinishTaskWorktreeRemovesTheWholeDirectoryIncludingToolState(t *testing.T) {
	t.Run("live worktree with tool state inside", func(t *testing.T) {
		svc, root, _, worktreePath := finishFixture(t, "from worktree\n")
		taskID := mustTaskID(t, svc, root.ID)
		// 收尾段跑完留下的工具状态目录（真实现场里就是这三个）。
		for _, name := range []string{".mindfs", ".omc", ".claude"} {
			if err := os.MkdirAll(filepath.Join(worktreePath, name), 0o755); err != nil {
				t.Fatalf("seed %s: %v", name, err)
			}
			if err := os.WriteFile(filepath.Join(worktreePath, name, "state.bin"), []byte("state"), 0o644); err != nil {
				t.Fatalf("seed %s file: %v", name, err)
			}
		}

		result, err := svc.FinishTaskWorktree(context.Background(), FinishWorktreeInput{
			RootID:       root.ID,
			TaskID:       taskID,
			DeleteBranch: true,
			PruneOrphans: true,
		})
		if err != nil {
			t.Fatalf("FinishTaskWorktree: %v", err)
		}
		if !result.WorktreeRemoved {
			t.Fatalf("WorktreeRemoved = false, want git to have removed it")
		}
		if _, statErr := os.Stat(worktreePath); !os.IsNotExist(statErr) {
			t.Fatalf("the worktree dir must be gone entirely, stat err = %v", statErr)
		}
	})

	t.Run("orphan shell left after git forgot it", func(t *testing.T) {
		svc, root, mainDir, worktreePath := finishFixture(t, "from worktree\n")
		taskID := mustTaskID(t, svc, root.ID)
		// 造出「git 已经不认、磁盘上还在」的空壳：先让 git 拆掉，再把状态目录放回去。
		gitForTest(t, mainDir, "worktree", "remove", "--force", worktreePath)
		if err := os.MkdirAll(filepath.Join(worktreePath, ".mindfs"), 0o755); err != nil {
			t.Fatalf("reseed shell: %v", err)
		}
		if err := os.WriteFile(filepath.Join(worktreePath, ".mindfs", "sessions.db"), []byte("state"), 0o644); err != nil {
			t.Fatalf("reseed shell file: %v", err)
		}

		result, err := svc.FinishTaskWorktree(context.Background(), FinishWorktreeInput{
			RootID: root.ID,
			TaskID: taskID,
		})
		if err != nil {
			t.Fatalf("FinishTaskWorktree: %v", err)
		}
		if !result.WorktreeDirRemoved {
			t.Fatalf("WorktreeDirRemoved = false (reason=%q), want the shell gone", result.DirRemoveReason)
		}
		if _, statErr := os.Stat(worktreePath); !os.IsNotExist(statErr) {
			t.Fatalf("the orphan shell must be gone, stat err = %v", statErr)
		}
	})
}

// 拆目录之前必须把 worktree 里用户上传的附件搬回主 checkout —— 否则它们会跟着
// 目录一起消失。只搬 upload/：会话库/任务库的权威副本本来就在主 checkout。
func TestFinishTaskWorktreeMigratesWorktreeUploads(t *testing.T) {
	svc, root, mainDir, worktreePath := finishFixture(t, "from worktree\n")
	taskID := mustTaskID(t, svc, root.ID)
	// 用户在 worktree 会话里传过两个文件（按 upload/<日期>/<文件> 的形状）。
	uploads := []string{
		filepath.Join("2026-01-01", "a.png"),
		filepath.Join("2026-01-02", "b.txt"),
	}
	for _, rel := range uploads {
		full := filepath.Join(worktreePath, ".mindfs", "upload", rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir uploads: %v", err)
		}
		if err := os.WriteFile(full, []byte("payload "+rel+"\n"), 0o644); err != nil {
			t.Fatalf("write upload: %v", err)
		}
	}

	result, err := svc.FinishTaskWorktree(context.Background(), FinishWorktreeInput{
		RootID: root.ID,
		TaskID: taskID,
	})
	if err != nil {
		t.Fatalf("FinishTaskWorktree: %v", err)
	}
	if len(result.MigratedUploads) != len(uploads) {
		t.Fatalf("MigratedUploads = %v, want %v", result.MigratedUploads, uploads)
	}
	// 内容真的到了主 checkout。
	for _, rel := range uploads {
		got, readErr := os.ReadFile(filepath.Join(mainDir, ".mindfs", "upload", rel))
		if readErr != nil {
			t.Fatalf("upload %s did not survive the teardown: %v", rel, readErr)
		}
		if !strings.Contains(string(got), rel) {
			t.Fatalf("upload %s content = %q, want the original payload", rel, got)
		}
	}
}

// 主 checkout 已有同名附件时**不覆盖**：那份是权威的，覆盖等于用旧换新。
func TestFinishTaskWorktreeDoesNotOverwriteExistingUploads(t *testing.T) {
	svc, root, mainDir, worktreePath := finishFixture(t, "from worktree\n")
	taskID := mustTaskID(t, svc, root.ID)
	rel := filepath.Join("2026-01-01", "a.png")
	// 主 checkout 先有一份权威内容。
	mainFile := filepath.Join(mainDir, ".mindfs", "upload", rel)
	if err := os.MkdirAll(filepath.Dir(mainFile), 0o755); err != nil {
		t.Fatalf("mkdir main uploads: %v", err)
	}
	if err := os.WriteFile(mainFile, []byte("authoritative\n"), 0o644); err != nil {
		t.Fatalf("write main upload: %v", err)
	}
	// worktree 里有一份同名的旧副本。
	wtFile := filepath.Join(worktreePath, ".mindfs", "upload", rel)
	if err := os.MkdirAll(filepath.Dir(wtFile), 0o755); err != nil {
		t.Fatalf("mkdir wt uploads: %v", err)
	}
	if err := os.WriteFile(wtFile, []byte("stale\n"), 0o644); err != nil {
		t.Fatalf("write wt upload: %v", err)
	}

	result, err := svc.FinishTaskWorktree(context.Background(), FinishWorktreeInput{
		RootID: root.ID,
		TaskID: taskID,
	})
	if err != nil {
		t.Fatalf("FinishTaskWorktree: %v", err)
	}
	if len(result.MigratedUploads) != 0 {
		t.Fatalf("MigratedUploads = %v, want empty — an existing upload must win", result.MigratedUploads)
	}
	got, readErr := os.ReadFile(mainFile)
	if readErr != nil {
		t.Fatalf("read main upload: %v", readErr)
	}
	if strings.TrimSpace(string(got)) != "authoritative" {
		t.Fatalf("main upload was overwritten: %q", got)
	}
}

// 别的任务留下的孤儿目录：只剩工具状态目录的空壳自动删掉；还有别的内容的只列不删。
//
// 判据只认白名单（.mindfs/ .omc/ .claude/），不来自任何外部输入 —— 含用户文件的
// 目录绝不能自动删。
func TestFinishTaskWorktreePrunesOnlyToolStateOrphanDirs(t *testing.T) {
	svc, root, mainDir, _ := finishFixture(t, "from worktree\n")
	taskID := mustTaskID(t, svc, root.ID)

	// 空壳：只有工具状态目录。
	shellDir := filepath.Join(mainDir, ".worktree", "task-9")
	if err := os.MkdirAll(filepath.Join(shellDir, ".claude"), 0o755); err != nil {
		t.Fatalf("seed shell: %v", err)
	}
	if err := os.WriteFile(filepath.Join(shellDir, ".claude", "local.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("seed shell file: %v", err)
	}
	// 有真内容：不能自动删。
	keptDir := filepath.Join(mainDir, ".worktree", "task-8")
	if err := os.MkdirAll(keptDir, 0o755); err != nil {
		t.Fatalf("seed kept: %v", err)
	}
	if err := os.WriteFile(filepath.Join(keptDir, "notes.txt"), []byte("mine\n"), 0o644); err != nil {
		t.Fatalf("seed kept file: %v", err)
	}

	result, err := svc.FinishTaskWorktree(context.Background(), FinishWorktreeInput{
		RootID:       root.ID,
		TaskID:       taskID,
		PruneOrphans: true,
	})
	if err != nil {
		t.Fatalf("FinishTaskWorktree: %v", err)
	}
	if len(result.RemovedOrphans) != 1 || result.RemovedOrphans[0] != shellDir {
		t.Fatalf("RemovedOrphans = %v, want [%s]", result.RemovedOrphans, shellDir)
	}
	if _, statErr := os.Stat(shellDir); !os.IsNotExist(statErr) {
		t.Fatalf("the tool-state-only shell must be gone, stat err = %v", statErr)
	}
	// 有真内容的目录必须原样留着，并出现在 Orphans 里让用户看见。
	if _, statErr := os.Stat(filepath.Join(keptDir, "notes.txt")); statErr != nil {
		t.Fatalf("an orphan with real content must survive: %v", statErr)
	}
	found := false
	for _, orphan := range result.Orphans {
		if orphan.Path == keptDir {
			found = true
		}
	}
	if !found {
		t.Fatalf("Orphans = %+v, want %s listed", result.Orphans, keptDir)
	}
}

// 收尾不能被**自己产生的**未跟踪目录挡住：agent 在收尾段跑完，worktree 里会留下
// .claude/ 和 .omc/，而服务端下一步就是拆这个目录。2026-10-02 端到端实测撞的就是
// 这个 —— git 报「contains modified or untracked files」，收尾卡住，活其实早就
// 并回主干了。
func TestFinishTearsDownDespiteTheStateDirsTheAgentLeftBehind(t *testing.T) {
	svc, root, mainDir, worktreePath := finishFixture(t, "work")
	task := parkAtWaitingUser(t, svc, root.ID, finishTaskID(t, svc, root.ID))
	_ = task
	// 造出「收尾段跑完的现场」：工具状态目录，未跟踪。
	for _, name := range []string{".claude", ".omc"} {
		dir := filepath.Join(worktreePath, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte("{}"), 0o644); err != nil {
			t.Fatalf("write %s state: %v", name, err)
		}
	}
	if out := gitForTest(t, worktreePath, "status", "--porcelain"); !strings.Contains(out, "?? .claude") {
		t.Fatalf("fixture must have untracked state dirs, got: %q", out)
	}

	if _, err := svc.FinishTaskWorktree(context.Background(), FinishWorktreeInput{
		RootID: root.ID, TaskID: finishTaskID(t, svc, root.ID),
		DeleteBranch: true, PruneOrphans: true,
	}); err != nil {
		t.Fatalf("finish must not be blocked by the tool's own leftovers: %v", err)
	}
	if _, err := os.Stat(worktreePath); !os.IsNotExist(err) {
		t.Fatalf("the worktree directory should be gone, stat err = %v", err)
	}
	if out := gitForTest(t, mainDir, "branch", "--list", "task-1"); strings.Contains(out, "task-1") {
		t.Fatalf("the branch should be deleted, got: %q", out)
	}
	_ = mainDir
}

// 状态目录 + 用户自己的未跟踪文件混在一起时：只清状态目录，用户的那个要挡住收尾。
func TestFinishCleansStateDirsButStillRefusesForTheUsersOwnFiles(t *testing.T) {
	svc, root, _, worktreePath := finishFixture(t, "work")
	if err := os.MkdirAll(filepath.Join(worktreePath, ".omc", "nested"), 0o755); err != nil {
		t.Fatalf("mkdir .omc: %v", err)
	}
	mine := filepath.Join(worktreePath, "my-notes.md")
	if err := os.WriteFile(mine, []byte("keep me"), 0o644); err != nil {
		t.Fatalf("write user file: %v", err)
	}

	_, err := svc.FinishTaskWorktree(context.Background(), FinishWorktreeInput{
		RootID: root.ID, TaskID: finishTaskID(t, svc, root.ID),
		DeleteBranch: true, PruneOrphans: true,
	})
	if err == nil {
		t.Fatal("an untracked file the user wrote must still block the teardown")
	}
	var userChanges *FinishWorktreeUserChanges
	if !errors.As(err, &userChanges) {
		t.Fatalf("err = %T (%v), want *FinishWorktreeUserChanges", err, err)
	}
	if len(userChanges.Files) != 1 || userChanges.Files[0] != "my-notes.md" {
		t.Fatalf("Files = %v, want [my-notes.md]", userChanges.Files)
	}
	if _, statErr := os.Stat(mine); statErr != nil {
		t.Fatalf("the user's own file must not be deleted: %v", statErr)
	}
	if content := readFileString(t, mine); content != "keep me" {
		t.Fatalf("the user's file content must be untouched, got %q", content)
	}
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
