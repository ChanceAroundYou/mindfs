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
	"errors"
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
	case "reservations":
		err = runReservations(args)
	case "release":
		err = runRelease(args)
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

  scope-id next [--count N]              打印接下来 N 个空号（只读，不占位）
  scope-id claim <plan.md> [--count N]   申请 N 个号：先在共享预留目录原子占位，再写清单 + 计划头部
  scope-id reservations [--gc]           列出组号预留；--gc 回收已完成/失效的预留
  scope-id release <ID...>               释放指定预留（claim 中途失败时手动回滚）
  scope-id check                         清单内组号唯一、计划里的号都已登记
  scope-id resolve --branch <name>       撞号裁决：先引入者保留，当前分支改号

全局参数：
  --root <dir>      仓库根（默认当前目录的 git 根）
  --remote <ref>    取号依据 / 撞号基准（默认 origin/main）
  --registry <path> 清单路径（默认 docs/upstream-customizations.yaml）

取号规则：两字母段取「最大号 + 1」，绝不回填空洞；G-ZZ 之后进入三字母段。
claim 的号先在 $(git-common-dir)/scope-ids/ 下用 O_EXCL 原子占住，所以共享同一
clone 的多个 worktree 并发取号也不会拿到同一个 —— 这是撞号的根治点。
next 只读：它把现有预留也计入，但不占位，真正的原子性在 claim。
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
	// flag 包遇到第一个非 flag 参数就停止解析，而用法是 `claim <plan.md> --count N`
	// （位置参数在前）。不重排的话 `--count` 会被当成第二个位置参数，报「用法」错。
	flags, positional, err := splitFlagArgs(args)
	if err != nil {
		return err
	}
	if err := fs.Parse(flags); err != nil {
		return err
	}
	rest := append(positional, fs.Args()...)
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

	// 先在所有 worktree 共享的目录里原子占住号，再写清单 / 计划。预留才是真正的
	// 「这号归我」：并发里抢输的候选会被顺延成新号，所以后续一切都以 reserved 为准，
	// 不再用 allocate 猜出来的 ids。任一步失败就回滚本轮预留，避免泄漏死号。
	reserved, err := reserveIDs(r, ids, plan)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = scopeid.Release(r, reserved)
		}
	}()

	if err := appendPlaceholders(reg, reserved); err != nil {
		return err
	}
	if err := stampPlan(plan, reserved); err != nil {
		return err
	}
	committed = true
	rel, _ := filepath.Rel(r, plan)
	fmt.Printf("已申请 %s\n", strings.Join(reserved, " / "))
	fmt.Printf("  · 清单已追加 %d 个占位组（标题「%s」）\n", len(reserved), placeholderTitle)
	fmt.Printf("  · %s 头部已写入 `%s`\n", rel, claimStampPrefix+strings.Join(reserved, " / "))
	fmt.Println("下一步：把占位填成真条目（title / kind / symptom / why / files / tests / anchors），")
	fmt.Println("       否则 check-upstream 的断言 3/5 会红 —— 这是刻意的。")
	return nil
}

// splitFlagArgs 把 flag 与位置参数分开，让 `claim <plan.md> --count N` 这种
// 「位置参数在前」的写法也能解析。flag 包本身不允许，但这是用法文本里写的形式，
// 两者必须一致 —— 否则文档写着能跑、实际报「用法」错。
//
// 只认 `--flag` / `--flag=value` 形态（本工具全部用长 flag）；`-` 开头的都当 flag，
// 位置参数里不会有这种。
func splitFlagArgs(args []string) (flags, positional []string, err error) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			positional = append(positional, a)
			continue
		}
		flags = append(flags, a)
		// `--count 3` 这种带独立取值的，把值一起带走
		if !strings.Contains(a, "=") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			// 只有已知需要取值的 flag 才吃掉下一个 token；布尔 flag 不会走到这里，
			// 因为本工具没有布尔 flag。
			flags = append(flags, args[i+1])
			i++
		}
	}
	return flags, positional, nil
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

func runReservations(args []string) error {
	fs := flag.NewFlagSet("reservations", flag.ContinueOnError)
	root := fs.String("root", "", "仓库根")
	registry := fs.String("registry", "", "清单路径")
	gc := fs.Bool("gc", false, "回收已完成/失效的预留")
	if err := fs.Parse(args); err != nil {
		return err
	}
	r, reg, err := resolvePaths(*root, *registry)
	if err != nil {
		return err
	}
	if *gc {
		committed, err := scopeid.RegistryIDs(reg)
		if err != nil {
			return err
		}
		known := make(map[string]bool, len(committed))
		for _, id := range committed {
			known[id] = true
		}
		dropped, err := scopeid.GCReservations(r, scopeid.GCOptions{
			Committed: known,
			// 空文件（崩溃残留）过了 1 小时仍没写成清单，就当死号回收。
			StaleAfter: time.Hour,
		})
		if err != nil {
			return err
		}
		for _, id := range dropped {
			fmt.Printf("  · 回收 %s\n", id)
		}
		if len(dropped) == 0 {
			fmt.Println("没有可回收的预留")
		}
	}
	rs, err := scopeid.ListReservations(r)
	if err != nil {
		return err
	}
	if len(rs) == 0 {
		fmt.Println("（无预留）")
		return nil
	}
	fmt.Printf("共 %d 个预留：\n", len(rs))
	for _, res := range rs {
		if res.Empty {
			fmt.Printf("  %s  空文件（崩溃残留，--gc 回收）\n", res.ID)
			continue
		}
		when := ""
		if !res.TS.IsZero() {
			when = res.TS.Local().Format("2006-01-02 15:04")
		}
		fmt.Printf("  %s  %s  %s  %s\n", res.ID, orDash(res.Branch), orDash(res.Plan), when)
	}
	return nil
}

