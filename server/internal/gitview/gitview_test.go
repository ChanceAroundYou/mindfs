package gitview

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func TestDecodeGitDiffOutputDecodesGB18030Text(t *testing.T) {
	source := "diff --git a/main.go b/main.go\n@@ -1 +1 @@\n-旧内容\n+新内容\n"
	encoded, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte(source))
	if err != nil {
		t.Fatalf("encode GB18030: %v", err)
	}

	got := decodeGitDiffOutput(encoded, ".go")
	if got != source {
		t.Fatalf("decoded diff = %q, want %q", got, source)
	}
}

func TestReadRelatedFileDiffUsesNextCommitAfterBase(t *testing.T) {
	root := initTestRepo(t)
	writeTestFile(t, root, "note.txt", "before\n")
	runTestGit(t, root, "add", "note.txt")
	runTestGit(t, root, "commit", "-m", "initial")
	base := strings.TrimSpace(runTestGit(t, root, "rev-parse", "HEAD"))

	writeTestFile(t, root, "note.txt", "after\n")
	runTestGit(t, root, "add", "note.txt")
	runTestGit(t, root, "commit", "-m", "update")

	diff, err := ReadRelatedFileDiff(context.Background(), root, base, "note.txt")
	if err != nil {
		t.Fatalf("ReadRelatedFileDiff: %v", err)
	}
	if diff.BaseHead != base {
		t.Fatalf("BaseHead = %q, want %q", diff.BaseHead, base)
	}
	if diff.TargetHead == "" {
		t.Fatal("TargetHead is empty")
	}
	if diff.Source != "commit_range" {
		t.Fatalf("Source = %q, want commit_range", diff.Source)
	}
	if !strings.Contains(diff.Content, "-before") || !strings.Contains(diff.Content, "+after") {
		t.Fatalf("diff content does not contain expected change:\n%s", diff.Content)
	}
}

func TestListWorktreesOnNonRepoReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	if _, err := loadRepoContext(context.Background(), dir); err == nil {
		t.Skip("temp dir resolves inside a git repo")
	}
	// 非 git 根（如 ~/projects/llmux 这类 plain 根）不该报错，只是没有 worktree。
	// 曾经这里抛 400，前端把 git 的原始报错渲染到面板上。
	result, err := ListWorktrees(context.Background(), dir)
	if err != nil {
		t.Fatalf("ListWorktrees on non-repo should not error: %v", err)
	}
	if len(result.Items) != 0 {
		t.Fatalf("Items = %+v, want empty", result.Items)
	}
}

// samePath 必须先解析符号链接再比 —— git 报的路径是解析后的，而 MainCheckoutPath
// 对主 checkout 返回的是调用方给的那个路径（可能是符号链接本身）。
func TestSamePathResolvesSymlinks(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	if !samePath(real, link) {
		t.Fatal("samePath should resolve symlinks")
	}
	if !samePath(link, real) {
		t.Fatal("samePath should be symmetric")
	}
	if !samePath(real, real) {
		t.Fatal("samePath should handle identical paths")
	}
	if samePath(real, t.TempDir()) {
		t.Fatal("samePath should return false for different paths")
	}
}

