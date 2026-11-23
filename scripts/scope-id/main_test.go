package main

import (
	"os"
	"path/filepath"
	"strings"
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
