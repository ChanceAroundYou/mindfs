// Package scopeid 是 `Scope: G-XX` 组号的唯一算术与扫描实现。
//
// 为什么要有这个包：组号是手写的字符串，历史上反复出事（2026-11-23 三个并行分支
// 各自从本地清单取「最大号 + 1」，拿到同一批号）。这里把「号怎么数」「计划里写了
// 哪些号」集中到一处，gate（scripts/check-upstream）与分配器（scripts/scope-id）
// 共用同一份实现，免得两边对「什么算一个号」有不同理解。
package scopeid

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// RegistryPath 是清单在仓库里的位置（相对仓库根）。
const RegistryPath = "docs/upstream-customizations.yaml"

// PlansDir 是执行计划文档所在目录（相对仓库根）。
const PlansDir = "docs/superpowers/plans"

// TokenRE 匹配执行计划里写下的组号。只认大写字母 1–3 位，与清单里实际出现过的
// 形状一致；G-XXX 这类占位符照样算「写了一个号」。
var TokenRE = regexp.MustCompile(`\bG-[A-Z]{1,3}\b`)

// TwoLetterMax 是两字母号段的上限：G-AA..G-ZZ 共 676 个。
const TwoLetterMax = 26 * 26

// ThreeLetterStart 是三字母号段的第一个序号（G-AAA）。
const ThreeLetterStart = TwoLetterMax + 1

// Ordinal 把组号换成连续的序号：两字母 G-AA..G-ZZ = 1..676，三字母
// G-AAA..G-ZZZ = 677..18252。不是合法组号形状时返回 ok=false。
func Ordinal(id string) (n int, letters int, ok bool) {
	if !strings.HasPrefix(id, "G-") {
		return 0, 0, false
	}
	body := id[2:]
	if body == "" || len(body) > 3 {
		return 0, 0, false
	}
	acc := 0
	for _, ch := range body {
		if ch < 'A' || ch > 'Z' {
			return 0, 0, false
		}
		acc = acc*26 + int(ch-'A')
	}
	if len(body) == 2 {
		return acc + 1, 2, true
	}
	return TwoLetterMax + acc + 1, 3, true
}

// Format 是 Ordinal 的逆运算。n<=676 给两字母，往上给三字母。
func Format(n int) (string, error) {
	if n < 1 {
		return "", fmt.Errorf("组号序号必须 >= 1，得到 %d", n)
	}
	if n <= TwoLetterMax {
		n--
		return "G-" + string(rune('A'+n/26)) + string(rune('A'+n%26)), nil
	}
	n -= TwoLetterMax + 1
	if n >= 26*26*26 {
		return "", fmt.Errorf("组号已用尽（超过 G-ZZZ）")
	}
	return "G-" +
		string(rune('A'+n/(26*26))) +
		string(rune('A'+n/26%26)) +
		string(rune('A'+n%26)), nil
}

// Duplicates 找出重复出现的号，每个只报一次，按字典序。
func Duplicates(ids []string) []string {
	seen := make(map[string]bool, len(ids))
	reported := make(map[string]bool, len(ids))
	var dups []string
	for _, id := range ids {
		if seen[id] && !reported[id] {
			dups = append(dups, id)
			reported[id] = true
		}
		seen[id] = true
	}
	sort.Strings(dups)
	return dups
}

// NextIDs 从已用集合里推出接下来 n 个空号。
//
// 规则：先在两字母号段里取「最大号 + 1」，绝不回头填洞 —— 回填空号会让两个分支
// 更容易拿到同一个号（A 分支填了 G-AB，B 分支也扫到同一个洞）。两字母用尽后才
// 进入三字母号段，并从三字母最大号往上续（G-PDF / G-WIN / G-WSX 这几只历史遗留
// 三字母号因此不会被重新发出去）。
func NextIDs(existing []string, n int) ([]string, error) {
	if n <= 0 {
		return nil, nil
	}
	used := make(map[string]bool, len(existing))
	maxTwo, maxThree := 0, 0
	for _, id := range existing {
		used[id] = true
		ord, letters, ok := Ordinal(id)
		switch {
		case !ok:
			continue
		case letters == 2 && ord > maxTwo:
			maxTwo = ord
		case letters == 3 && ord > maxThree:
			maxThree = ord
		}
	}
	var out []string
	// 两字母段没满就一直在两字母段里加。历史遗留的三字母号（G-PDF / G-WIN / G-WSX）
	// 是孤岛，不该把新号一起拖进三字母段 —— 除非新号本身很可能撞上它们。
	next := maxTwo + 1
	if maxTwo >= TwoLetterMax {
		next = ThreeLetterStart
		if maxThree+1 > next {
			next = maxThree + 1
		}
	}
	for len(out) < n {
		id, err := Format(next)
		if err != nil {
			return nil, err
		}
		next++
		if used[id] {
			continue
		}
		out = append(out, id)
	}
	return out, nil
}