// validateTarget 不能因为主 checkout 是符号链接就自我阻塞 —— 实测
// /home/xiaokubao/family → /mnt/fnos/family，收尾时报「main 正被另一个 worktree
// 占用」，而那个「另一个 worktree」就是主 checkout 自己。
func TestValidateTargetDoesNotSelfBlockViaSymlink(t *testing.T) {
	root := initTestRepo(t)
	writeTestFile(t, root, "note.txt", "before\n")
	runTestGit(t, root, "add", "note.txt")
	runTestGit(t, root, "commit", "-m", "initial")
	branch := strings.TrimSpace(runTestGit(t, root, "rev-parse", "--abbrev-ref", "HEAD"))

	// 通过符号链接访问主 checkout —— 这正是 /home/xiaokubao/family → /mnt/fnos/family 的形态。
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(root, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	// MainCheckoutPath 对主 checkout 返回的是**调用方给的那个路径**（即符号链接本身），
	// 而 git 报的是解析后的路径。validateTarget 必须能认出这是同一个目录。
	mainDir, err := MainCheckoutPath(context.Background(), link)
	if err != nil {
		t.Fatalf("MainCheckoutPath: %v", err)
	}
	if err := validateTarget(context.Background(), mainDir, "", branch); err != nil {
		t.Fatalf("validateTarget should not self-block via symlink: %v", err)
	}
}

// ListWorktrees 的 Current 标记必须对符号链接访问也成立 —— 否则根 worktree 的
// Current 标志在符号链接路径下会错误地变成 false。
func TestListWorktreesCurrentViaSymlink(t *testing.T) {
	root := initTestRepo(t)
	writeTestFile(t, root, "note.txt", "before\n")
	runTestGit(t, root, "add", "note.txt")
	runTestGit(t, root, "commit", "-m", "initial")

	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(root, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	result, err := ListWorktrees(context.Background(), link)
	if err != nil {
		t.Fatalf("ListWorktrees: %v", err)
	}
	if len(result.Items) == 0 {
		t.Fatal("expected at least the root worktree")
	}
	var found bool
	for _, item := range result.Items {
		if item.Current {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("root worktree should be marked Current even when accessed via symlink")
	}
}

func TestReadRelatedFileDiffSkipsUntouchedCommits(t *testing.T) {
	root := initTestRepo(t)
	writeTestFile(t, root, "note.txt", "before\n")
	runTestGit(t, root, "add", "note.txt")
	runTestGit(t, root, "commit", "-m", "initial")
	base := strings.TrimSpace(runTestGit(t, root, "rev-parse", "HEAD"))

	// 记录的 base 与真正改动该文件的提交之间夹着无关提交是常态
	// （实测库内 42/118 条关联文件因此报 400），基线解析必须跳过它们。
	writeTestFile(t, root, "other.txt", "unrelated\n")
	runTestGit(t, root, "add", "other.txt")
	runTestGit(t, root, "commit", "-m", "unrelated")

	writeTestFile(t, root, "note.txt", "after\n")
	runTestGit(t, root, "add", "note.txt")
	runTestGit(t, root, "commit", "-m", "update")
	target := strings.TrimSpace(runTestGit(t, root, "rev-parse", "HEAD"))

	diff, err := ReadRelatedFileDiff(context.Background(), root, base, "note.txt")
	if err != nil {
		t.Fatalf("ReadRelatedFileDiff: %v", err)
	}
	if diff.TargetHead != target {
		t.Fatalf("TargetHead = %q, want %q", diff.TargetHead, target)
	}
	if diff.Source != "commit_range" {
		t.Fatalf("Source = %q, want commit_range", diff.Source)
	}
	if !strings.Contains(diff.Content, "-before") || !strings.Contains(diff.Content, "+after") {
		t.Fatalf("diff content does not contain expected change:\n%s", diff.Content)
	}
}

func TestReadRelatedFileDiffEmptyWhenNothingChanged(t *testing.T) {
	root := initTestRepo(t)
	writeTestFile(t, root, "note.txt", "same\n")
	runTestGit(t, root, "add", "note.txt")
	runTestGit(t, root, "commit", "-m", "initial")
	head := strings.TrimSpace(runTestGit(t, root, "rev-parse", "HEAD"))

	// base 之后既没有提交碰过该文件、工作区也干净 —— 「没有变更可显示」不是客户端错误。
	diff, err := ReadRelatedFileDiff(context.Background(), root, head, "note.txt")
	if err != nil {
		t.Fatalf("ReadRelatedFileDiff should not fail when nothing changed: %v", err)
	}
	if diff.Source != "none" {
		t.Fatalf("Source = %q, want none", diff.Source)
	}
	if diff.Content != "" || diff.Additions != 0 || diff.Deletions != 0 {
		t.Fatalf("expected an empty diff, got %+v", diff)
	}
}

func TestReadRelatedFileDiffUsesWorktreeRoot(t *testing.T) {
	root := initTestRepo(t)
	writeTestFile(t, root, "note.txt", "base\n")
	runTestGit(t, root, "add", "note.txt")
	runTestGit(t, root, "commit", "-m", "initial")
	runTestGit(t, root, "checkout", "-b", "task-1")
	base := strings.TrimSpace(runTestGit(t, root, "rev-parse", "HEAD"))
	runTestGit(t, root, "checkout", "-")

	worktreeRoot := filepath.Join(root, ".worktree", "task-1")
	runTestGit(t, root, "worktree", "add", worktreeRoot, "task-1")
	writeTestFile(t, root, "note.txt", "main-only\n")
	writeTestFile(t, worktreeRoot, "note.txt", "worktree-only\n")

	diff, err := ReadRelatedFileDiff(context.Background(), worktreeRoot, base, "note.txt")
	if err != nil {
		t.Fatalf("ReadRelatedFileDiff: %v", err)
	}
	if diff.Source != "worktree" {
		t.Fatalf("Source = %q, want worktree", diff.Source)
	}
	if !strings.Contains(diff.Content, "+worktree-only") {
		t.Fatalf("diff content does not contain worktree change:\n%s", diff.Content)
	}
	if strings.Contains(diff.Content, "main-only") {
		t.Fatalf("diff content used main worktree instead of task worktree:\n%s", diff.Content)
	}
}

func TestIsInsideWorktreeDetectsSubdirectories(t *testing.T) {
	root := initTestRepo(t)
	writeTestFile(t, root, "note.txt", "base\n")
	runTestGit(t, root, "add", "note.txt")
	runTestGit(t, root, "commit", "-m", "initial")
	runTestGit(t, root, "checkout", "-b", "task-1")
	runTestGit(t, root, "checkout", "-")

	worktreeRoot := filepath.Join(root, ".worktree", "task-1")
	runTestGit(t, root, "worktree", "add", worktreeRoot, "task-1")
	subdir := filepath.Join(worktreeRoot, "src")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}

	inside, err := IsInsideWorktree(context.Background(), subdir)
	if err != nil {
		t.Fatalf("IsInsideWorktree: %v", err)
	}
	if !inside {
		t.Fatal("IsInsideWorktree returned false for worktree subdir")
	}

	inside, err = IsInsideWorktree(context.Background(), root)
	if err != nil {
		t.Fatalf("IsInsideWorktree main root: %v", err)
	}
	if inside {
		t.Fatal("IsInsideWorktree returned true for main root")
	}
}

func TestReadRelatedFileDiffRejectsHeadOutsideCurrentHistory(t *testing.T) {
	root := initTestRepo(t)
	writeTestFile(t, root, "note.txt", "main\n")
	runTestGit(t, root, "add", "note.txt")
	runTestGit(t, root, "commit", "-m", "initial")
	mainBranch := strings.TrimSpace(runTestGit(t, root, "branch", "--show-current"))
	runTestGit(t, root, "checkout", "-b", "other")
	writeTestFile(t, root, "note.txt", "other\n")
	runTestGit(t, root, "commit", "-am", "other update")
	otherHead := strings.TrimSpace(runTestGit(t, root, "rev-parse", "HEAD"))
	runTestGit(t, root, "checkout", mainBranch)

	_, err := ReadRelatedFileDiff(context.Background(), root, otherHead, "note.txt")
	if err == nil {
		t.Fatal("ReadRelatedFileDiff succeeded for head outside current history")
	}
	if !strings.Contains(err.Error(), "记录的提交不存在或不在当前分支历史中") {
		t.Fatalf("error = %v", err)
	}
}

func initTestRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found")
	}
	root := t.TempDir()
	runTestGit(t, root, "init")
	runTestGit(t, root, "config", "user.email", "test@example.com")
	runTestGit(t, root, "config", "user.name", "Test User")
	return root
}

