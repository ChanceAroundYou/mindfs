package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func writeLocalLLMuxEnv(t *testing.T, content string) {
	t.Helper()
	configRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	path := filepath.Join(configRoot, "codex", "llmux.env")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCodexChildEnvInjectsLocalCredentialAndKeepsProcessEnv(t *testing.T) {
	writeLocalLLMuxEnv(t, "export LLMUX_API_KEY='file-key' # active\n")
	t.Setenv("LLMUX_API_KEY", "")
	t.Setenv("MINDFS_ENV_SENTINEL", "kept")

	env := codexChildEnv(Definition{})
	if env["LLMUX_API_KEY"] != "file-key" {
		t.Fatalf("LLMUX_API_KEY = %q, want the device-local literal", env["LLMUX_API_KEY"])
	}
	if env["MINDFS_ENV_SENTINEL"] != "kept" {
		t.Fatalf("process environment was dropped: %#v", env["MINDFS_ENV_SENTINEL"])
	}
}

func TestCodexChildEnvKeepsExplicitAgentEnv(t *testing.T) {
	writeLocalLLMuxEnv(t, "LLMUX_API_KEY=file-key\n")
	t.Setenv("LLMUX_API_KEY", "")
	t.Setenv("MINDFS_ENV_SENTINEL", "kept")

	env := codexChildEnv(Definition{Env: map[string]string{"LLMUX_API_KEY": "agent-key"}})
	if env["LLMUX_API_KEY"] != "agent-key" {
		t.Fatalf("LLMUX_API_KEY = %q, want the explicit agent env to win", env["LLMUX_API_KEY"])
	}
}

func TestCodexChildEnvMergesProcessEnvWhenAgentEnvPresent(t *testing.T) {
	writeLocalLLMuxEnv(t, "LLMUX_API_KEY=file-key\n")
	t.Setenv("LLMUX_API_KEY", "")
	t.Setenv("MINDFS_ENV_SENTINEL", "kept")

	env := codexChildEnv(Definition{Env: map[string]string{"AGENT_ONLY": "1"}})
	if env["LLMUX_API_KEY"] != "file-key" || env["AGENT_ONLY"] != "1" || env["MINDFS_ENV_SENTINEL"] != "kept" {
		t.Fatalf("merged env missing entries: %#v", env)
	}
}

func TestCodexChildEnvReturnsNilWithoutCredential(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("LLMUX_API_KEY", "")

	if env := codexChildEnv(Definition{}); env != nil {
		t.Fatalf("codexChildEnv without credentials = %#v, want nil so the SDK inherits the process environment", env)
	}
}

func TestCodexChildEnvIgnoresDynamicOrEmptyCredential(t *testing.T) {
	for name, content := range map[string]string{
		"dynamic": `LLMUX_API_KEY="$OTHER_KEY"` + "\n",
		"empty":   "LLMUX_API_KEY=\n",
	} {
		t.Run(name, func(t *testing.T) {
			writeLocalLLMuxEnv(t, content)
			t.Setenv("LLMUX_API_KEY", "")
			if env := codexChildEnv(Definition{}); env != nil {
				t.Fatalf("codexChildEnv used an unusable credential: %#v", env)
			}
		})
	}
}
