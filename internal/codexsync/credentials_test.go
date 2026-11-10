package codexsync

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLocalLLMuxCredentialAvailableChecksEnvironmentAndLocalEnvFile(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	t.Setenv("LLMUX_API_KEY", "")
	envFile := filepath.Join(configRoot, "codex", "llmux.env")
	if err := os.MkdirAll(filepath.Dir(envFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(envFile, []byte("# local credentials\nOTHER_KEY=value\nLLMUX_API_KEY=\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if LocalLLMuxCredentialAvailable() {
		t.Fatal("empty local key was reported available")
	}
	if err := os.WriteFile(envFile, []byte("export LLMUX_API_KEY='local-secret'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !LocalLLMuxCredentialAvailable() {
		t.Fatal("quoted local key was not detected")
	}
	if err := os.Remove(envFile); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LLMUX_API_KEY", "environment-secret")
	if !LocalLLMuxCredentialAvailable() {
		t.Fatal("environment key was not detected")
	}
}

func TestLocalLLMuxCredentialAvailableDoesNotFollowEnvFileSymlink(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	t.Setenv("LLMUX_API_KEY", "")
	outside := filepath.Join(t.TempDir(), "llmux.env")
	if err := os.WriteFile(outside, []byte("LLMUX_API_KEY=local-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(configRoot, "codex", "llmux.env")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if LocalLLMuxCredentialAvailable() {
		t.Fatal("symlinked env file was reported as local credentials")
	}
}

func TestLocalLLMuxCredentialAvailableUsesLastLiteralShellAssignment(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	t.Setenv("LLMUX_API_KEY", "inherited-key")
	envFile := filepath.Join(configRoot, "codex", "llmux.env")
	if err := os.MkdirAll(filepath.Dir(envFile), 0o700); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name        string
		content     string
		environment string
		want        bool
	}{
		{name: "empty with inline comment overrides inherited value", content: "LLMUX_API_KEY= # empty\n", want: false},
		{name: "empty with adjacent inline comment overrides inherited value", content: "LLMUX_API_KEY= #comment\n", want: false},
		{name: "text after assignment whitespace is not its value", content: "LLMUX_API_KEY= file-key\n", want: false},
		{name: "last empty assignment wins", content: "LLMUX_API_KEY='file-key'\nLLMUX_API_KEY= # cleared\n", want: false},
		{name: "last non-empty assignment wins", content: "LLMUX_API_KEY= # cleared\nexport LLMUX_API_KEY='file-key' # active\n", want: true},
		{name: "quoted hash is part of the value", content: "LLMUX_API_KEY='value#part' # comment\n", want: true},
		{name: "unquoted hash without whitespace is literal", content: "LLMUX_API_KEY=value#part\n", want: true},
		{name: "dynamic expansion is not evaluated", content: "LLMUX_API_KEY=\"$OTHER_KEY\"\n", want: false},
		{name: "spaces before equals are not shell assignments", content: "LLMUX_API_KEY =file-key\n", want: false},
		{name: "export with spaces before equals is not an assignment", content: "export LLMUX_API_KEY =file-key\n", want: false},
		{name: "no assignment preserves inherited value", content: "# only a comment\nOTHER_KEY=local\n", environment: "inherited-key", want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LLMUX_API_KEY", tc.environment)
			if err := os.WriteFile(envFile, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := LocalLLMuxCredentialAvailable(); got != tc.want {
				t.Fatalf("LocalLLMuxCredentialAvailable() = %t, want %t", got, tc.want)
			}
		})
	}
}
