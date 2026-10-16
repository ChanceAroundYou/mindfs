// check-upstream 是 fork 定制清单的覆盖率门禁。
//
// 背景（AGENTS.md「fork 定制与上游合并（必守）」）：这是上游 a9gent/mindfs 的 fork，
// 每次改动都是一份会被上游合并冲掉的定制。清单 docs/upstream-customizations.yaml 是
// 「哪条该留」的唯一真相，本工具把它与 git 的实际分歧面对账。
//
// 六条断言，任一失败即非零退出：
//
//	1 覆盖  —— 每个 delta 文件必须被某组 files 命中（新增定制未登记；已删除的文件不算）
//	2 尚存  —— 每个 files 条目必须仍在 delta 里（定制已被上游覆盖 / 已被上游吸收）
//	3 测试  —— 每个 tests 路径不仅存在，还得真能被测试运行器跑到
//	4 锚点  —— 每个 anchors 的 path:子串在源码里命中
//	5 非空  —— 每组至少有一个针对性测试
//
// 断言 2 是「静默丢失」的主检测器：定制被上游实现顶掉时，该文件往往仍因上游自身的改动
// 留在 delta 里，光看文件级差异查不出来；而一旦整份定制连同上游改动一起被替换，
// 文件就会掉出 delta —— 这正是要报的。测试（断言 3/5）才是逐条的覆盖检测器。
//
// 用法：
//
//	go run ./scripts/check-upstream
//	go run ./scripts/check-upstream -explain        # 额外打印未覆盖文件的归属证据
//	go run ./scripts/check-upstream -baseline <sha> # 覆盖动态算出的 merge-base
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	registryPath = "docs/upstream-customizations.yaml"
	kindDocs     = "docs"
)

var kinds = []string{"feature", "fix", "perf", "arch", "trim", "deploy", kindDocs}

func validKind(k string) bool {
	for _, v := range kinds {
		if v == k {
			return true
		}
	}
	return false
}

// runnableTestGlob 从 web/package.json 的 test 脚本里取出测试运行器实际使用的文件名 glob。
//
// 「文件在磁盘上」不等于「测试会跑」：web/tests/ 下曾有 .behavior.mjs 这类后缀，
// 不匹配运行器配置的测试文件 glob，于是它躺在清单里、永远不执行、也永远不会红
// —— 一条退役后没人发现的死条目。glob 从 package.json 现读，改了运行器这里自动跟上。
func runnableTestGlob(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "web", "package.json"))
	if err != nil {
		return ""
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal(data, &pkg) != nil {
		return ""
	}
	for _, tok := range strings.Fields(pkg.Scripts["test"]) {
		// 取 `--test` 之后那个带通配的文件名参数（形如 tests/*.test.mjs）
		if strings.ContainsAny(tok, "*?[") && !strings.HasPrefix(tok, "-") {
			return path.Base(tok)
		}
	}
	return ""
}

// isRunnableTest：Go 测试靠 `go test ./...` 按 *_test.go 纳管，不需要 glob；
// 其余按运行器 glob 匹配文件名（清单里的是相对路径，比对用 basename）。
func isRunnableTest(p, glob string) bool {
	if filepath.Ext(p) == ".go" {
		return strings.HasSuffix(p, "_test.go")
	}
	if glob == "" {
		return true // 读不到运行器配置就不误报，交给别的断言兜
	}
	ok, err := path.Match(glob, path.Base(p))
	return err == nil && ok
}

type registry struct {
	Version  int      `yaml:"version"`
	Baseline baseline `yaml:"baseline"`
	Groups   []group  `yaml:"groups"`
}

type baseline struct {
	UpstreamRef string `yaml:"upstream_ref"`
	MergeBase   string `yaml:"merge_base"`
	UpstreamTag string `yaml:"upstream_tag"`
	RecordedAt  string `yaml:"recorded_at"`
}

type group struct {
	ID      string   `yaml:"id"`
	Title   string   `yaml:"title"`
	Kind    string   `yaml:"kind"`
	Symptom string   `yaml:"symptom"`
	Why     string   `yaml:"why"`
	Files   []string `yaml:"files"`
	Tests   []string `yaml:"tests"`
	Anchors []string `yaml:"anchors"`
}

type deltaFile struct {
	status string
	path   string
}

func main() {
	root := flag.String("root", ".", "仓库根目录")
	baselineFlag := flag.String("baseline", "", "覆盖 baseline（默认 git merge-base HEAD upstream/main）")
	explain := flag.Bool("explain", false, "对未覆盖文件打印归属证据（git log 主题）")
	flag.Parse()

	if err := run(*root, *baselineFlag, *explain); err != nil {
		fmt.Fprintf(os.Stderr, "\n✗ check-upstream 未通过：%v\n", err)
		os.Exit(1)
	}
}

