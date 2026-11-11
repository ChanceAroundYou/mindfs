package codexsync

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// harnessAdapter 把「某个 harness 的共享配置」接入同一套同步引擎。
//
// 同步引擎本身（Git fetch/rebase/push、锁、状态机、定时 Runner）与 harness 无关；
// 每个 harness 只需要提供三件事：共享目录、共享内容的校验、以及把共享内容渲染到
// 本机运行时文件。codex 的 render 仍由 RunAt 里既有的 renderRuntime 负责（它还要
// 处理 active profile 与 llmux 链接），这里只在 adapter 中登记校验与可用性。
type harnessAdapter struct {
	name         string
	sharedDir    string // .agents 下的相对目录
	requiredFile string // 该目录下必须存在的文件，缺失即视为不可用
	validate     func(paths Paths) error
	render       func(paths Paths) error
}

func harnessAdapters(paths Paths) []harnessAdapter {
	return []harnessAdapter{
		{
			name:         "codex",
			sharedDir:    "codex",
			requiredFile: "config.toml",
			validate:     validateSharedSources,
		},
		{
			name:         "claude",
			sharedDir:    "claude",
			requiredFile: "settings.shared.json",
			validate:     validateClaudeShared,
			render:       renderClaudeRuntime,
		},
	}
}

func (a harnessAdapter) availability(paths Paths) (bool, string) {
	dir := filepath.Join(paths.AgentsRoot, a.sharedDir)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return false, fmt.Sprintf("no shared configuration directory at %s", filepath.ToSlash(filepath.Join(".agents", a.sharedDir)))
	}
	required := filepath.Join(dir, a.requiredFile)
	if info, err := os.Stat(required); err != nil || !info.Mode().IsRegular() {
		return false, fmt.Sprintf("shared configuration is missing %s", filepath.ToSlash(filepath.Join(".agents", a.sharedDir, a.requiredFile)))
	}
	return true, ""
}

// HarnessStatuses 返回全部已登记 harness 的可用性。字段是纯追加的：旧前端或旧
// worker 不认识它时行为不变。
func HarnessStatuses() []HarnessInfo {
	paths, err := ResolvePaths()
	if err != nil {
		return nil
	}
	return HarnessStatusesAt(paths)
}

func HarnessStatusesAt(paths Paths) []HarnessInfo {
	adapters := harnessAdapters(paths)
	out := make([]HarnessInfo, 0, len(adapters))
	for _, adapter := range adapters {
		info := HarnessInfo{Name: adapter.name, SharedDir: adapter.sharedDir}
		if available, reason := adapter.availability(paths); available {
			info.Available = true
		} else {
			info.Message = reason
		}
		out = append(out, info)
	}
	return out
}

// validateAllSharedSources 校验全部可用 harness 的共享内容。任何一个失败都阻止
// 仓库同步与运行时渲染：宁可不同步，也不把带凭据的配置推进 Git。
func validateAllSharedSources(paths Paths) error {
	for _, adapter := range harnessAdapters(paths) {
		if adapter.validate == nil {
			continue
		}
		if available, _ := adapter.availability(paths); !available {
			continue
		}
		if err := adapter.validate(paths); err != nil {
			return fmt.Errorf("%s shared configuration: %w", adapter.name, err)
		}
	}
	return nil
}

// renderSharedHarnessRuntimes 渲染所有自带 renderer 的可用 harness。codex 不在此列
// （由 renderRuntime + ensureLLMuxProfile 在 sync 流程里单独处理）。
func renderSharedHarnessRuntimes(paths Paths) error {
	for _, adapter := range harnessAdapters(paths) {
		if adapter.render == nil {
			continue
		}
		if available, _ := adapter.availability(paths); !available {
			continue
		}
		if err := adapter.render(paths); err != nil {
			return fmt.Errorf("%s runtime configuration: %w", adapter.name, err)
		}
	}
	return nil
}

// normalizeRepoPath 把 git 输出的路径归一成仓库相对路径；绝对路径、反斜杠与
// 上跳路径一律拒绝（allowlist 之前的第一道门）。
func normalizeRepoPath(repoPath string) (string, bool) {
	if repoPath == "" || strings.Contains(repoPath, `\`) || strings.HasPrefix(repoPath, "/") {
		return "", false
	}
	clean := strings.TrimPrefix(repoPath, "./")
	if clean != repoPath || strings.Contains(repoPath, "..") {
		return "", false
	}
	return repoPath, true
}
