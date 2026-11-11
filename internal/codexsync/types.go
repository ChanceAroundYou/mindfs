package codexsync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

type Mode string

const (
	ModeCheck        Mode = "check"
	ModeApply        Mode = "apply"
	ModeSync         Mode = "sync"
	ModeRender       Mode = "render"
	ModeCheckRuntime Mode = "check-runtime"
)

type Paths struct {
	AgentsRoot      string
	CodexHome       string
	MindFSConfig    string
	SharedDefault   string
	SharedLLMux     string
	SharedProviders string
	RuntimeConfig   string
	RuntimeLLMux    string
	SnapshotsRoot   string
	DefaultSnapshot string
	LLMuxSnapshot   string
	LegacyProviders string
	LocalSecrets    string
	StateDir        string

	// Claude 共享配置与运行时文件。共享目录在 .agents 下，运行时在
	// ${CLAUDE_CONFIG_DIR:-$HOME/.claude}；~/.claude.json 是设备本地文件，只合并
	// 其中的 mcpServers 键（与 .agents/claude/scripts/render.sh 的语义一致）。
	ClaudeHome            string
	SharedClaude          string
	SharedClaudeSettings  string
	SharedClaudeMCP       string
	ClaudeLocalSettings   string
	ClaudeRuntimeSettings string
	ClaudeRuntimeMCP      string
	ClaudeJSON            string
}

// HarnessInfo 描述一个 harness 的共享同步可用性。不可用时 Message 说明原因，
// 前端据此渲染「为什么这里没有同步」而不是把入口藏起来。
type HarnessInfo struct {
	Name      string `json:"name"`
	Available bool   `json:"available"`
	SharedDir string `json:"shared_dir,omitempty"`
	Message   string `json:"message,omitempty"`
}

type Result struct {
	State         string        `json:"state"`
	CommitIDs     []string      `json:"commit_ids,omitempty"`
	LastAttemptAt string        `json:"last_attempt_at,omitempty"`
	LastSuccessAt string        `json:"last_success_at,omitempty"`
	Message       string        `json:"message,omitempty"`
	Harnesses     []HarnessInfo `json:"harnesses,omitempty"`
}

func ResolvePaths() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}
	configRoot, err := os.UserConfigDir()
	if err != nil {
		return Paths{}, err
	}
	agentsRoot := strings.TrimSpace(os.Getenv("AGENTS_ROOT"))
	if agentsRoot == "" {
		agentsRoot = filepath.Join(home, ".agents")
	}
	codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if codexHome == "" {
		codexHome = filepath.Join(home, ".codex")
	}
	mindfsConfig := filepath.Join(configRoot, "mindfs")
	snapshotsRoot := filepath.Join(mindfsConfig, "agents-config")
	sharedCodex := filepath.Join(agentsRoot, "codex")
	sharedClaude := filepath.Join(agentsRoot, "claude")
	claudeHome := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR"))
	if claudeHome == "" {
		claudeHome = filepath.Join(home, ".claude")
	}
	return Paths{
		AgentsRoot:      agentsRoot,
		CodexHome:       codexHome,
		MindFSConfig:    mindfsConfig,
		SharedDefault:   filepath.Join(sharedCodex, "config.toml"),
		SharedLLMux:     filepath.Join(sharedCodex, "profiles", "llmux.config.toml"),
		SharedProviders: filepath.Join(sharedCodex, "providers.json"),
		RuntimeConfig:   filepath.Join(codexHome, "config.toml"),
		RuntimeLLMux:    filepath.Join(codexHome, "llmux.config.toml"),
		SnapshotsRoot:   snapshotsRoot,
		DefaultSnapshot: filepath.Join(snapshotsRoot, "codex-default", "002-config.toml"),
		LLMuxSnapshot:   filepath.Join(snapshotsRoot, "codex-llmux", "002-config.toml"),
		LegacyProviders: filepath.Join(mindfsConfig, "api-providers.json"),
		LocalSecrets:    filepath.Join(mindfsConfig, "api-provider-secrets.json"),
		StateDir:        filepath.Join(mindfsConfig, "agents-config-sync"),

		ClaudeHome:            claudeHome,
		SharedClaude:          sharedClaude,
		SharedClaudeSettings:  filepath.Join(sharedClaude, "settings.shared.json"),
		SharedClaudeMCP:       filepath.Join(sharedClaude, "mcp.shared.json"),
		ClaudeLocalSettings:   filepath.Join(claudeHome, "settings.local.json"),
		ClaudeRuntimeSettings: filepath.Join(claudeHome, "settings.json"),
		ClaudeRuntimeMCP:      filepath.Join(claudeHome, ".mcp.json"),
		ClaudeJSON:            filepath.Join(home, ".claude.json"),
	}, nil
}

func Run(ctx context.Context, mode Mode) (Result, error) {
	paths, err := ResolvePaths()
	if err != nil {
		return Result{State: "unavailable", Message: "could not resolve local configuration paths"}, err
	}
	return RunAt(ctx, mode, paths)
}
