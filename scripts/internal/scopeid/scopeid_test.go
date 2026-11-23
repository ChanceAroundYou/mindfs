package scopeid

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
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
