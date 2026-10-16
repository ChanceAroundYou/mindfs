package usecase

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
	rootfs "mindfs/server/internal/fs"
	"mindfs/server/internal/session"
)

// 批量统计的 usecase 层对拍：逐个 Target 走同一个 resolveGitRelatedFileDiffTarget 解析，
// 结果必须与逐文件 GetGitRelatedFileDiff 完全一致。
//
// 这条测试守的是两件事：
//  1. 分组（按解析出的仓库）没有把不同仓库/不同基线的文件串在一起；
//  2. 输出的 JSON 键名 —— 前端按 id 对齐、读 status/additions/deletions，
//     任何一个 tag 写错都会静默变成「徽标全空」而不报错。
//
// 形状照抄生产实际：repo_path 指向 worktree 绝对路径，path 是项目内相对路径。
func TestGetGitRelatedFileStatsMatchesPerFileDiff(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found")
	}
	rootDir := t.TempDir()
	runUsecaseGit(t, rootDir, "init")
	runUsecaseGit(t, rootDir, "config", "user.email", "test@example.com")
	runUsecaseGit(t, rootDir, "config", "user.name", "Test User")
	mustWriteFile(t, filepath.Join(rootDir, "note.txt"), "base\n")
	runUsecaseGit(t, rootDir, "add", "note.txt")
	runUsecaseGit(t, rootDir, "commit", "-m", "initial")
	runUsecaseGit(t, rootDir, "checkout", "-b", "task-1")
	base := strings.TrimSpace(runUsecaseGit(t, rootDir, "rev-parse", "HEAD"))
	runUsecaseGit(t, rootDir, "checkout", "-")

	worktreeRoot := filepath.Join(rootDir, ".worktree", "task-1")
	runUsecaseGit(t, rootDir, "worktree", "add", worktreeRoot, "task-1")
	mustWriteFile(t, filepath.Join(worktreeRoot, "note.txt"), "worktree-only\n")
	mustWriteFile(t, filepath.Join(rootDir, "note.txt"), "main-only\n")

	root := rootfs.NewRootInfo("mindfs", "mindfs", rootDir)
	service := Service{Registry: uploadTestRegistry{root: root}}

	targets := []GitRelatedFileStatTargetInput{
		{ID: "worktree", Path: "note.txt", RepoPath: worktreeRoot, RepoKind: "git", Head: base},
		{ID: "main", Path: "note.txt", RepoKind: "git"},
		{ID: "no-path", Path: "", RepoKind: "git"},
		{ID: "plain", Path: "note.txt", RepoKind: "plain"},
	}
	out, err := service.GetGitRelatedFileStats(context.Background(), GitRelatedFileStatsInput{
		RootID:  root.ID,
		Targets: targets,
	})
	if err != nil {
		t.Fatalf("GetGitRelatedFileStats: %v", err)
	}
	if len(out.Stats) != len(targets) {
		t.Fatalf("stats = %d, want %d", len(out.Stats), len(targets))
	}

	// 顺序与 ID 必须一一对上 —— 前端就是按 id 取值的。
	for i, stat := range out.Stats {
		if stat.ID != targets[i].ID {
			t.Fatalf("stats[%d].ID = %q, want %q（顺序漂了前端会错位）", i, stat.ID, targets[i].ID)
		}
	}

	// 前两个目标必须与逐文件结果逐字段一致。
	for _, index := range []int{0, 1} {
		target := targets[index]
		diff, err := service.GetGitRelatedFileDiff(context.Background(), GitRelatedFileDiffInput{
			RootID:   root.ID,
			RepoPath: target.RepoPath,
			RepoKind: target.RepoKind,
			Head:     target.Head,
			Path:     target.Path,
		})
		if err != nil {
			t.Fatalf("GetGitRelatedFileDiff(%s): %v", target.ID, err)
		}
		got := out.Stats[index]
		if got.Status != diff.Diff.Status || got.Additions != diff.Diff.Additions || got.Deletions != diff.Diff.Deletions {
			t.Fatalf("%s: batch = {%q %d %d}, per-file = {%q %d %d}",
				target.ID, got.Status, got.Additions, got.Deletions,
				diff.Diff.Status, diff.Diff.Additions, diff.Diff.Deletions)
		}
	}
	if out.Stats[0].Status == "" {
		t.Fatal("worktree 目标应当有变更（+1 −1），留空说明 repo_path 没被用上")
	}

	// 空路径与 plain 都被跳过，留空而不是报错。
	if out.Stats[2].Status != "" || out.Stats[3].Status != "" {
		t.Fatalf("空路径/plain 应当留空，得到 %+v", out.Stats[2:])
	}

	// JSON 键名是前后端契约，写错不会报错、只会让徽标全空。
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, key := range []string{`"stats"`, `"id"`, `"status"`, `"additions"`, `"deletions"`} {
		if !bytes.Contains(raw, []byte(key)) {
			t.Fatalf("响应缺少键 %s：%s", key, raw)
		}
	}
}

