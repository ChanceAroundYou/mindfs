// scope-id 是 `Scope: G-XX` 组号的分配与撞号裁决工具。
//
// 为什么需要它：组号是手写字符串，两个并行分支各自从本地清单取「最大号 + 1」就会拿到
// 同一批号；合并时 git 不冲突、旧门禁也不报，只有人事后读 git log 才会发现同一个号
// 指着两组互不相关的定制（2026-11-23 真实发生：G-PW / G-PX / G-PY 三组撞车）。
//
// 这个工具把取号、声明、裁决三件事变成命令：
//
//	scope-id next [--count N]              打印接下来 N 个空号（读 origin/main，绝不回填空号）
//	scope-id claim <plan.md> [--count N]   申请 N 个号 + 在清单里占位 + 写进计划头部
//	scope-id check                         门禁：清单内组号唯一、计划里的号都已登记
//	scope-id resolve --branch <name>       两分支撞号时裁决：先引入者保留，后来者改号
//
// 设计约束（与 docs/upstream-customizations.yaml 里既有撞号说明一致）：
//   - 提交信息里的历史 `Scope:` 一律不改写，裁决只改清单（历史是事实，不是索引）；
//   - 号只增不减、不回填空洞 —— 回填会让两个分支更容易拿到同一个洞；
//   - 分配看 origin/main 与本地清单的并集，本地已提交的号也算占位（重跑不会重复发号）。
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"mindfs/scripts/internal/scopeid"
)

// claimStampPrefix 写在计划头部的声明行前缀。它是机器拥有的字段（claim/resolve 会改），
// 与正文里的人话分开。
const claimStampPrefix = "> Scope IDs: "

// placeholderTitle 是 claim 写进清单的占位标题。断言 3/5（测试存在、至少一条测试）
// 会因此变红 —— 这是刻意的：占位必须被填成真条目，否则该组等于没有保护。
const placeholderTitle = "WIP — 待填写（scope-id claim 占位）"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]
	var err error
	switch cmd {
	case "next":
		err = runNext(args)
	case "claim":
		err = runClaim(args)
	case "check":
		err = runCheck(args)
	case "resolve":
		err = runResolve(args)
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "未知子命令 %q\n\n", cmd)
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `scope-id —— Scope 组号的分配与撞号裁决

  scope-id next [--count N]              打印接下来 N 个空号
  scope-id claim <plan.md> [--count N]   申请 N 个号：清单里占位 + 写进计划头部
  scope-id check                         清单内组号唯一、计划里的号都已登记
  scope-id resolve --branch <name>       撞号裁决：先引入者保留，当前分支改号

全局参数：
  --root <dir>      仓库根（默认当前目录的 git 根）
  --remote <ref>    取号依据 / 撞号基准（默认 origin/main）
  --registry <path> 清单路径（默认 docs/upstream-customizations.yaml）

取号规则：两字母段取「最大号 + 1」，绝不回填空洞；G-ZZ 之后进入三字母段。
`)
}

func runNext(args []string) error {
	fs := flag.NewFlagSet("next", flag.ContinueOnError)
	count := fs.Int("count", 1, "要申请几个号")
	root := fs.String("root", "", "仓库根")
	remote := fs.String("remote", "origin/main", "取号依据的 git 引用")
	registry := fs.String("registry", "", "清单路径")
	if err := fs.Parse(args); err != nil {
		return err
	}
	r, reg, err := resolvePaths(*root, *registry)
	if err != nil {
		return err
	}
	ids, err := allocate(r, reg, *remote, *count)
	if err != nil {
		return err
	}
	fmt.Println(strings.Join(ids, " "))
	return nil
}

func runClaim(args []string) error {
	fs := flag.NewFlagSet("claim", flag.ContinueOnError)
	count := fs.Int("count", 1, "要申请几个号")
	root := fs.String("root", "", "仓库根")
	remote := fs.String("remote", "origin/main", "取号依据的 git 引用")
	registry := fs.String("registry", "", "清单路径")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return fmt.Errorf("用法：scope-id claim <plan.md> [--count N]")
	}
	r, reg, err := resolvePaths(*root, *registry)
	if err != nil {
		return err
	}
	plan := rest[0]
	if !filepath.IsAbs(plan) {
		plan = filepath.Join(r, plan)
	}
	if _, err := os.Stat(plan); err != nil {
		return fmt.Errorf("计划文档不存在：%s", plan)
	}
	ids, err := allocate(r, reg, *remote, *count)
	if err != nil {
		return err
	}
	if err := appendPlaceholders(reg, ids); err != nil {
		return err
	}
	if err := stampPlan(plan, ids); err != nil {
		return err
	}
	rel, _ := filepath.Rel(r, plan)
	fmt.Printf("已申请 %s\n", strings.Join(ids, " / "))
	fmt.Printf("  · 清单已追加 %d 个占位组（标题「%s」）\n", len(ids), placeholderTitle)
	fmt.Printf("  · %s 头部已写入 `%s`\n", rel, claimStampPrefix+strings.Join(ids, " / "))
	fmt.Println("下一步：把占位填成真条目（title / kind / symptom / why / files / tests / anchors），")
	fmt.Println("       否则 check-upstream 的断言 3/5 会红 —— 这是刻意的。")
	return nil
}