// RegistryIDs 读出清单里的组号（保持文件顺序）。
func RegistryIDs(registryPath string) ([]string, error) {
	var doc struct {
		Groups []struct {
			ID string `yaml:"id"`
		} `yaml:"groups"`
	}
	data, err := os.ReadFile(registryPath)
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("解析 %s 失败：%w", registryPath, err)
	}
	ids := make([]string, 0, len(doc.Groups))
	for _, g := range doc.Groups {
		ids = append(ids, g.ID)
	}
	return ids, nil
}

// RegistryIDsAt 读某个 git 提交/引用里的清单。分配号必须看远端（origin/main），
// 否则两个分支都从本地清单取号，必然撞车。
func RegistryIDsAt(root, ref, registryPath string) ([]string, error) {
	out, err := exec.Command("git", "-C", root, "show", ref+":"+registryPath).Output()
	if err != nil {
		return nil, fmt.Errorf("读 %s:%s 失败：%w", ref, registryPath, err)
	}
	var doc struct {
		Groups []struct {
			ID string `yaml:"id"`
		} `yaml:"groups"`
	}
	if err := yaml.Unmarshal(out, &doc); err != nil {
		return nil, fmt.Errorf("解析 %s:%s 失败：%w", ref, registryPath, err)
	}
	ids := make([]string, 0, len(doc.Groups))
	for _, g := range doc.Groups {
		ids = append(ids, g.ID)
	}
	return ids, nil
}

// PlanRef 是计划文档里的一处组号引用。
type PlanRef struct {
	File string // 相对仓库根
	Line int    // 1 起
	ID   string
}

// IsTemplatePlaceholder 判断一个组号是不是文档模板里的占位写法。
//
// `Scope: G-XX` / `Scope: G-XXX` 这种「同一个字母重复」的号只出现在模板与规则说明里，
// 不是任何一组定制的真实编号。真存在时必须已被登记，所以扫描器跳过它们不会漏报 ——
// 反过来不跳过，任何介绍本机制的文档都会把断言 7 判红。
func IsTemplatePlaceholder(id string) bool {
	letters := strings.TrimPrefix(id, "G-")
	if len(letters) < 2 {
		return false
	}
	for i := 1; i < len(letters); i++ {
		if letters[i] != letters[0] {
			return false
		}
	}
	return true
}

// ScanPlanScopes 收集计划文档里「含 Scope 的行」上的组号。
//
// 只看含 "Scope" 的行：正文里顺带提到别的号（登记表引用、历史撞号说明）不构成
// 本计划的承诺，要求它们全部登记只会逼人加永远不用的空组。模板占位（G-XX）同理跳过。
func ScanPlanScopes(root string) ([]PlanRef, error) {
	plans, err := filepath.Glob(filepath.Join(root, PlansDir, "*.md"))
	if err != nil {
		return nil, fmt.Errorf("列出计划文档失败：%w", err)
	}
	sort.Strings(plans)
	var refs []PlanRef
	for _, p := range plans {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("读 %s 失败：%w", p, err)
		}
		rel, _ := filepath.Rel(root, p)
		for i, line := range strings.Split(string(data), "\n") {
			if !strings.Contains(line, "Scope") {
				continue
			}
			for _, id := range TokenRE.FindAllString(line, -1) {
				if IsTemplatePlaceholder(id) {
					continue
				}
				refs = append(refs, PlanRef{File: rel, Line: i + 1, ID: id})
			}
		}
	}
	return refs, nil
}

// Unresolved 返回计划里写了、但 known 里没有的引用（按文件行号排序，已排序）。
func Unresolved(refs []PlanRef, known map[string]bool) []PlanRef {
	var out []PlanRef
	for _, r := range refs {
		if !known[r.ID] {
			out = append(out, r)
		}
	}
	return out
}