// assertBatchMatchesPerFile 逐字段对拍：批量结果必须与逐个调 ReadRelatedFileDiff 得到的
// status/additions/deletions 完全一致。
//
// 逐文件那条路径**报错**的情况（基线提交不在历史里、路径不在区间里）批量侧留空 Status ——
// 观感一致：前端两边都不显示 +N −M 徽标。所以期望值在报错时取空。
func assertBatchMatchesPerFile(t *testing.T, root string, targets []RelatedFileStatTarget) {
	t.Helper()
	batch, err := ReadRelatedFileStats(context.Background(), root, targets)
	if err != nil {
		t.Fatalf("ReadRelatedFileStats: %v", err)
	}
	if len(batch) != len(targets) {
		t.Fatalf("results = %d, want %d", len(batch), len(targets))
	}
	for i, target := range targets {
		diff, err := ReadRelatedFileDiff(context.Background(), root, target.BaseHead, target.Path)
		var wantStatus string
		var wantAdditions, wantDeletions int
		if err == nil {
			wantStatus = diff.Status
			wantAdditions = diff.Additions
			wantDeletions = diff.Deletions
		}
		got := batch[i]
		if got.Path != target.Path {
			t.Errorf("[%d] Path = %q, want %q", i, got.Path, target.Path)
		}
		if got.Status != wantStatus || got.Additions != wantAdditions || got.Deletions != wantDeletions {
			t.Errorf("[%d] %s (base=%q): batch = {%q %d %d}, per-file = {%q %d %d}",
				i, target.Path, target.BaseHead,
				got.Status, got.Additions, got.Deletions,
				wantStatus, wantAdditions, wantDeletions)
		}
	}
}