func runCheck(args []string) error {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	root := fs.String("root", "", "仓库根")
	registry := fs.String("registry", "", "清单路径")
	if err := fs.Parse(args); err != nil {
		return err
	}
	r, reg, err := resolvePaths(*root, *registry)
	if err != nil {
		return err
	}
	ids, err := scopeid.RegistryIDs(reg)
	if err != nil {
		return err
	}
	var failures []string
	if dups := scopeid.Duplicates(ids); len(dups) > 0 {
		fmt.Printf("✗ 组号重复：%d\n", len(dups))
		for _, d := range dups {
			fmt.Printf("    %s\n", d)
		}
		failures = append(failures, fmt.Sprintf("%d 个组号重复", len(dups)))
	}
	known := make(map[string]bool, len(ids))
	for _, id := range ids {
		known[id] = true
	}
	refs, err := scopeid.ScanPlanScopes(r)
	if err != nil {
		return err
	}
	unresolved := scopeid.Unresolved(refs, known)
	if len(unresolved) > 0 {
		fmt.Printf("✗ 计划里写了、清单里没有的组号：%d\n", len(unresolved))
		for _, u := range unresolved {
			fmt.Printf("    %s:%d 引用了 %s\n", u.File, u.Line, u.ID)
		}
		failures = append(failures, fmt.Sprintf("%d 个计划组号未登记", len(unresolved)))
	}
	if len(failures) > 0 {
		return fmt.Errorf("%s", strings.Join(failures, "；"))
	}
	fmt.Printf("✓ 全部通过：%d 个组号唯一，计划引用全部已登记\n", len(ids))
	return nil
}

// resolvePaths 确定仓库根与清单绝对路径。
func resolvePaths(rootFlag, registryFlag string) (string, string, error) {
	root := rootFlag
	if root == "" {
		out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
		if err != nil {
			return "", "", fmt.Errorf("不在 git 仓库里：%w", err)
		}
		root = strings.TrimSpace(string(out))
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", "", err
	}
	reg := registryFlag
	if reg == "" {
		reg = filepath.Join(abs, scopeid.RegistryPath)
	} else if !filepath.IsAbs(reg) {
		reg = filepath.Join(abs, reg)
	}
	return abs, reg, nil
}

// allocate 推出接下来 n 个空号：取「origin/main 的清单」与「本地清单」的并集，
// 这样本地已经提交但还没推送的号也算占位，重跑不会发出同一个号。
func allocate(root, registry, remote string, n int) ([]string, error) {
	if n <= 0 {
		return nil, fmt.Errorf("--count 必须 >= 1")
	}
	seen := make(map[string]bool)
	add := func(ids []string) {
		for _, id := range ids {
			seen[id] = true
		}
	}
	local, err := scopeid.RegistryIDs(registry)
	if err != nil {
		return nil, err
	}
	add(local)
	if remote != "" {
		remoteIDs, err := scopeid.RegistryIDsAt(root, remote, scopeid.RegistryPath)
		switch {
		case err != nil:
			fmt.Fprintf(os.Stderr, "· 读不到 %s 的清单（%v），只按本地清单取号\n", remote, err)
		default:
			add(remoteIDs)
		}
	}
	all := make([]string, 0, len(seen))
	for id := range seen {
		all = append(all, id)
	}
	sort.Strings(all)
	return scopeid.NextIDs(all, n)
}

