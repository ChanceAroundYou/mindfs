// resolve 子命令：两分支撞号时的裁决。
//
// 它读 git 历史、改写清单与计划文档，因此与 claim 一样属于「写」操作 —— 但写的是
// 已经发生过的冲突结论，不发明新号。归属 main 包而非独立包，是因为它复用 main.go
// 的路径解析与 `> Scope IDs:` 头部约定；拆文件是为了守 500 行硬线，不是拆职责边界。
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