func run(root, baselineFlag string, explain bool) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}

	reg, err := loadRegistry(filepath.Join(root, registryPath))
	if err != nil {
		return err
	}
	if len(reg.Groups) == 0 {
		return fmt.Errorf("%s 里没有任何分组", registryPath)
	}

	upstreamRef := reg.Baseline.UpstreamRef
	if upstreamRef == "" {
		upstreamRef = "upstream/main"
	}
	baseline := baselineFlag
	if baseline == "" {
		baseline, err = mergeBase(root, upstreamRef)
		if err != nil {
			// 无网 / 未 fetch upstream 的机器不该被这个门禁卡住。
			fmt.Printf("· 跳过：拿不到 %s 的 merge-base（%v）\n", upstreamRef, err)
			fmt.Println("  需要时先 `git fetch upstream --tags`。")
			return nil
		}
	}
	fmt.Printf("· baseline = %s（%s）· %d 组\n\n", baseline, upstreamRef, len(reg.Groups))

	delta, err := diffDelta(root, baseline)
	if err != nil {
		return err
	}
	if len(delta) == 0 {
		return fmt.Errorf("baseline %s..HEAD 的 delta 为空 —— baseline 选错了？", baseline)
	}

	var failures []string

	// present = delta 全部路径（断言 2 用）；alive = 其中仍存在于 HEAD 的（断言 1 用）。
	//
	// 两者分开是因为「删掉一个文件」不是定制：文件已不在 HEAD，没道理要求某组登记它
	// （断言 1）。但断言 2 仍然把 D 当作「尚存」—— 这是历史行为，刻意不动：改成不算，
	// 会一次性炸出 71 条陈旧路径（都是 fork 内部改名/挪目录后的**旧路径**，如
	// web/src/components/FileTree.tsx → web/src/components/file/FileTree.tsx），
	// 那是登记表的存量债，该另开一轮清，不该顺手混进别的改动里。
	present := make(map[string]bool, len(delta))
	alive := make(map[string]bool, len(delta))
	for _, f := range delta {
		present[f.path] = true
		if f.status != "D" {
			alive[f.path] = true
		}
	}

	// 断言 1：delta 全覆盖（只算仍存在的文件）
	var uncovered []string
	for f := range alive {
		if !coveredByAny(reg.Groups, f) {
			uncovered = append(uncovered, f)
		}
	}
	if len(uncovered) > 0 {
		sort.Strings(uncovered)
		fmt.Printf("✗ 未覆盖的 delta 文件：%d / %d\n", len(uncovered), len(alive))
		for _, p := range uncovered {
			fmt.Printf("    %s\n", p)
			if explain {
				for _, s := range commitSubjects(root, baseline, p, 3) {
					fmt.Printf("        ↳ %s\n", s)
				}
			}
		}
		fmt.Println()
		failures = append(failures, fmt.Sprintf("%d 个 delta 文件未被任何组登记", len(uncovered)))
	}

	// 断言 2：登记的条目仍存在于 delta
	var phantom []string
	for _, g := range reg.Groups {
		for _, pat := range g.Files {
			if !matchesAnyDelta(pat, present) {
				phantom = append(phantom, fmt.Sprintf("%s %s", g.ID, pat))
			}
		}
	}
	if len(phantom) > 0 {
		fmt.Printf("✗ 清单里已不在 delta 的条目：%d\n", len(phantom))
		fmt.Println("    （要么已被上游覆盖，要么已合入上游。确认后从 yaml 移除，或恢复实现。）")
		for _, p := range phantom {
			fmt.Printf("    %s\n", p)
		}
		fmt.Println()
		failures = append(failures, fmt.Sprintf("%d 个登记条目已不在 delta", len(phantom)))
	}

	// 断言 3/4/5：每组自洽
	//
	// kind=docs 的组豁免「必须有测试」：它的保护由断言 2 承担 —— 文档一旦被上游吸收
	// 就会掉出 delta 而被报出来。硬塞一个测试只会制造仪式感。
	var selfBad []string
	testGlob := runnableTestGlob(root)
	for _, g := range reg.Groups {
		if !validKind(g.Kind) {
			selfBad = append(selfBad, fmt.Sprintf("%s 的 kind 非法: %q（应为 %s）", g.ID, g.Kind, strings.Join(kinds, "|")))
		}
		if g.Kind != kindDocs && len(g.Tests) == 0 {
			selfBad = append(selfBad, g.ID+" 未列任何针对性测试")
		}
		if strings.TrimSpace(g.Symptom) == "" {
			selfBad = append(selfBad, g.ID+" 缺 symptom（判定「还得不该留」的依据）")
		}
		if strings.TrimSpace(g.Why) == "" {
			selfBad = append(selfBad, g.ID+" 缺 why（为什么必须保留）")
		}
		for _, t := range g.Tests {
			if _, err := os.Stat(filepath.Join(root, t)); err != nil {
				selfBad = append(selfBad, fmt.Sprintf("%s 测试不存在: %s", g.ID, t))
				continue
			}
			// 存在 ≠ 会跑：跑不到的测试是条死条目 —— 它永远不会红，也就永远发现不了被覆盖
			if !isRunnableTest(t, testGlob) {
				selfBad = append(selfBad, fmt.Sprintf(
					"%s 测试跑不到（不匹配运行器 glob %q）: %s —— 加进清单前先确认它在 make test/test-web 里真的执行",
					g.ID, testGlob, t))
			}
		}
		for _, a := range g.Anchors {
			p, sub, ok := strings.Cut(a, ":")
			if !ok || p == "" || sub == "" {
				selfBad = append(selfBad, fmt.Sprintf("%s 锚点格式应为 path:子串 —— %q", g.ID, a))
				continue
			}
			data, err := os.ReadFile(filepath.Join(root, p))
			if err != nil {
				selfBad = append(selfBad, fmt.Sprintf("%s 锚点文件读不到: %s", g.ID, p))
				continue
			}
			if !strings.Contains(string(data), sub) {
				selfBad = append(selfBad, fmt.Sprintf("%s 锚点失配: %s 里找不到 %q", g.ID, p, sub))
			}
		}
	}
	if len(selfBad) > 0 {
		fmt.Printf("✗ 分组自身不自洽：%d\n", len(selfBad))
		for _, s := range selfBad {
			fmt.Printf("    %s\n", s)
		}
		fmt.Println()
		failures = append(failures, fmt.Sprintf("%d 处分组不自洽", len(selfBad)))
	}

	if len(failures) > 0 {
		return fmt.Errorf("%s", strings.Join(failures, "；"))
	}
	fmt.Printf("✓ 全部通过：%d 个 delta 文件被 %d 组完整覆盖\n", len(alive), len(reg.Groups))
	return nil
}