// appendPlaceholders 在清单末尾追加占位组。清单是手写 YAML、带大量注释，
// 序列化整个文档会把注释全冲掉 —— 所以只做「文件末尾追加」这一种写法。
func appendPlaceholders(registry string, ids []string) error {
	data, err := os.ReadFile(registry)
	if err != nil {
		return err
	}
	if !strings.Contains(string(data), "\ngroups:") && !strings.HasPrefix(string(data), "groups:") {
		return fmt.Errorf("%s 里找不到顶层 groups: 键", registry)
	}
	var b strings.Builder
	b.Write(data)
	if len(data) > 0 && data[len(data)-1] != '\n' {
		b.WriteByte('\n')
	}
	b.WriteString("\n  # ── scope-id claim 占位 ──────────────────────────────────────────────\n")
	b.WriteString("  # 断言 3/5 会因这些组变红，直到 title/why/files/tests/anchors 全部填齐。\n")
	for _, id := range ids {
		fmt.Fprintf(&b, "  - id: %s\n", id)
		fmt.Fprintf(&b, "    title: \"%s\"\n", placeholderTitle)
		b.WriteString("    kind: feature\n")
		b.WriteString("    symptom: \"待填写：用户会看到什么。\"\n")
		b.WriteString("    why: \"待填写：为什么必须保留、合上游时丢了会怎样。\"\n")
		b.WriteString("    files: []\n")
		b.WriteString("    tests: []\n")
		b.WriteString("    anchors: []\n")
	}
	return os.WriteFile(registry, []byte(b.String()), 0o644)
}

// stampPlan 在计划文档头部写一行认领声明（已有则替换）。
func stampPlan(plan string, ids []string) error {
	data, err := os.ReadFile(plan)
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	stamp := claimStampPrefix + strings.Join(ids, " / ") +
		"（claimed " + time.Now().UTC().Format("2006-01-02T15:04Z") + "）"
	for i, line := range lines {
		if strings.HasPrefix(line, claimStampPrefix) {
			lines[i] = stamp
			return os.WriteFile(plan, []byte(strings.Join(lines, "\n")), 0o644)
		}
	}
	// 首行是标题就插在它后面（留一个空行），否则插在最前面。
	at := 0
	if len(lines) > 0 && strings.HasPrefix(lines[0], "# ") {
		at = 1
	}
	head := append([]string{}, lines[:at]...)
	head = append(head, "", stamp)
	head = append(head, lines[at:]...)
	return os.WriteFile(plan, []byte(strings.Join(head, "\n")), 0o644)
}

// runResolve 裁决当前分支与 --branch 之间的组号冲突。
//
// 用法：站在**要改号的那条分支**上，`--branch` 指向另一边（通常是你要合进来、
// 或你要合进去的 main）。规则沿用仓库既有先例（G-MG→G-MN、G-NP→G-NS、
// G-PW/PX/PY→G-PZ/QA/QB）：
//   - 按 `git log -S 'id: G-XX'` 找两边的引入提交，先引入的一方保留该号；
//   - 当前分支是后来者 → 改号；当前分支是先引入者 → 拒绝，并告诉你该改哪一边；
//   - 提交信息里的历史 `Scope:` 一律不改写；清单里插一条 `# 撞号说明` 记下旧号、
//     新号、两边引入提交与时间；
//   - 计划正文里仍提到旧号的地方只报告不擅改（那是历史叙述），但机器拥有的
//     `> Scope IDs:` 头部行会被改写。
func runResolve(args []string) error {
	fs := flag.NewFlagSet("resolve", flag.ContinueOnError)
	branch := fs.String("branch", "", "另一条分支名（要比较撞号的那边）")
	root := fs.String("root", "", "仓库根")
	remote := fs.String("remote", "origin/main", "撞号基准：这里已有的号不算冲突")
	dryRun := fs.Bool("dry-run", false, "只打印裁决结果，不写文件")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *branch == "" {
		return fmt.Errorf("用法：scope-id resolve --branch <name> [--dry-run]")
	}
	r, reg, err := resolvePaths(*root, "")
	if err != nil {
		return err
	}
	if dirty, err := gitDirty(r); err != nil {
		return err
	} else if dirty {
		return fmt.Errorf("工作区有未提交改动，先提交或 stash 再裁决（会改写清单与计划文档）")
	}
	localIDs, err := scopeid.RegistryIDs(reg)
	if err != nil {
		return err
	}
	return resolveBranch(r, reg, *remote, *branch, localIDs, *dryRun)
}

func gitDirty(root string) (bool, error) {
	out, err := exec.Command("git", "-C", root, "status", "--porcelain").Output()
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) != "", nil
}