func TestSaveUploadedFilesDefaultsToAttachmentDirAndRenamesConflicts(t *testing.T) {
	rootDir := t.TempDir()
	root := rootfs.NewRootInfo("mindfs", "mindfs", rootDir)
	service := Service{Registry: uploadTestRegistry{root: root}}

	out, err := service.SaveUploadedFiles(context.Background(), SaveUploadedFilesInput{
		RootID: "mindfs",
		Files: []UploadFile{
			{
				Name:        "demo.txt",
				ContentType: "text/plain; charset=utf-8",
				Reader:      bytes.NewBufferString("first file"),
			},
			{
				Name:        "demo.txt",
				ContentType: "text/plain",
				Reader:      bytes.NewBufferString("second file"),
			},
		},
	})
	if err != nil {
		t.Fatalf("SaveUploadedFiles returned error: %v", err)
	}
	if len(out.Files) != 2 {
		t.Fatalf("expected 2 saved files, got %d", len(out.Files))
	}

	dateDir := time.Now().Format("2006-01-02")
	wantFirst := filepath.ToSlash(filepath.Join(".mindfs", "upload", dateDir, "demo.txt"))
	wantSecond := filepath.ToSlash(filepath.Join(".mindfs", "upload", dateDir, "demo (1).txt"))
	if out.Files[0].Path != wantFirst {
		t.Fatalf("first upload path = %q, want %q", out.Files[0].Path, wantFirst)
	}
	if out.Files[1].Path != wantSecond {
		t.Fatalf("second upload path = %q, want %q", out.Files[1].Path, wantSecond)
	}
	if out.Files[0].Mime != "text/plain" {
		t.Fatalf("first upload mime = %q, want text/plain", out.Files[0].Mime)
	}
	if out.Files[1].Name != "demo (1).txt" {
		t.Fatalf("second upload name = %q, want %q", out.Files[1].Name, "demo (1).txt")
	}

	assertFileContent(t, filepath.Join(rootDir, filepath.FromSlash(wantFirst)), "first file")
	assertFileContent(t, filepath.Join(rootDir, filepath.FromSlash(wantSecond)), "second file")
}

func TestGetGitRelatedFileDiffResolvesTaskWorktreePath(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found")
	}
	rootDir := t.TempDir()
	runUsecaseGit(t, rootDir, "init")
	runUsecaseGit(t, rootDir, "config", "user.email", "test@example.com")
	runUsecaseGit(t, rootDir, "config", "user.name", "Test User")
	mustWriteFile(t, filepath.Join(rootDir, "note.txt"), "base\n")
	runUsecaseGit(t, rootDir, "add", "note.txt")
	runUsecaseGit(t, rootDir, "commit", "-m", "initial")
	runUsecaseGit(t, rootDir, "checkout", "-b", "task-1")
	base := strings.TrimSpace(runUsecaseGit(t, rootDir, "rev-parse", "HEAD"))
	runUsecaseGit(t, rootDir, "checkout", "-")

	worktreeRoot := filepath.Join(rootDir, ".worktree", "task-1")
	runUsecaseGit(t, rootDir, "worktree", "add", worktreeRoot, "task-1")
	mustWriteFile(t, filepath.Join(rootDir, "note.txt"), "main-only\n")
	mustWriteFile(t, filepath.Join(worktreeRoot, "note.txt"), "worktree-only\n")

	root := rootfs.NewRootInfo("mindfs", "mindfs", rootDir)
	service := Service{Registry: uploadTestRegistry{root: root}}
	out, err := service.GetGitRelatedFileDiff(context.Background(), GitRelatedFileDiffInput{
		RootID:   root.ID,
		RepoPath: rootDir,
		RepoKind: "git",
		Head:     base,
		Path:     ".worktree/task-1/note.txt",
	})
	if err != nil {
		t.Fatalf("GetGitRelatedFileDiff returned error: %v", err)
	}
	if !strings.Contains(out.Diff.Content, "+worktree-only") {
		t.Fatalf("diff content does not contain worktree change:\n%s", out.Diff.Content)
	}
	if strings.Contains(out.Diff.Content, "main-only") {
		t.Fatalf("diff content used main worktree instead of task worktree:\n%s", out.Diff.Content)
	}

	out, err = service.GetGitRelatedFileDiff(context.Background(), GitRelatedFileDiffInput{
		RootID:   root.ID,
		RepoKind: "git",
		Head:     base,
		Path:     ".worktree/task-1/note.txt",
	})
	if err != nil {
		t.Fatalf("GetGitRelatedFileDiff without repo path returned error: %v", err)
	}
	if !strings.Contains(out.Diff.Content, "+worktree-only") {
		t.Fatalf("diff without repo path does not contain worktree change:\n%s", out.Diff.Content)
	}
	if strings.Contains(out.Diff.Content, "main-only") {
		t.Fatalf("diff without repo path used main worktree instead of task worktree:\n%s", out.Diff.Content)
	}
}