// TestReadRelatedFileStatsMatchesPerFileAcrossSources 覆盖三种源与三条空结果路径：
// commit_range（改动/删除）、worktree（无基线的未跟踪、无基线的删除）、
// 以及留空（自基线无变化、基线非法、区间里没有该路径）。
func TestReadRelatedFileStatsMatchesPerFileAcrossSources(t *testing.T) {
	root := initTestRepo(t)
	writeTestFile(t, root, "committed.txt", "one\n")
	writeTestFile(t, root, "clean.txt", "steady\n")
	writeTestFile(t, root, "gone.txt", "remove me\n")
	runTestGit(t, root, "add", ".")
	runTestGit(t, root, "commit", "-m", "initial")
	base := strings.TrimSpace(runTestGit(t, root, "rev-parse", "HEAD"))

	writeTestFile(t, root, "committed.txt", "one\ntwo\n")
	runTestGit(t, root, "rm", "gone.txt")
	runTestGit(t, root, "add", ".")
	runTestGit(t, root, "commit", "-m", "update committed, drop gone")

	// 之后再叠一层未提交的工作区改动：commit_range 的统计不该被它影响。
	writeTestFile(t, root, "dirty.txt", "first\nsecond\n")
	writeTestFile(t, root, "committed.txt", "one\ntwo\nthree\n")

	targets := []RelatedFileStatTarget{
		{Path: "committed.txt", BaseHead: base},                    // commit_range：M
		{Path: "gone.txt", BaseHead: base},                         // commit_range：D
		{Path: "clean.txt", BaseHead: base},                        // 自基线无变化 → 留空
		{Path: "dirty.txt"},                                        // 无基线 → worktree，未跟踪
		{Path: "gone.txt"},                                         // 无基线 → worktree，已删除
		{Path: "committed.txt", BaseHead: strings.Repeat("0", 40)}, // 基线非法 → 留空
		{Path: "nothing-here.txt", BaseHead: base},                 // 区间里没有 → 回落 worktree → 留空
	}
	assertBatchMatchesPerFile(t, root, targets)
}

// TestReadRelatedFileStatsMatchesPerFileInSubdirectory 子目录前缀（repoContext.prefix）
// 是 toRepoPath/fromRepoPath 唯一会改写路径的地方，单独钉一遍。
func TestReadRelatedFileStatsMatchesPerFileInSubdirectory(t *testing.T) {
	root := initTestRepo(t)
	sub := filepath.Join(root, "pkg")
	writeTestFile(t, root, "pkg/inner.txt", "one\n")
	runTestGit(t, root, "add", ".")
	runTestGit(t, root, "commit", "-m", "initial")
	base := strings.TrimSpace(runTestGit(t, root, "rev-parse", "HEAD"))

	writeTestFile(t, root, "pkg/inner.txt", "one\ntwo\n")
	runTestGit(t, root, "add", ".")
	runTestGit(t, root, "commit", "-m", "update inner")

	targets := []RelatedFileStatTarget{
		{Path: "inner.txt", BaseHead: base},
		{Path: "inner.txt"},
	}
	if batch, err := ReadRelatedFileStats(context.Background(), sub, targets); err != nil {
		t.Fatalf("ReadRelatedFileStats: %v", err)
	} else if len(batch) != 2 {
		t.Fatalf("results = %d, want 2", len(batch))
	}
	assertBatchMatchesPerFile(t, sub, targets)
}

