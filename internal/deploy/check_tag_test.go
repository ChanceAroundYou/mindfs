package deploy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// check-tag.sh 的五条护栏各自造一个失败态真跑一遍。这个脚本守着「发布锚点」：
// tag 打错就改不掉（改了等于让已装的机器认不出新旧），所以捷径（只测 happy path）
// 在这里最贵 —— 五条里任何一条退回「不检查」都必须被看见。

type tagFixture struct {
	t    *testing.T
	root string
}

func newTagFixture(t *testing.T) *tagFixture {
	t.Helper()
	root := t.TempDir()
	f := &tagFixture{t: t, root: root}
	f.git("init", "-q", "-b", "main")
	f.git("config", "user.email", "t@example.com")
	f.git("config", "user.name", "t")
	f.write("release-notes.md", "# MindFS v1.2.3 — 测试\n\n正文\n")
	f.write("a.txt", "x\n")
	f.commit("init")
	return f
}

func (f *tagFixture) git(args ...string) string {
	f.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", f.root}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func (f *tagFixture) write(rel, body string) {
	f.t.Helper()
	p := filepath.Join(f.root, rel)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *tagFixture) commit(msg string) {
	f.git("add", "-A")
	f.git("commit", "-q", "-m", msg)
}

// run 跑脚本，返回 (输出, 是否失败)。
func (f *tagFixture) run(tag string) (string, bool) {
	f.t.Helper()
	script, err := filepath.Abs(filepath.Join("..", "..", "scripts", "check-tag.sh"))
	if err != nil {
		f.t.Fatal(err)
	}
	cmd := exec.Command("bash", script, tag)
	cmd.Dir = f.root
	cmd.Env = append(os.Environ(), "MINDFS_ROOT="+f.root)
	out, err := cmd.CombinedOutput()
	return string(out), err != nil
}

func TestCheckTagAcceptsCleanRelease(t *testing.T) {
	f := newTagFixture(t)
	out, failed := f.run("v1.2.3")
	if failed {
		t.Fatalf("干净仓库的合法 tag 应通过：\n%s", out)
	}
	if !strings.Contains(out, "通过") {
		t.Fatalf("应打印通过信息：\n%s", out)
	}
}

func TestCheckTagRejectsMalformedShape(t *testing.T) {
	f := newTagFixture(t)
	// 只断言「失败」不够：去掉形状检查后，这些 tag 会改由发布说明不一致而失败，
	// 测试照样绿。必须断言失败**原因**是形状。
	for _, bad := range []string{"1.2.3", "v1.2", "v1.2.3.4", "v1.2.3 8", "vX.Y.Z", "v1.2.3-"} {
		out, failed := f.run(bad)
		if !failed {
			t.Fatalf("形状 '%s' 应被拒绝，实际通过：\n%s", bad, out)
		}
		if !strings.Contains(out, "形状") {
			t.Fatalf("'%s' 应因形状不符被拒，实际原因不对：\n%s", bad, out)
		}
	}
	// 带构建号尾巴的是本仓库的真实形状（v0.5.5-1300）：形状该过，只因发布说明
	// 首行是 v1.2.3 不一致而失败。
	out, failed := f.run("v1.2.3-1300")
	if !failed {
		t.Fatalf("v1.2.3-1300 应因发布说明不一致而失败：\n%s", out)
	}
	if strings.Contains(out, "形状") || !strings.Contains(out, "不一致") {
		t.Fatalf("v1.2.3-1300 的形状是合法的，失败原因应只有发布说明不一致：\n%s", out)
	}
}

func TestCheckTagRejectsDirtyWorktree(t *testing.T) {
	f := newTagFixture(t)
	f.write("a.txt", "changed\n")
	out, failed := f.run("v1.2.3")
	if !failed {
		t.Fatalf("脏工作区应被拒绝：\n%s", out)
	}
	if !strings.Contains(out, "工作区不干净") {
		t.Fatalf("应说明是工作区的问题：\n%s", out)
	}
}

func TestCheckTagRejectsExistingTag(t *testing.T) {
	f := newTagFixture(t)
	f.git("tag", "v1.2.3")
	out, failed := f.run("v1.2.3")
	if !failed {
		t.Fatalf("重复 tag 应被拒绝：\n%s", out)
	}
	if !strings.Contains(out, "已存在") {
		t.Fatalf("应说明 tag 已存在：\n%s", out)
	}
}

func TestCheckTagRejectsNotesVersionMismatch(t *testing.T) {
	f := newTagFixture(t)
	out, failed := f.run("v9.9.9")
	if !failed {
		t.Fatalf("发布说明首行与 TAG 不一致应被拒绝：\n%s", out)
	}
	if !strings.Contains(out, "不一致") {
		t.Fatalf("应说明版本不一致：\n%s", out)
	}
}

func TestCheckTagRejectsMergeInProgress(t *testing.T) {
	f := newTagFixture(t)
	gitDir := strings.TrimSpace(f.git("rev-parse", "--git-dir"))
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(f.root, gitDir)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "MERGE_HEAD"), []byte(strings.Repeat("0", 40)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, failed := f.run("v1.2.3")
	if !failed {
		t.Fatalf("半截合并应被拒绝：\n%s", out)
	}
	if !strings.Contains(out, "半截合并") {
		t.Fatalf("应说明是半截合并：\n%s", out)
	}
}

// TestMakeTagDelegatesToScript 钉住「五条护栏只有一份实现」。
// Makefile 曾内联同一套校验，与脚本各改各的就会漂移 —— 而这条路的失败模式是
// 「校验看起来跑过了」。这里只要求 Makefile 的 tag 目标调用脚本，不复制它。
func TestMakeTagDelegatesToScript(t *testing.T) {
	mk := readRepoFile(t, "Makefile")
	start := strings.Index(mk, "\ntag:\n")
	if start < 0 {
		t.Fatal("Makefile 里找不到 tag 目标")
	}
	body := mk[start:]
	if end := strings.Index(body, "\n\n"); end >= 0 {
		body = body[:end]
	}
	if !strings.Contains(body, "scripts/check-tag.sh") {
		t.Fatalf("tag 目标必须委托 scripts/check-tag.sh：\n%s", body)
	}
	for _, dup := range []string{"git status --porcelain", "MERGE_HEAD", "refs/tags", "grep -Eq '^v[0-9]"} {
		if strings.Contains(body, dup) {
			t.Fatalf("tag 目标里出现了护栏的重复实现（%q），应只在脚本里有一份：\n%s", dup, body)
		}
	}
}