func TestNormalizeSessionRelatedFilesResolvesLegacyTaskWorktreePath(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found")
	}
	rootDir := t.TempDir()
	runUsecaseGit(t, rootDir, "init")
	runUsecaseGit(t, rootDir, "config", "user.email", "test@example.com")
	runUsecaseGit(t, rootDir, "config", "user.name", "Test User")
	mustWriteFile(t, filepath.Join(rootDir, "note.txt"), "base\n")
	runUsecaseGit(t, rootDir, "add", "note.txt")
	runUsecaseGit(t, rootDir, "commit", "-m", "initial")
	runUsecaseGit(t, rootDir, "checkout", "-b", "task-1")
	runUsecaseGit(t, rootDir, "checkout", "-")

	worktreeRoot := filepath.Join(rootDir, ".worktree", "task-1")
	runUsecaseGit(t, rootDir, "worktree", "add", worktreeRoot, "task-1")
	mustWriteFile(t, filepath.Join(worktreeRoot, "note.txt"), "worktree-only\n")

	root := rootfs.NewRootInfo("mindfs", "mindfs", rootDir)
	files := normalizeSessionRelatedFiles(context.Background(), root, []session.RelatedFile{{
		RootID:   root.ID,
		RepoPath: rootDir,
		RepoName: root.Name,
		RepoKind: "git",
		Path:     ".worktree/task-1/note.txt",
		Head:     strings.TrimSpace(runUsecaseGit(t, worktreeRoot, "rev-parse", "HEAD")),
	}})
	if len(files) != 1 {
		t.Fatalf("files len = %d, want 1", len(files))
	}
	if !sameUsecaseTestPath(files[0].RepoPath, worktreeRoot) {
		t.Fatalf("RepoPath = %q, want %q", files[0].RepoPath, worktreeRoot)
	}
	if files[0].Path != "note.txt" {
		t.Fatalf("Path = %q, want note.txt", files[0].Path)
	}
}

func TestGetGitDiffUsesRepoPath(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found")
	}
	rootDir := t.TempDir()
	runUsecaseGit(t, rootDir, "init")
	runUsecaseGit(t, rootDir, "config", "user.email", "test@example.com")
	runUsecaseGit(t, rootDir, "config", "user.name", "Test User")
	mustWriteFile(t, filepath.Join(rootDir, "note.txt"), "base\n")
	runUsecaseGit(t, rootDir, "add", "note.txt")
	runUsecaseGit(t, rootDir, "commit", "-m", "initial")
	runUsecaseGit(t, rootDir, "checkout", "-b", "task-1")
	runUsecaseGit(t, rootDir, "checkout", "-")

	worktreeRoot := filepath.Join(rootDir, ".worktree", "task-1")
	runUsecaseGit(t, rootDir, "worktree", "add", worktreeRoot, "task-1")
	mustWriteFile(t, filepath.Join(rootDir, "note.txt"), "main-only\n")
	mustWriteFile(t, filepath.Join(worktreeRoot, "note.txt"), "worktree-only\n")

	root := rootfs.NewRootInfo("mindfs", "mindfs", rootDir)
	service := Service{Registry: uploadTestRegistry{root: root}}
	out, err := service.GetGitDiff(context.Background(), GitDiffInput{
		RootID:   root.ID,
		RepoPath: worktreeRoot,
		Path:     "note.txt",
	})
	if err != nil {
		t.Fatalf("GetGitDiff returned error: %v", err)
	}
	if !strings.Contains(out.Diff.Content, "+worktree-only") {
		t.Fatalf("diff content does not contain worktree change:\n%s", out.Diff.Content)
	}
	if strings.Contains(out.Diff.Content, "main-only") {
		t.Fatalf("diff content used main worktree instead of task worktree:\n%s", out.Diff.Content)
	}
}