func runRelease(args []string) error {
	fs := flag.NewFlagSet("release", flag.ContinueOnError)
	root := fs.String("root", "", "仓库根")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ids := fs.Args()
	if len(ids) == 0 {
		return fmt.Errorf("用法：scope-id release <ID...>")
	}
	r, _, err := resolvePaths(*root, "")
	if err != nil {
		return err
	}
	n, err := scopeid.Release(r, ids)
	if err != nil {
		return err
	}
	fmt.Printf("已释放 %d 个预留（请求 %d 个）\n", n, len(ids))
	return nil
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
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

// allocate 推出接下来 n 个空号：取「origin/main 的清单」「本地清单」「共享预留目录」
// 以及「本 clone 所有本地分支已提交的清单」的并集。
//
// 为什么还要扫全部本地分支：兄弟 worktree 常常已经提交了某个号但还没推、也没走预留
// （旧机制留下的号，或并发里先落盘的那一方）。只读 origin/main 会看不见它们，于是
// 从陈旧远端重新发出同一个号 —— 这正是撞号的现场（task-117 已用 G-RD，origin/main
// 还停在 G-QT）。同一 clone 里 `git show <branch>:registry` 是现成的，扫一遍比事后
// resolve 便宜得多。真正落盘前的原子性仍由 reserveIDs 的 O_EXCL 保证。
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
	add(localBranchIDs(root))
	reserved, err := scopeid.ListReservations(root)
	if err != nil {
		return nil, err
	}
	for _, r := range reserved {
		seen[r.ID] = true
	}
	all := make([]string, 0, len(seen))
	for id := range seen {
		all = append(all, id)
	}
	sort.Strings(all)
	return scopeid.NextIDs(all, n)
}

// localBranchIDs 汇总本 clone 所有本地分支已提交清单里的组号。取不到的（清单还没
// 提交、YAML 坏了等）静默跳过 —— 少看一个分支只影响防撞强度，不该阻断取号。
func localBranchIDs(root string) []string {
	out, err := exec.Command("git", "-C", root, "for-each-ref", "--format=%(refname:short)", "refs/heads").Output()
	if err != nil {
		return nil
	}
	var all []string
	for _, branch := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if branch == "" {
			continue
		}
		ids, err := scopeid.RegistryIDsAt(root, branch, scopeid.RegistryPath)
		if err != nil {
			continue
		}
		all = append(all, ids...)
	}
	return all
}

// reserveIDs 在共享预留目录里原子占住这些号，返回实际占到的号。
//
// 正常情况下 allocate 已经把现有占位计入，第一次就全中；但两个 claim 可能几乎同时
// 读到同一份旧状态，于是 Reserve 会撞上 ErrIDTaken —— 这时把该号并入已用集、重新取号，
// 直到凑够 n 个。任何非「已被占」的错误直接上报（调用方 defer 会回滚已占部分）。
func reserveIDs(root string, ids []string, plan string) ([]string, error) {
	branch := currentBranch(root)
	meta := scopeid.Reservation{Branch: branch, Plan: plan, Worktree: root, TS: time.Now().UTC()}
	var got []string
	used := make(map[string]bool)
	for _, id := range ids {
		used[id] = true
	}
	pending := append([]string{}, ids...)
	for len(got) < len(ids) {
		if len(pending) == 0 {
			// 候选用完了还不够，说明撞得厉害：按已占集重新取剩下的号。
			all := make([]string, 0, len(used))
			for id := range used {
				all = append(all, id)
			}
			sort.Strings(all)
			more, err := scopeid.NextIDs(all, len(ids)-len(got))
			if err != nil {
				return got, err
			}
			pending = append(pending, more...)
			for _, id := range more {
				used[id] = true
			}
			continue
		}
		id := pending[0]
		pending = pending[1:]
		err := scopeid.Reserve(root, id, meta)
		switch {
		case err == nil:
			got = append(got, id)
		case errors.Is(err, scopeid.ErrIDTaken):
			// 被别的 worktree 抢先占了 —— 该号已在 used 里，继续试下一个。
		default:
			return got, err
		}
	}
	return got, nil
}

// currentBranch 返回当前分支名；取不到时给空串（预留仍成立，只是 GC 少一条判据）。
func currentBranch(root string) string {
	out, err := exec.Command("git", "-C", root, "branch", "--show-current").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
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
