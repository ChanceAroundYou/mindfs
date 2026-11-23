package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"mindfs/scripts/internal/scopeid"
)

// 撞号裁决只在真 git 历史里有意义：它靠 `git log -S` 找引入提交、靠提交时间决定
// 谁先谁后、靠跨 ref 读清单。所以这里搭一个真的双分支仓库，而不是打桩。
type repo struct {
	t   *testing.T
	dir string
}

func newRepo(t *testing.T) *repo {
	t.Helper()
	dir := t.TempDir()
	r := &repo{t: t, dir: dir}
	r.git("init", "-q", "-b", "main")
	r.git("config", "user.email", "t@example.com")
	r.git("config", "user.name", "t")
	return r
}

func (r *repo) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", r.dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_DATE="+r.stamp(), "GIT_COMMITTER_DATE="+r.stamp())
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v 失败：%v\n%s", args, err, out)
	}
	return string(out)
}

var clock int

// stamp 让每个提交都有递增的提交时间 —— 裁决方向正是按它判「谁先引入」。
func (r *repo) stamp() string {
	clock++
	return "2026-11-23T0" + string(rune('0'+clock%10)) + ":00:00Z"
}

func (r *repo) write(rel, body string) {
	r.t.Helper()
	p := filepath.Join(r.dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *repo) commitAll(msg string) {
	r.git("add", "-A")
	r.git("commit", "-q", "-m", msg)
}

const registryHead = "version: 1\nbaseline:\n  upstream_ref: upstream/main\ngroups:\n  - id: G-QB\n    title: 更早的\n"

func groupYAML(id, title string) string {
	return "  - id: " + id + "\n    title: " + title + "\n"
}

// 两个分支各自从 G-QB 之后取号，都拿到 G-QC。当前分支是后来者时应当自己改号，
// 另一边保留 —— 并留下含两边 sha 的撞号说明，计划头部跟着改，正文历史不动。
func TestResolveRenumbersWhenCurrentBranchIsTheLaterIntroducer(t *testing.T) {
	clock = 0
	r := newRepo(t)
	r.write(scopeid.RegistryPath, registryHead)
	r.write("docs/superpowers/plans/p.md", "# P\n\n正文说 G-QC 会被裁决。\n")
	r.commitAll("base")
	baseSHA := strings.TrimSpace(r.git("rev-parse", "HEAD"))

	// 另一条分支先引入 G-QC（从 base 切出 —— 与 main 真正并行）。
	r.git("checkout", "-q", "-b", "other", baseSHA)
	r.write(scopeid.RegistryPath, registryHead+groupYAML("G-QC", "另一边的修正"))
	r.commitAll("other: add G-QC")
	otherSHA := strings.TrimSpace(r.git("rev-parse", "HEAD"))

	// 回到 main，晚一步也用了 G-QC —— 这正是三次事故的形状。
	r.git("checkout", "-q", "main")
	r.write(scopeid.RegistryPath, registryHead+groupYAML("G-QC", "本分支的修正"))
	r.write("docs/superpowers/plans/p.md",
		"# P\n\n> Scope IDs: G-QC（claimed 2026-11-23T10:00Z）\n\n正文说 G-QC 会被裁决。\n")
	r.commitAll("main: add G-QC")
	headSHA := strings.TrimSpace(r.git("rev-parse", "HEAD"))

	if err := runResolve([]string{"--branch", "other", "--remote", "origin/main", "--root", r.dir}); err != nil {
		t.Fatalf("resolve 应成功：%v", err)
	}

	ids, err := scopeid.RegistryIDs(filepath.Join(r.dir, scopeid.RegistryPath))
	if err != nil {
		t.Fatal(err)
	}
	// G-QC 已被换掉，新号是 G-QB 之后的下一个。
	hasQC := false
	for _, id := range ids {
		if id == "G-QC" {
			hasQC = true
		}
	}
	if hasQC {
		t.Fatalf("当前分支（后来者）应改号，清单仍含 G-QC：%v", ids)
	}
	plan, _ := os.ReadFile(filepath.Join(r.dir, "docs/superpowers/plans/p.md"))
	body := string(plan)
	if strings.Contains(body, claimStampPrefix+"G-QC") {
		t.Fatalf("计划头部声明应跟着改号：\n%s", body)
	}
	if !strings.Contains(body, "正文说 G-QC 会被裁决") {
		t.Fatalf("正文的历史叙述不该被改：\n%s", body)
	}
	reg, _ := os.ReadFile(filepath.Join(r.dir, scopeid.RegistryPath))
	text := string(reg)
	if !strings.Contains(text, "# 撞号说明") {
		t.Fatalf("应写入撞号说明：\n%s", text)
	}
	if !strings.Contains(text, headSHA[:12]) || !strings.Contains(text, otherSHA[:12]) {
		t.Fatalf("撞号说明应含两边引入提交 %s / %s：\n%s", headSHA[:12], otherSHA[:12], text)
	}
}

// 反过来站：当前分支是先引入者时拒绝改号，并指名另一条分支 —— 否则会把先来者
// 的登记挪走，本该由后来者让号的那一边反而占了新号。
func TestResolveRefusesWhenCurrentBranchIntroducedFirst(t *testing.T) {
	clock = 0
	r := newRepo(t)
	r.write(scopeid.RegistryPath, registryHead)
	r.commitAll("base")
	baseSHA := strings.TrimSpace(r.git("rev-parse", "HEAD"))

	r.write(scopeid.RegistryPath, registryHead+groupYAML("G-QC", "我先引入"))
	r.commitAll("main: add G-QC first")

	// 关键：later 分支从 base 切出，才是真正的并行 —— 从 main 的新提交切出的话，
	// 共同祖先里已经有 G-QC，本来就不算撞号。
	r.git("checkout", "-q", "-b", "other", baseSHA)
	r.write(scopeid.RegistryPath, registryHead+groupYAML("G-QC", "后来者"))
	r.commitAll("other: add G-QC later")
	r.git("checkout", "-q", "main")

	err := runResolve([]string{"--branch", "other", "--remote", "origin/main", "--root", r.dir})
	if err == nil {
		t.Fatal("当前分支先引入时应拒绝改号")
	}
	if !strings.Contains(err.Error(), "other") {
		t.Fatalf("拒绝信息应指名另一条分支，实际：%v", err)
	}
	ids, _ := scopeid.RegistryIDs(filepath.Join(r.dir, scopeid.RegistryPath))
	if len(ids) != 2 || ids[1] != "G-QC" {
		t.Fatalf("拒绝时不得改动清单，实际：%v", ids)
	}
}

// --dry-run 只打印，一个字节都不写。
func TestResolveDryRunWritesNothing(t *testing.T) {
	clock = 0
	r := newRepo(t)
	r.write(scopeid.RegistryPath, registryHead)
	r.commitAll("base")
	baseSHA := strings.TrimSpace(r.git("rev-parse", "HEAD"))
	r.git("checkout", "-q", "-b", "other", baseSHA)
	r.write(scopeid.RegistryPath, registryHead+groupYAML("G-QC", "另一边"))
	r.commitAll("other: G-QC")
	r.git("checkout", "-q", "main")
	r.write(scopeid.RegistryPath, registryHead+groupYAML("G-QC", "本分支"))
	r.commitAll("main: G-QC")

	before, _ := os.ReadFile(filepath.Join(r.dir, scopeid.RegistryPath))
	if err := runResolve([]string{"--branch", "other", "--dry-run", "--root", r.dir}); err != nil {
		t.Fatalf("dry-run 应成功：%v", err)
	}
	after, _ := os.ReadFile(filepath.Join(r.dir, scopeid.RegistryPath))
	if string(before) != string(after) {
		t.Fatal("--dry-run 不得改动清单")
	}
}
