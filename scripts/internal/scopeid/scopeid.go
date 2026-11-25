// Package scopeid 是 `Scope: G-XX` 组号的唯一算术与扫描实现。
//
// 为什么要有这个包：组号是手写的字符串，历史上反复出事（2026-11-23 三个并行分支
// 各自从本地清单取「最大号 + 1」，拿到同一批号）。这里把「号怎么数」「计划里写了
// 哪些号」集中到一处，gate（scripts/check-upstream）与分配器（scripts/scope-id）
// 共用同一份实现，免得两边对「什么算一个号」有不同理解。
package scopeid

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

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

// ReservationsDir 返回组号预留目录：所有共享同一个 clone 的 worktree 看到的是同一个
// 目录（git common dir 下的 scope-ids/）。取号先在这里原子占位，再写清单 —— 两个
// worktree 才能既不读到对方的 stale 清单，也不会拿到同一个号。
//
// 不在 git 仓库里（例如单元测试的临时目录）时退回到 <root>/.scope-ids：那里没有
// worktree 并发可言，回退是等价语义，只是让命令在仓外也能跑。
func ReservationsDir(root string) (string, error) {
	out, err := exec.Command("git", "-C", root, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		abs, aerr := filepath.Abs(root)
		if aerr != nil {
			return "", aerr
		}
		return filepath.Join(abs, ".scope-ids"), nil
	}
	common := strings.TrimSpace(string(out))
	if !filepath.IsAbs(common) {
		// 老版本 git 没有 --path-format=absolute，返回的是相对路径（可能相对 root）。
		common, err = filepath.Abs(filepath.Join(root, common))
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(common, "scope-ids"), nil
}

// Reservation 是一个组号的占位记录。Empty 表示哨兵文件在崩溃窗口里被留下、metadata
// 没写完 —— 仍按「已占用」处理（宁可跳号，绝不重发），由 GC 回收。
type Reservation struct {
	ID       string    `json:"id"`
	Branch   string    `json:"branch"`
	Plan     string    `json:"plan"`
	Worktree string    `json:"worktree"`
	TS       time.Time `json:"ts"`
	Empty    bool      `json:"-"`
}

// Reserve 在共享目录里原子占住一个组号。已存在（含只落了空文件的崩溃残留）返回
// ErrIDTaken，让调用方去试下一个号。目录不存在时自动创建。
func Reserve(root, id string, meta Reservation) error {
	dir, err := ReservationsDir(root)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, id), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return ErrIDTaken
		}
		return err
	}
	meta.ID = id
	data, _ := json.MarshalIndent(meta, "", "  ")
	_, werr := f.Write(data)
	cerr := f.Close()
	if werr != nil {
		return werr
	}
	if cerr != nil {
		return cerr
	}
	return nil
}

// ErrIDTaken 表示该号已被（另一个 worktree 或崩溃残留）占住。
var ErrIDTaken = errors.New("组号已被预留")

// ListReservations 读出所有预留，按组号排序。空文件以 Empty=true 呈现。
func ListReservations(root string) ([]Reservation, error) {
	dir, err := ReservationsDir(root)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []Reservation
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		var r Reservation
		if len(strings.TrimSpace(string(data))) == 0 {
			r = Reservation{ID: e.Name(), Empty: true}
		} else if err := json.Unmarshal(data, &r); err != nil {
			// metadata 坏了也当成已占用，只是信息不全。
			r = Reservation{ID: e.Name(), Empty: true}
		}
		if r.ID == "" {
			r.ID = e.Name()
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Release 删掉指定组号的预留，返回实际删掉的个数。不存在的号静默跳过。
func Release(root string, ids []string) (int, error) {
	dir, err := ReservationsDir(root)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, id := range ids {
		if err := os.Remove(filepath.Join(dir, id)); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return n, err
		}
		n++
	}
	return n, nil
}

// GCOptions 控制 GCReservations 的回收范围。
type GCOptions struct {
	// Committed 是当前已提交清单里的组号：已在清单里的预留已完成使命（改由 resolve
	// 管），可以回收。
	Committed map[string]bool
	// BranchExists 判断某分支名是否还存在：分支删了，它的预留就是死号。
	BranchExists func(string) bool
	// StaleAfter：空文件（崩溃残留）超过这个时长才回收；零值表示立刻可回收。
	StaleAfter time.Duration
	Now        time.Time
}

// GCReservations 回收已无意义的预留，返回被删掉的组号。三种情形：
//  1. 号已进当前已提交清单 —— 预留的作用已由清单接管；
//  2. 预留记录的分支已不存在；
//  3. 空文件（metadata 没写完）且超过 StaleAfter。
//
// 只删不搬；判不准的一律保留。
func GCReservations(root string, opts GCOptions) ([]string, error) {
	rs, err := ListReservations(root)
	if err != nil {
		return nil, err
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	var drop []string
	for _, r := range rs {
		switch {
		case opts.Committed[r.ID]:
			drop = append(drop, r.ID)
		case opts.BranchExists != nil && r.Branch != "" && !opts.BranchExists(r.Branch):
			drop = append(drop, r.ID)
		case r.Empty:
			// 空文件没有 TS；用文件修改时间判超龄。
			dir, _ := ReservationsDir(root)
			info, statErr := os.Stat(filepath.Join(dir, r.ID))
			if statErr == nil && opts.StaleAfter > 0 && now.Sub(info.ModTime()) > opts.StaleAfter {
				drop = append(drop, r.ID)
			}
		}
	}
	if _, err := Release(root, drop); err != nil {
		return nil, err
	}
	return drop, nil
}

