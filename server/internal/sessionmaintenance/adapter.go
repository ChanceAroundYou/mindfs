package sessionmaintenance

import (
	"os"
	"path/filepath"
	"strings"
)

// Options 允许测试注入目录；零值走真实 harness 默认位置。
type Options struct {
	ClaudeProjectsDir string
	ClaudeLocksDir    string
	CodexSessionsDir  string
	// KeepBackups 为 nil 或 true 时在改写真转录前留 .bak；只有显式 false 才不留。
	// 用指针而不是 bool：零值 Options 在测试与未配置生产里都必须保留备份。
	KeepBackups *bool
}

// keepBackup 是「保留备份」这一安全默认值的唯一判据。
func (o Options) keepBackup() bool {
	return o.KeepBackups == nil || *o.KeepBackups
}

// Adapter 描述一种 harness 在本机留下的文件形态。
type Adapter interface {
	Harness() string
	TranscriptPaths(entry Entry, opts Options) ([]string, error)
	LocksDir(opts Options) string
	SupportsImages() bool
	// SupportsPurge 只给「能可靠判定空会话」的 harness 开删除能力。
	// Codex rollout 的记录形状与 Claude 不同，套用同一套启发式会误删完整会话。
	SupportsPurge() bool
}

// ForHarness 取 harness 适配器。未知 harness 返回 nil，调用方必须显式报不支持。
func ForHarness(agent string) Adapter {
	switch strings.ToLower(strings.TrimSpace(agent)) {
	case "claude":
		return claudeAdapter{}
	case "codex":
		return codexAdapter{}
	default:
		return nil
	}
}

func defaultClaudeProjectsDir() string {
	if env := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); env != "" {
		return filepath.Join(env, "projects")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "projects")
}

func defaultClaudeLocksDir() string {
	if env := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); env != "" {
		return filepath.Join(env, "sessions")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "sessions")
}

func defaultCodexSessionsDir() string {
	homeDir := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if homeDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		homeDir = filepath.Join(home, ".codex")
	}
	return filepath.Join(homeDir, "sessions")
}
