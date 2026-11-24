package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"mindfs/scripts/internal/scopeid"
)

// claim 写进计划头部的那一行动作：没有就插在标题后，有就替换 —— 重复 claim 不该
// 在文档里堆出一串互相矛盾的声明。
func TestStampPlanInsertsThenReplaces(t *testing.T) {
	dir := t.TempDir()
	plan := filepath.Join(dir, "p.md")
	if err := os.WriteFile(plan, []byte("# 计划标题\n\n正文第一行\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := stampPlan(plan, []string{"G-QD", "G-QE"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(plan)
	lines := strings.Split(string(data), "\n")
	if !strings.HasPrefix(lines[0], "# 计划标题") {
		t.Fatalf("标题应保持在首行，实际 %q", lines[0])
	}
	if !strings.HasPrefix(lines[2], claimStampPrefix+"G-QD / G-QE") {
		t.Fatalf("声明行应落在标题后的空行之后，实际第 3 行 %q", lines[2])
	}

	// 第二次 claim 同一份计划：替换，不追加第二行。
	if err := stampPlan(plan, []string{"G-QF"}); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(plan)
	if n := strings.Count(string(data), claimStampPrefix); n != 1 {
		t.Fatalf("重复 claim 后应只有 1 行声明，实际 %d", n)
	}
	if !strings.Contains(string(data), claimStampPrefix+"G-QF") {
		t.Fatalf("第二次 claim 应覆盖旧声明：\n%s", data)
	}
}

// 清单只做末尾追加：占位组必须能被 YAML 解析回来，否则断言 1/2 会把它们当成
// 未登记的定制去报，或者干脆解析失败。
func TestAppendPlaceholdersKeepsYAMLParseable(t *testing.T) {
	dir := t.TempDir()
	reg := filepath.Join(dir, "registry.yaml")
	body := "version: 1\ngroups:\n  - id: G-QC\n    title: 已有\n"
	if err := os.WriteFile(reg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := appendPlaceholders(reg, []string{"G-QD", "G-QE"}); err != nil {
		t.Fatal(err)
	}
	ids, err := scopeid.RegistryIDs(reg)
	if err != nil {
		t.Fatalf("追加后清单应仍可解析：%v", err)
	}
	if want := []string{"G-QC", "G-QD", "G-QE"}; strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("组号 = %v；期望 %v", ids, want)
	}
	// 没有 groups: 键的文档拒绝写入 —— 宁可报错，也不要把占位塞进不相干的位置。
	bad := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(bad, []byte("version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := appendPlaceholders(bad, []string{"G-QF"}); err == nil {
		t.Fatal("缺 groups: 键时应报错")
	}
}

// 撞号裁决写清单：改号 + 在第一条被改的组前插入说明，说明里两边 sha 都要在。
func TestRenumberRegistryRenumbersAndNotes(t *testing.T) {
	dir := t.TempDir()
	reg := filepath.Join(dir, "registry.yaml")
	body := "groups:\n  - id: G-QC\n    title: 先来的\n  - id: G-QA\n    title: 撞号的\n"
	if err := os.WriteFile(reg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	note := collisionNote(
		[]string{"G-QA"}, map[string]string{"G-QA": "G-QD"},
		map[string]string{"G-QA": "1111111111111"}, "main", map[string]string{"G-QA": "2222222222222"}, "2026-11-23")
	if err := renumberRegistry(reg, map[string]string{"G-QA": "G-QD"}, note); err != nil {
		t.Fatal(err)
	}
	ids, err := scopeid.RegistryIDs(reg)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"G-QC", "G-QD"}; strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("改号后 = %v；期望 %v", ids, want)
	}
	data, _ := os.ReadFile(reg)
	for _, want := range []string{"# 撞号说明", "G-QA", "G-QD", "G-QA：HEAD 111111111111", "main 222222222222"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("撞号说明缺少 %q：\n%s", want, data)
		}
	}
	// 找不到要改的号时要报错，不能静默通过。
	if err := renumberRegistry(reg, map[string]string{"G-ZZ": "G-QE"}, ""); err == nil {
		t.Fatal("清单里没有该号时应报错")
	}
}

// 计划正文只改机器拥有的头部声明；正文里提到旧号是历史叙述，擅改会抹掉事故记录。
func TestRestampPlansOnlyTouchesScopeIDsLine(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, scopeid.PlansDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "# P\n\n" + claimStampPrefix + "G-QA / G-QB（claimed 2026-11-23T10:00Z）\n\n" +
		"历史：本轮会撞上 G-QA，裁决后改用别的号。\n"
	plan := filepath.Join(dir, "p.md")
	if err := os.WriteFile(plan, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := restampPlans(root, map[string]string{"G-QA": "G-QD"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(plan)
	if !strings.Contains(string(data), claimStampPrefix+"G-QD / G-QB") {
		t.Fatalf("头部声明应改号：\n%s", data)
	}
	if !strings.Contains(string(data), "本轮会撞上 G-QA") {
		t.Fatalf("正文的历史叙述不该被改：\n%s", data)
	}
}

// 用法文本写的是 `claim <plan.md> [--count N]`（位置参数在前），而 Go 的 flag 包
// 遇到第一个非 flag 参数就停止解析。不重排的话 `--count` 会被当成第二个位置参数，
// 命令直接报「用法」错 —— 文档写着能跑、实际跑不了。
func TestSplitFlagArgsHandlesPositionalBeforeFlags(t *testing.T) {
	flags, pos, err := splitFlagArgs([]string{"docs/p.md", "--count", "2", "--root", "/tmp/x"})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(pos, " "); got != "docs/p.md" {
		t.Fatalf("位置参数应只有计划路径，实际 %q", got)
	}
	if got := strings.Join(flags, " "); got != "--count 2 --root /tmp/x" {
		t.Fatalf("flag 与取值顺序应原样保留，实际 %q", got)
	}

	// `--flag=value` 形态不能被拆开。
	flags, pos, err = splitFlagArgs([]string{"--count=3", "docs/p.md"})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(flags, " "); got != "--count=3" {
		t.Fatalf("= 形态应整条保留，实际 %q", got)
	}
	if got := strings.Join(pos, " "); got != "docs/p.md" {
		t.Fatalf("位置参数应正确分离，实际 %q", got)
	}
}

// runClaim 必须真的接受「位置参数在前」的写法 —— 上面的纯函数测的是拆分，
// 这里测的是它接到了 runClaim 上。
func TestRunClaimAcceptsPlanBeforeCount(t *testing.T) {
	dir := t.TempDir()
	plan := filepath.Join(dir, "p.md")
	if err := os.WriteFile(plan, []byte("# 计划\n\n正文\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	registry := filepath.Join(dir, "reg.yaml")
	if err := os.WriteFile(registry, []byte("version: 1\nbaseline:\n  upstream_ref: upstream/main\ngroups: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 用不存在的 remote：取号会退到空基准，但命令本身必须走到「已申请」那一步，
	// 而不是在参数校验处就报「用法」。
	err := runClaim([]string{plan, "--count", "2", "--root", dir, "--registry", registry})
	if err != nil {
		t.Fatalf("位置参数在前的写法应被接受，实际报错：%v", err)
	}
	data, _ := os.ReadFile(registry)
	if n := strings.Count(string(data), "id: G-"); n != 2 {
		t.Fatalf("应追加 2 个占位组，实际 %d：\n%s", n, data)
	}
}

// 核心回归：在同一个仓库里并发 claim，任意两个 claim 都不能拿到同一个号。
//
// 旧实现的 allocate() 只读清单、读-算-写无锁，两个 goroutine 必然可能读到同一份
// stale 清单而拿到同号。现在每号先在共享目录里 O_EXCL 占住，抢不到就顺延。
func TestConcurrentClaimsNeverCollide(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	runGit(t, base, "git", "init", "-q", repo)
	runGit(t, repo, "git", "config", "user.email", "t@t")
	runGit(t, repo, "git", "config", "user.name", "t")
	// 两份独立清单 + 计划，各自由不同 goroutine 写 —— 模拟不同 worktree 各写各的。
	// 但它们共享同一份预留目录（这里都指向同一 repo），这正是原子性要覆盖的场景。
	if err := os.WriteFile(filepath.Join(repo, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "git", "add", "f")
	runGit(t, repo, "git", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "init")

	const workers = 8
	regs := make([]string, workers)
	plans := make([]string, workers)
	for i := 0; i < workers; i++ {
		regs[i] = filepath.Join(repo, "reg"+string(rune('a'+i))+".yaml")
		plans[i] = filepath.Join(repo, "plan"+string(rune('a'+i))+".md")
		if err := os.WriteFile(regs[i], []byte("version: 1\ngroups: []\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(plans[i], []byte("# 计划\n\n正文\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	errs := make([]error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = runClaim([]string{plans[i], "--count", "1", "--root", repo, "--registry", regs[i], "--remote", ""})
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("worker %d claim 失败：%v", i, err)
		}
	}

	// 所有 plan 头部写的号必须两两不同。
	seen := map[string]int{}
	for i := 0; i < workers; i++ {
		data, _ := os.ReadFile(plans[i])
		line := ""
		for _, l := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(l, claimStampPrefix) {
				line = l
				break
			}
		}
		if line == "" {
			t.Fatalf("worker %d 的 plan 没有 Scope IDs 头", i)
		}
		for _, id := range scopeid.TokenRE.FindAllString(line, -1) {
			seen[id]++
		}
	}
	for id, n := range seen {
		if n > 1 {
			t.Fatalf("组号 %s 被 %d 个并发 claim 拿到（撞号未修复）", id, n)
		}
	}
	if len(seen) != workers {
		t.Fatalf("应有 %d 个不同的号，实际 %d：%v", workers, len(seen), seen)
	}
}

func runGit(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v 失败：%v\n%s", name, args, err, out)
	}
}