func loadRegistry(p string) (*registry, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("读不到清单 %s：%w", registryPath, err)
	}
	var reg registry
	if err := yaml.Unmarshal(data, &reg); err != nil {
		return nil, fmt.Errorf("解析 %s 失败：%w", registryPath, err)
	}
	return &reg, nil
}

func git(root string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s", msg)
	}
	return string(out), nil
}

func mergeBase(root, upstreamRef string) (string, error) {
	out, err := git(root, "merge-base", "HEAD", upstreamRef)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// diffDelta 取 baseline..HEAD 的文件级差异。
// --no-renames 让改名退化成 A+D，覆盖判定就不必额外处理 R/C 条目。
func diffDelta(root, baseline string) ([]deltaFile, error) {
	out, err := git(root, "diff", "--name-status", "--no-renames", baseline+"..HEAD")
	if err != nil {
		return nil, fmt.Errorf("git diff 失败：%w", err)
	}
	var files []deltaFile
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		files = append(files, deltaFile{status: strings.TrimSpace(parts[0]), path: strings.TrimSpace(parts[1])})
	}
	return files, nil
}

func commitSubjects(root, baseline, file string, n int) []string {
	out, err := git(root, "log", "--format=%h %s", "-n", fmt.Sprint(n), baseline+"..HEAD", "--", file)
	if err != nil {
		return nil
	}
	var subs []string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if l != "" {
			subs = append(subs, l)
		}
	}
	return subs
}

// matchesFile 的语义（刻意只有两种，Go 的 path.Match 不支持 **）：
//   - 结尾带 /  → 目录前缀匹配
//   - 含 *      → path.Match 单段通配
//   - 其它      → 精确路径
func matchesFile(pattern, file string) bool {
	switch {
	case strings.HasSuffix(pattern, "/"):
		return strings.HasPrefix(file, pattern)
	case strings.Contains(pattern, "*"):
		ok, err := path.Match(pattern, file)
		return err == nil && ok
	default:
		return pattern == file
	}
}

// coveredByAny 把 tests 也算作登记：测试文件本身就是 fork 的定制产物，
// 列在 tests 里就是被登记了，不必在 files 里再抄一遍。
func coveredByAny(groups []group, file string) bool {
	for _, g := range groups {
		for _, pat := range append(append([]string{}, g.Files...), g.Tests...) {
			if matchesFile(pat, file) {
				return true
			}
		}
	}
	return false
}

// matchesAnyDelta：目录前缀/通配条目只要命中至少一个 delta 文件即算「尚存」。
func matchesAnyDelta(pattern string, present map[string]bool) bool {
	for f := range present {
		if matchesFile(pattern, f) {
			return true
		}
	}
	return false
}

