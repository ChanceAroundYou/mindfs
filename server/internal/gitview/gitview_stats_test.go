package gitview

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