// mergeBase 取当前 HEAD 与另一条分支的共同祖先。
func mergeBase(root, branch string) (string, error) {
	out, err := exec.Command("git", "-C", root, "merge-base", "HEAD", branch).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func resolveBranch(root, registry, remote, branch string, localIDs []string, dryRun bool) error {
	baseIDs, remoteErr := scopeid.RegistryIDsAt(root, remote, scopeid.RegistryPath)
	if remoteErr != nil {
		// 退到 merge-base 再试一次：真正回答「这个号是这两边各自新取的，还是本来就有的」
		// 的是共同祖先，不是某条 remote 分支。退化成空基准会把祖先里早就登记过的号
		// 全判成撞号（真撞号里还混着 G-QB 这类老号，改号就会挪走无关的组）。
		var mb string
		mb, err := mergeBase(root, branch)
		if err != nil {
			fmt.Fprintf(os.Stderr, "· 读不到 %s 的清单（%v），退到 merge-base 也失败（%v），以空基准继续\n", remote, remoteErr, err)
			baseIDs = nil
		} else if baseIDs, err = scopeid.RegistryIDsAt(root, mb, scopeid.RegistryPath); err != nil {
			fmt.Fprintf(os.Stderr, "· 读不到 %s 的清单（%v），merge-base %s 也读不到（%v），以空基准继续\n", remote, remoteErr, shortSHA(mb), err)
			baseIDs = nil
		} else {
			fmt.Fprintf(os.Stderr, "· 读不到 %s 的清单（%v），改用 merge-base %s\n", remote, remoteErr, shortSHA(mb))
		}
	}
	base := make(map[string]bool, len(baseIDs))
	for _, id := range baseIDs {
		base[id] = true
	}
	branchIDs, err := scopeid.RegistryIDsAt(root, branch, scopeid.RegistryPath)
	if err != nil {
		return fmt.Errorf("读 %s 的清单失败：%w", branch, err)
	}
	branchSet := make(map[string]bool, len(branchIDs))
	for _, id := range branchIDs {
		branchSet[id] = true
	}

	var conflicts []string
	for _, id := range localIDs {
		if branchSet[id] && !base[id] {
			conflicts = append(conflicts, id)
		}
	}
	if len(conflicts) == 0 {
		fmt.Println("✓ 没有需要裁决的组号冲突")
		return nil
	}
	sort.Strings(conflicts)
	fmt.Printf("发现 %d 个撞号：%s\n", len(conflicts), strings.Join(conflicts, " / "))

	intros := make(map[string]introPair, len(conflicts))
	for _, id := range conflicts {
		h := introducingCommit(root, "HEAD", registry, id)
		o := introducingCommit(root, branch, registry, id)
		if h == "" || o == "" {
			return fmt.Errorf("%s 找不到引入提交（HEAD=%q, %s=%q），请人工比对后再改号", id, h, branch, o)
		}
		intros[id] = introPair{head: h, other: o}
	}
	// 当前分支必须先引入**全部**冲突号才允许整体改号；否则说明争的不是同一批号，
	// 机械改号会把本来没撞的号也挪走。
	for _, id := range conflicts {
		in := intros[id]
		if commitTime(root, in.head) >= commitTime(root, in.other) {
			return fmt.Errorf(
				"%s 是 %s（%s）先引入的，当前分支是后来者吗？先人工确认：\n"+
					"    HEAD 引入 %s\n    %s 引入 %s\n"+
					"  当前分支不是后来者时，请到必须先改号的那条分支上重跑本命令",
				id, branch, shortSHA(in.other), shortSHA(in.head), branch, shortSHA(in.other))
		}
	}

	used := make([]string, 0, len(localIDs)+len(branchIDs)+len(baseIDs))
	used = append(used, localIDs...)
	used = append(used, branchIDs...)
	used = append(used, baseIDs...)
	newIDs, err := scopeid.NextIDs(used, len(conflicts))
	if err != nil {
		return err
	}
	mapping := make(map[string]string, len(conflicts))
	for i, id := range conflicts {
		mapping[id] = newIDs[i]
	}
	fmt.Println("改号映射（当前分支改号，另一边保留）：")
	for _, id := range conflicts {
		fmt.Printf("    %s → %s   （HEAD %s vs %s %s）\n",
			id, mapping[id], shortSHA(intros[id].head), branch, shortSHA(intros[id].other))
	}
	if dryRun {
		fmt.Println("（--dry-run：未写任何文件）")
		return nil
	}

	note := collisionNote(conflicts, mapping, side(intros, true), branch, side(intros, false), nowUTC())
	if err := renumberRegistry(registry, mapping, note); err != nil {
		return err
	}
	if err := restampPlans(root, mapping); err != nil {
		return err
	}
	fmt.Println("✓ 清单已改号，并写入 `# 撞号说明`（提交信息里的历史 Scope 保持不变）")
	fmt.Println("  下一步：`git add docs/upstream-customizations.yaml docs/superpowers/plans && git commit -m 'chore(scope): 撞号裁决 …'`")
	return nil
}

type introPair = struct{ head, other string }

// side 把「每个号的引入提交对」投影成一边的 map，供撞号说明使用。
func side(m map[string]introPair, head bool) map[string]string {
	out := make(map[string]string, len(m))
	for id, p := range m {
		if head {
			out[id] = p.head
		} else {
			out[id] = p.other
		}
	}
	return out
}

// collisionNote 生成清单里那条 `# 撞号说明` 注释块，格式对齐仓库既有先例。
func collisionNote(conflicts []string, mapping map[string]string, headIntro map[string]string, branch string, branchIntro map[string]string, at string) string {
	var oldList, newList []string
	for _, id := range conflicts {
		oldList = append(oldList, id)
		newList = append(newList, mapping[id])
	}
	var b strings.Builder
	b.WriteString("\n  # 撞号说明：" + at + " scope-id resolve 裁决的结果。\n")
	b.WriteString("  # 本分支登记的是 " + strings.Join(oldList, " / ") + "，另一边 " + branch +
		" 先引入了同一批号，\n  # 按 G-MG→G-MN / G-NP→G-NS 的先例：提交信息保持原文不改写历史，\n")
	b.WriteString("  # 本表登记改用 " + strings.Join(newList, " / ") + "。\n")
	for _, id := range conflicts {
		b.WriteString("  #   " + id + "：HEAD " + shortSHA(headIntro[id]) + " vs " + branch + " " + shortSHA(branchIntro[id]) + "\n")
	}
	return b.String()
}

func nowUTC() string { return time.Now().UTC().Format("2006-01-02") }

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	if sha == "" {
		return "(未知)"
	}
	return sha
}