func TestSaveUploadedFilesUsesExplicitDir(t *testing.T) {
	rootDir := t.TempDir()
	root := rootfs.NewRootInfo("mindfs", "mindfs", rootDir)
	service := Service{Registry: uploadTestRegistry{root: root}}

	out, err := service.SaveUploadedFiles(context.Background(), SaveUploadedFilesInput{
		RootID: "mindfs",
		Dir:    "design",
		Files: []UploadFile{
			{
				Name:        "spec.pdf",
				ContentType: "application/pdf",
				Reader:      bytes.NewBufferString("pdf-bytes"),
			},
		},
	})
	if err != nil {
		t.Fatalf("SaveUploadedFiles returned error: %v", err)
	}
	if len(out.Files) != 1 {
		t.Fatalf("expected 1 saved file, got %d", len(out.Files))
	}
	if out.Files[0].Path != "design/spec.pdf" {
		t.Fatalf("saved path = %q, want %q", out.Files[0].Path, "design/spec.pdf")
	}
	assertFileContent(t, filepath.Join(rootDir, "design", "spec.pdf"), "pdf-bytes")
}

func assertFileContent(t *testing.T, path string, want string) {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}
	if string(payload) != want {
		t.Fatalf("file content = %q, want %q", string(payload), want)
	}
}

func mustWriteFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

func runUsecaseGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s returned error: %v\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out)
}

func sameUsecaseTestPath(left, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if resolved, err := filepath.EvalSymlinks(left); err == nil {
		left = filepath.Clean(resolved)
	}
	if resolved, err := filepath.EvalSymlinks(right); err == nil {
		right = filepath.Clean(resolved)
	}
	return left == right
}

// 列表页给命令会话补 shell 标签必须仍然可用（fillCommandShells 改走 aux 尾部有界读
// manager.GetCommandShell 之后的接线守卫）。全量读 aux 的版本在实测会话上是
// 11.8MB/281ms 且持 m.mu，列表页对每个命令会话各读一次。
func TestListSessionsFillsCommandShellFromAuxTail(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := session.NewManager(root)
	registry := &commandTestRegistry{root: root, manager: manager}
	service := Service{Registry: registry}
	ctx := context.Background()

	command, err := manager.Create(ctx, session.CreateInput{Type: session.TypeCommand, Name: "Cmd"})
	if err != nil {
		t.Fatalf("create command session: %v", err)
	}
	if err := manager.AddExchangeAux(ctx, command.Key, session.ExchangeAux{
		Seq: 1,
		ToolCall: &agenttypes.ToolCall{
			CallID:  "call-1",
			Kind:    agenttypes.ToolKindExecute,
			RawType: "commandExecution",
			Meta: map[string]any{
				"source": "userShell",
				"phase":  "final",
				"shell":  "bash",
			},
		},
	}); err != nil {
		t.Fatalf("add aux: %v", err)
	}
	// 非命令会话不该被填充。
	if _, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Name: "Chat"}); err != nil {
		t.Fatalf("create chat session: %v", err)
	}

	out, err := service.ListSessions(ctx, ListSessionsInput{RootID: root.ID, Limit: 50})
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	var found bool
	for _, item := range out.Sessions {
		if item == nil || item.Key != command.Key {
			continue
		}
		found = true
		if item.Shell != "bash" {
			t.Fatalf("command session shell = %q, want bash", item.Shell)
		}
	}
	if !found {
		t.Fatalf("command session missing from list: %v", usecaseSessionKeys(out.Sessions))
	}
}
