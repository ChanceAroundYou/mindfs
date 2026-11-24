package scopeid

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestOrdinalRoundTrip(t *testing.T) {
	cases := []struct {
		id   string
		ord  int
		lens int
	}{
		{"G-AA", 1, 2},
		{"G-AB", 2, 2},
		{"G-AZ", 26, 2},
		{"G-BA", 27, 2},
		{"G-ZZ", 676, 2},
		{"G-AAA", 677, 3},
		{"G-PDF", 677 + 15*676 + 3*26 + 5, 3},
	}
	for _, c := range cases {
		ord, lens, ok := Ordinal(c.id)
		if !ok || ord != c.ord || lens != c.lens {
			t.Fatalf("Ordinal(%s) = %d,%d,%v；期望 %d,%d,true", c.id, ord, lens, ok, c.ord, c.lens)
		}
		back, err := Format(c.ord)
		if err != nil || back != c.id {
			t.Fatalf("Format(%d) = %q,%v；期望 %q", c.ord, back, err, c.id)
		}
	}
}

func TestOrdinalRejectsBadShapes(t *testing.T) {
	for _, id := range []string{"", "G-", "G-AAA A", "g-aa", "G-A1", "G-ABCD", "X-AA", "G-XXX "} {
		if _, _, ok := Ordinal(id); ok {
			t.Fatalf("%q 不该被接受", id)
		}
	}
	// 三字母号段以后继续增长
	if _, err := Format(676 + 26*26*26 + 1); err == nil {
		t.Fatal("超出 G-ZZZ 应报错")
	}
	if _, err := Format(0); err == nil {
		t.Fatal("序号 0 应报错")
	}
}

func TestNextIDsContinuesFromMaxNeverBackfillsHoles(t *testing.T) {
	got, err := NextIDs([]string{"G-AA", "G-AC", "G-AB"}, 2)
	if err != nil {
		t.Fatal(err)
	}
	// G-AB 是洞，但绝不回头补 —— 回填会让两个分支拿到同一个洞。
	if want := []string{"G-AD", "G-AE"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("NextIDs = %v；期望 %v", got, want)
	}
}

func TestLegacyThreeLetterIDsDoNotDragNewIDsIntoThem(t *testing.T) {
	// G-PDF / G-WIN / G-WSX 是历史孤岛；两字母段还有空位时就别跟着跳。
	got, err := NextIDs([]string{"G-QC", "G-PDF", "G-WIN", "G-WSX"}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"G-QD", "G-QE"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("NextIDs = %v；期望 %v", got, want)
	}
}