func TestReadRelatedFileStatsNonRepoRootErrors(t *testing.T) {
	dir := t.TempDir()
	if _, err := loadRepoContext(context.Background(), dir); err == nil {
		t.Skip("temp dir resolves inside a git repo")
	}
	if _, err := ReadRelatedFileStats(context.Background(), dir, []RelatedFileStatTarget{{Path: "a.txt"}}); err == nil {
		t.Fatal("非 git 根应当报错，好让上层整组留空")
	}
}

func TestReadRelatedFileStatsEmptyTargets(t *testing.T) {
	root := initTestRepo(t)
	stats, err := ReadRelatedFileStats(context.Background(), root, nil)
	if err != nil {
		t.Fatalf("ReadRelatedFileStats: %v", err)
	}
	if len(stats) != 0 {
		t.Fatalf("results = %+v, want empty", stats)
	}
}

// TestReadRelatedFileStatsUsesFewerGitCallsThanPerFile 是本改动存在的**理由本身**：
// 数一数 git 子进程。批量一次要明显少于逐文件 N 次，否则这层抽象白加。
//
// 做法：往 PATH 前面塞一个 git 垫片，它每次被调用就往 $GIT_CALL_LOG 追加一个字节，
// 然后 exec 真正的 git。计数即子进程调用次数。
func TestReadRelatedFileStatsUsesFewerGitCallsThanPerFile(t *testing.T) {
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not found")
	}
	root := initTestRepo(t)

	const fileCount = 8
	paths := make([]string, 0, fileCount)
	for i := 0; i < fileCount; i++ {
		name := fmt.Sprintf("file-%d.txt", i)
		writeTestFile(t, root, name, "base\n")
		paths = append(paths, name)
	}
	runTestGit(t, root, "add", ".")
	runTestGit(t, root, "commit", "-m", "initial")
	base := strings.TrimSpace(runTestGit(t, root, "rev-parse", "HEAD"))
	for _, name := range paths {
		writeTestFile(t, root, name, "base\nmore\n")
	}
	runTestGit(t, root, "add", ".")
	runTestGit(t, root, "commit", "-m", "touch all")

	shimDir := t.TempDir()
	callLog := filepath.Join(shimDir, "calls.log")
	shim := "#!/bin/sh\nprintf 'x' >> \"$GIT_CALL_LOG\"\nexec \"" + realGit + "\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(shimDir, "git"), []byte(shim), 0o755); err != nil {
		t.Fatalf("write shim: %v", err)
	}
	t.Setenv("GIT_CALL_LOG", callLog)
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	countCalls := func() int {
		data, err := os.ReadFile(callLog)
		if err != nil {
			return 0
		}
		return len(data)
	}
	resetCalls := func() {
		if err := os.WriteFile(callLog, nil, 0o644); err != nil {
			t.Fatalf("reset call log: %v", err)
		}
	}

	targets := make([]RelatedFileStatTarget, 0, fileCount)
	for _, name := range paths {
		targets = append(targets, RelatedFileStatTarget{Path: name, BaseHead: base})
	}

	resetCalls()
	if _, err := ReadRelatedFileStats(context.Background(), root, targets); err != nil {
		t.Fatalf("ReadRelatedFileStats: %v", err)
	}
	batchCalls := countCalls()

	resetCalls()
	for _, target := range targets {
		if _, err := ReadRelatedFileDiff(context.Background(), root, target.BaseHead, target.Path); err != nil {
			t.Fatalf("ReadRelatedFileDiff(%s): %v", target.Path, err)
		}
	}
	perFileCalls := countCalls()

	if batchCalls == 0 {
		t.Fatal("垫片没有计数，测试本身失效了")
	}
	if batchCalls >= perFileCalls {
		t.Fatalf("批量 git 调用 = %d，逐文件 = %d；批量必须更少（这是这层抽象存在的理由）",
			batchCalls, perFileCalls)
	}
	t.Logf("%d 个文件：批量 %d 次 git 调用，逐文件 %d 次（降为 %.0f%%）",
		fileCount, batchCalls, perFileCalls, 100*float64(batchCalls)/float64(perFileCalls))
}

func writeTestFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func runTestGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out)
}
