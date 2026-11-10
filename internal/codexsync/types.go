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
}

type Result struct {
	State         string   `json:"state"`
	CommitIDs     []string `json:"commit_ids,omitempty"`
	LastAttemptAt string   `json:"last_attempt_at,omitempty"`
	LastSuccessAt string   `json:"last_success_at,omitempty"`
	Message       string   `json:"message,omitempty"`
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
	}, nil
}

func Run(ctx context.Context, mode Mode) (Result, error) {
	paths, err := ResolvePaths()
	if err != nil {
		return Result{State: "unavailable", Message: "could not resolve local configuration paths"}, err
	}
	return RunAt(ctx, mode, paths)
}