func TestNextIDsRollsIntoThreeLettersAfterZZ(t *testing.T) {
	got, err := NextIDs([]string{"G-ZZ"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "G-AAA" {
		t.Fatalf("NextIDs(G-ZZ) = %v；期望 [G-AAA]", got)
	}
}

func TestNextIDsSkipsAlreadyUsedNumbers(t *testing.T) {
	// 两字母用尽后进入三字母段；历史号（G-PDF / G-WIN / G-WSX）不许被重新发出。
	got, err := NextIDs([]string{"G-ZZ", "G-WIN", "G-WSX"}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"G-WSY", "G-WSZ"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("NextIDs = %v；期望 %v", got, want)
	}
}

func TestDuplicatesReportsEachIDOnce(t *testing.T) {
	got := Duplicates([]string{"G-QA", "G-QB", "G-QA", "G-QA"})
	if want := []string{"G-QA"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Duplicates = %v；期望 %v", got, want)
	}
	if n := Duplicates([]string{"G-QA", "G-QB"}); len(n) != 0 {
		t.Fatalf("无重复应为空，实际 %v", n)
	}
}

func TestScanPlanScopesOnlyReadsScopeLines(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, PlansDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "# A\n\n历史：G-QY 已并入 G-QB。\n\nScope: G-QA / G-QZ\n"
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	refs, err := ScanPlanScopes(root)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, r := range refs {
		ids = append(ids, r.ID)
		if r.Line != 5 {
			t.Fatalf("%s 的引用应落在第 5 行，实际 %d", r.ID, r.Line)
		}
	}
	if want := []string{"G-QA", "G-QZ"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("ScanPlanScopes = %v；期望 %v", ids, want)
	}

	unresolved := Unresolved(refs, map[string]bool{"G-QA": true})
	if len(unresolved) != 1 || unresolved[0].ID != "G-QZ" {
		t.Fatalf("Unresolved = %v；期望只剩 G-QZ", unresolved)
	}
}

// 文档里的占位写法（`Scope: G-XX`、`Scope: G-XXX`）不是承诺：同一字母重复三次以上的
// 号形状只在文档模板里出现，真存在时必然已在清单里，所以扫描器直接跳过。
// 不做这一步，任何提到「Scope: G-XX 这类组号」的文档都会把门禁判红。
func TestScanPlanScopesIgnoresTemplatePlaceholders(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, PlansDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "# 模板\n\n提交信息标 `Scope: G-XX`，一组定制一个号。\n\nScope: <本组组号>\n\n" +
		"以前写的是 Scope: G-XXX 这种。\n"
	if err := os.WriteFile(filepath.Join(dir, "t.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	refs, err := ScanPlanScopes(root)
	if err != nil {
		t.Fatal(err)
	}
	// G-XX / G-XXX 是模板占位，跳过；真号（G-QA 之类）照旧收。
	if len(refs) != 0 {
		t.Fatalf("占位不该被当成承诺，实际收进 %v", refs)
	}
	if !IsTemplatePlaceholder("G-XX") || !IsTemplatePlaceholder("G-XXX") {
		t.Fatal("G-XX / G-XXX 应判为模板占位")
	}
	for _, id := range []string{"G-QA", "G-PDF", "G-A"} {
		if IsTemplatePlaceholder(id) {
			t.Fatalf("%s 是真号，不该被判成占位", id)
		}
	}
}

// 不在 git 仓库里时，预留目录退回到 <root>/.scope-ids —— 让 claim 在仓外也能跑。
func TestReservationsDirFallsBackOutsideGit(t *testing.T) {
	root := t.TempDir()
	dir, err := ReservationsDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, ".scope-ids"); dir != want {
		t.Fatalf("仓外预留目录 = %q；期望 %q", dir, want)
	}
}

// 在 git 仓库里，预留目录必须落在 common dir 下 —— 这是多个 worktree 共享同一处
// 占位的物理基础。用 git worktree 真的建一个 worktree，断言两者的预留目录相同。
func TestReservationsDirSharedAcrossWorktrees(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	run(t, base, "git", "init", "-q", repo)
	run(t, repo, "git", "config", "user.email", "t@t")
	run(t, repo, "git", "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(repo, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, repo, "git", "add", "f")
	run(t, repo, "git", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "init")

	wt := filepath.Join(base, "wt")
	run(t, repo, "git", "worktree", "add", "-q", "-b", "side", wt)

	a, err := ReservationsDir(repo)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ReservationsDir(wt)
	if err != nil {
		t.Fatal(err)
	}
	// macOS 的 /var → /private/var 之类符号链接会让字面不等，比 realpath。
	ra, _ := filepath.EvalSymlinks(a)
	rb, _ := filepath.EvalSymlinks(b)
	if ra != rb {
		t.Fatalf("两个 worktree 的预留目录应一致：\n  repo=%s\n  wt  =%s", a, b)
	}
}

// Reserve 第二次同号必须报 ErrIDTaken：这是跨 worktree 不撞号的原子性来源。
func TestReserveIsExclusive(t *testing.T) {
	root := t.TempDir()
	meta := Reservation{Branch: "b", Plan: "p.md", Worktree: root, TS: time.Now()}
	if err := Reserve(root, "G-QA", meta); err != nil {
		t.Fatal(err)
	}
	err := Reserve(root, "G-QA", meta)
	if !errors.Is(err, ErrIDTaken) {
		t.Fatalf("第二次占同号应报 ErrIDTaken，实际 %v", err)
	}
	// 空文件（崩溃残留）同样算占用。
	if err := os.WriteFile(filepath.Join(root, ".scope-ids", "G-QB"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Reserve(root, "G-QB", meta); !errors.Is(err, ErrIDTaken) {
		t.Fatalf("空文件应算已占用，实际 %v", err)
	}
}

func TestListReservationsReadsMetadataAndFlagsEmpty(t *testing.T) {
	root := t.TempDir()
	if err := Reserve(root, "G-QA", Reservation{Branch: "b1", Plan: "p1.md", TS: time.Now()}); err != nil {
		t.Fatal(err)
	}
	// 手工留一个空文件模拟崩溃残留。
	if err := os.MkdirAll(filepath.Join(root, ".scope-ids"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".scope-ids", "G-QB"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	rs, err := ListReservations(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 2 || rs[0].ID != "G-QA" || rs[1].ID != "G-QB" {
		t.Fatalf("应读到 G-QA、G-QB 两个预留，实际 %+v", rs)
	}
	if rs[1].Empty != true || rs[0].Empty != false {
		t.Fatalf("G-QB 应标 Empty，G-QA 不是：%+v", rs)
	}
	if rs[0].Branch != "b1" || rs[0].Plan != "p1.md" {
		t.Fatalf("metadata 丢失：%+v", rs[0])
	}
}

func TestGCReservationsDropsCommittedAndDeadBranches(t *testing.T) {
	root := t.TempDir()
	_ = Reserve(root, "G-QA", Reservation{Branch: "live", Plan: "p", TS: time.Now()})    // 已进清单
	_ = Reserve(root, "G-QB", Reservation{Branch: "dead", Plan: "p", TS: time.Now()})    // 分支没了
	_ = Reserve(root, "G-QC", Reservation{Branch: "live2", Plan: "p", TS: time.Now()})   // 该留
	dropped, err := GCReservations(root, GCOptions{
		Committed:    map[string]bool{"G-QA": true},
		BranchExists: func(b string) bool { return b != "dead" },
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"G-QA", "G-QB"}; !reflect.DeepEqual(dropped, want) {
		t.Fatalf("GC 应回收 %v，实际 %v", want, dropped)
	}
	rs, _ := ListReservations(root)
	if len(rs) != 1 || rs[0].ID != "G-QC" {
		t.Fatalf("GC 后应只剩 G-QC，实际 %+v", rs)
	}
}

func TestGCReservationsKeepsFreshEmptyFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".scope-ids"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".scope-ids", "G-QA"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	// 还没超过 StaleAfter：崩溃残留留着，宁可多跳号也不重发。
	dropped, err := GCReservations(root, GCOptions{StaleAfter: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if len(dropped) != 0 {
		t.Fatalf("未超龄的空文件不该回收，实际 %v", dropped)
	}
}

// run 是 git 调用的小助手；失败即 t.Fatal。
func run(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	out, err := runOut(t, dir, name, args...)
	if err != nil {
		t.Fatalf("%s %v 失败：%v\n%s", name, args, err, out)
	}
}

// runOut 同 run，但把合并冲突这类「预期失败」的输出交回调用方判断。
func runOut(t *testing.T, dir string, name string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// initMergeRepo 建一个 baseline 已含 .gitattributes（merge=union）的仓库，模拟
// 「A 阶段先把 union 推到共同祖先、再并行开分支追加组」的真实时序。
func initMergeRepo(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	run(t, base, "git", "init", "-q", repo)
	run(t, repo, "git", "config", "user.email", "t@t")
	run(t, repo, "git", "config", "user.name", "t")
	run(t, repo, "git", "config", "commit.gpgsign", "false")
	// 固定默认分支名为 main，免得依赖用户全局 init.defaultBranch。
	run(t, repo, "git", "checkout", "-q", "-b", "main")
	if err := os.MkdirAll(filepath.Join(repo, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	// baseline：已有 G-QA 一组 + 本章程属性。
	if err := os.WriteFile(filepath.Join(repo, ".gitattributes"),
		[]byte(RegistryPath+" merge=union\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := "version: 1\ngroups:\n  - id: G-QA\n    title: 基线\n"
	if err := os.WriteFile(filepath.Join(repo, RegistryPath), []byte(reg), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, repo, "git", "add", "-A")
	run(t, repo, "git", "commit", "-q", "-m", "baseline")
	return repo
}

func appendGroup(t *testing.T, repo, id string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(repo, RegistryPath), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString("  - id: " + id + "\n    title: 新组\n"); err != nil {
		t.Fatal(err)
	}
}

func checkoutBranch(t *testing.T, repo, name string, create bool) {
	t.Helper()
	if create {
		run(t, repo, "git", "checkout", "-q", "-b", name)
	} else {
		run(t, repo, "git", "checkout", "-q", name)
	}
}

// merge=union 的两条真实后果，钉住本设计对缺陷 B 的承诺：
//  1. 两分支追加不同号 → 自动合并，两边块都在（不再有 EOF 文本冲突）；
//  2. 两分支追加同一号 → 合并后出现两段相同 id，被 Duplicates 抓到（交给 resolve）。
func TestUnionMergeOfRegistryKeepsDistinctAppends(t *testing.T) {
	repo := initMergeRepo(t)

	checkoutBranch(t, repo, "a", true)
	appendGroup(t, repo, "G-QB")
	run(t, repo, "git", "commit", "-q", "-am", "a 追加 G-QB")

	checkoutBranch(t, repo, "main", false)
	checkoutBranch(t, repo, "b", true)
	appendGroup(t, repo, "G-QC")
	run(t, repo, "git", "commit", "-q", "-am", "b 追加 G-QC")

	checkoutBranch(t, repo, "main", false)
	out, err := runOut(t, repo, "git", "merge", "--no-edit", "a")
	if err != nil {
		t.Fatalf("合 a 不应冲突：%v\n%s", err, out)
	}
	out, err = runOut(t, repo, "git", "merge", "--no-edit", "b")
	if err != nil {
		t.Fatalf("合 b 不应冲突（union 应自动合并不同号的追加）：%v\n%s", err, out)
	}

	ids, err := RegistryIDs(filepath.Join(repo, RegistryPath))
	if err != nil {
		t.Fatalf("合并后清单应仍可解析：%v", err)
	}
	want := map[string]bool{"G-QA": true, "G-QB": true, "G-QC": true}
	for _, id := range ids {
		delete(want, id)
	}
	if len(want) != 0 {
		t.Fatalf("合并后应留有 G-QA/G-QB/G-QC，缺 %v（实际 %v）", want, ids)
	}
	if dups := Duplicates(ids); len(dups) != 0 {
		t.Fatalf("不同号不该产生重复，实际 %v", dups)
	}
}

func appendGroupTitle(t *testing.T, repo, id, title string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(repo, RegistryPath), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString("  - id: " + id + "\n    title: " + title + "\n"); err != nil {
		t.Fatal(err)
	}
}

func TestUnionMergeOfSameIDProducesDuplicateCaughtByGate(t *testing.T) {
	repo := initMergeRepo(t)

	// 同样是 G-QB，但两边内容不同（各写了不同的 title）—— 这是真实撞号的样子：
	// scope-id 的旧 allocate 各算各的号、内容各不相同。union 会把两段都保留，
	// 于是出现两段 `id: G-QB`，交给 check-upstream 断言 6 判红、resolve 改号。
	checkoutBranch(t, repo, "a", true)
	appendGroupTitle(t, repo, "G-QB", "甲分支的定制")
	run(t, repo, "git", "commit", "-q", "-am", "a 追加 G-QB")

	checkoutBranch(t, repo, "main", false)
	checkoutBranch(t, repo, "b", true)
	appendGroupTitle(t, repo, "G-QB", "乙分支的定制")
	run(t, repo, "git", "commit", "-q", "-am", "b 追加 G-QB")

	checkoutBranch(t, repo, "main", false)
	if _, err := runOut(t, repo, "git", "merge", "--no-edit", "a"); err != nil {
		t.Fatalf("合 a 不应冲突")
	}
	// 内容不同的同号：union 不做文本冲突，但两段都会进来。
	if _, err := runOut(t, repo, "git", "merge", "--no-edit", "b"); err != nil {
		t.Fatalf("合 b 不应有文本冲突（union 语义），实际：%v", err)
	}
	data, err := os.ReadFile(filepath.Join(repo, RegistryPath))
	if err != nil {
		t.Fatal(err)
	}
	// 同号同结构时 union 会留下两段相同的行 —— 可能表现为重复 id（可解析），
	// 也可能表现为重复的 mapping key（YAML 直接解析失败）。两种情况都是「响亮失败」：
	// check-upstream 解析清单时必非零退出，绝不会静默放过。
	ids, perr := RegistryIDs(filepath.Join(repo, RegistryPath))
	if perr != nil {
		if !strings.Contains(perr.Error(), "already defined") {
			t.Fatalf("解析失败应源于重复 key，实际：%v", perr)
		}
		return // YAML 直接报错 —— 门禁必红，符合预期。
	}
	if dups := Duplicates(ids); len(dups) == 0 {
		t.Fatalf("同号合并后必须可被检出（重复 id 或解析失败），实际 ids=%v\n清单：\n%s", ids, data)
	}
}