// renumberRegistry 把清单里的 `  - id: G-XX` 行换成新号，并在第一处替换前插入撞号说明。
func renumberRegistry(registry string, mapping map[string]string, note string) error {
	data, err := os.ReadFile(registry)
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	changed := 0
	firstAt := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		for oldID, newID := range mapping {
			if trimmed == "- id: "+oldID {
				lines[i] = strings.Replace(line, "- id: "+oldID, "- id: "+newID, 1)
				if firstAt < 0 {
					firstAt = i
				}
				changed++
				break
			}
		}
	}
	if changed == 0 {
		return fmt.Errorf("清单里没有找到要改的组号（%v）", mapping)
	}
	if firstAt >= 0 && note != "" {
		head := append([]string{}, lines[:firstAt]...)
		head = append(head, strings.Split(strings.TrimSuffix(note, "\n"), "\n")...)
		head = append(head, lines[firstAt:]...)
		lines = head
	}
	return os.WriteFile(registry, []byte(strings.Join(lines, "\n")), 0o644)
}

// restampPlans 只改机器拥有的 `> Scope IDs:` 头部行；正文里的人话一律不动，
// 仍在提到旧号的计划只报告（那是历史叙述，擅改会抹掉事故记录）。
func restampPlans(root string, mapping map[string]string) error {
	plans, err := filepath.Glob(filepath.Join(root, scopeid.PlansDir, "*.md"))
	if err != nil {
		return err
	}
	sort.Strings(plans)
	for _, p := range plans {
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		lines := strings.Split(string(data), "\n")
		touched := false
		for i, line := range lines {
			if !strings.HasPrefix(line, claimStampPrefix) {
				continue
			}
			next := line
			for oldID, newID := range mapping {
				next = strings.ReplaceAll(next, oldID, newID)
			}
			if next != line {
				lines[i] = next
				touched = true
			}
		}
		if !touched {
			continue
		}
		if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		fmt.Printf("  · 改写了 %s 的 Scope IDs 头部\n", rel)
	}
	return nil
}

// introducingCommit 返回在 ref 上「首次新增」该组号的提交（`-S` 按出现次数变化找）。
func introducingCommit(root, ref, registry, id string) string {
	out, err := exec.Command("git", "-C", root, "log", "--format=%H", "-S", "id: "+id, ref, "--", registry).Output()
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return ""
	}
	return lines[len(lines)-1] // git log 新→旧，最后一行是最早的引入提交
}

func commitTime(root, sha string) string {
	if sha == "" {
		return ""
	}
	out, err := exec.Command("git", "-C", root, "log", "-1", "--format=%ct", sha).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
