package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 断言 6：同一组号出现两次必须被点出来。2026-11-23 三个并行分支各自取「最大号 + 1」，
// 拿到同一批号，合并时既无冲突、旧门禁也不红 —— 这条用例钉住「撞号必须红」。
func TestDuplicateGroupIDsReportsEachIDOnce(t *testing.T) {
	groups := []group{{ID: "G-QA"}, {ID: "G-QB"}, {ID: "G-QA"}, {ID: "G-QA"}, {ID: "G-QC"}}
	got := duplicateGroupIDs(groups)
	if len(got) != 1 {
		t.Fatalf("重复组号应报 1 条，实际 %v", got)
	}
	if !strings.Contains(got[0], "G-QA") {
		t.Fatalf("应点名 G-QA，实际 %q", got[0])
	}
	if n := duplicateGroupIDs([]group{{ID: "G-QA"}, {ID: "G-QB"}}); len(n) != 0 {
		t.Fatalf("无重复时应为空，实际 %v", n)
	}
}

// 断言 7：计划正文里写下的 Scope 组号必须在清单里存在。
func TestUnresolvedPlanScopes(t *testing.T) {
	root := t.TempDir()
	plans := filepath.Join(root, "docs/superpowers/plans")
	if err := os.MkdirAll(plans, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(plans, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.md", "# A\n\nScope: G-QA / G-QZ（第二项没登记）\n")
	// 非 Scope 行提到别的组号不算本计划的承诺，不该报。
	write("b.md", "# B\n\n历史记录：早先的 G-QY 已并入 G-QB。\n\nScope: G-QA\n")

	unresolved, err := unresolvedPlanScopes(root, []group{{ID: "G-QA"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(unresolved) != 1 {
		t.Fatalf("应只有 1 条未登记，实际 %v", unresolved)
	}
	if !strings.Contains(unresolved[0], "a.md:3") || !strings.Contains(unresolved[0], "G-QZ") {
		t.Fatalf("应定位到 a.md:3 的 G-QZ，实际 %q", unresolved[0])
	}
}

// 占位符形状（G-XX / G-XXX）是文档在介绍本约定时的模板写法，不是任何一组的承诺。
// 报它会在每次写机制文档时制造假红（2026-11-23 写本机制的计划时当场撞上），
// 而真正的取号路径是 `scope-id claim` 写进计划头部的 `> Scope IDs:` 行 —— 那是真号。
func TestUnresolvedPlanScopesIgnoresDocumentationPlaceholders(t *testing.T) {
	root := t.TempDir()
	plans := filepath.Join(root, "docs/superpowers/plans")
	if err := os.MkdirAll(plans, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "# 约定\n\n提交信息标 `Scope: G-XX`，一组定制一个号。\n\n" +
		"旧写法是 Scope: G-XXX。\n\n> Scope IDs: G-QA\n"
	if err := os.WriteFile(filepath.Join(plans, "c.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	unresolved, err := unresolvedPlanScopes(root, []group{{ID: "G-QA"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(unresolved) != 0 {
		t.Fatalf("模板占位不该被当成承诺，实际 %v", unresolved)
	}
	// 同一份文档里写下的真号没登记时照报 —— 跳过占位不等于放过真号。
	unresolved, err = unresolvedPlanScopes(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(unresolved) != 1 || !strings.Contains(unresolved[0], "G-QA") {
		t.Fatalf("真号 G-QA 未登记时应报 1 条，实际 %v", unresolved)
	}
}
