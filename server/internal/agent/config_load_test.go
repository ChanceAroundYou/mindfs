package agent

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestLoadConfigReadsShells(t *testing.T) {
	cfg := loadPoolTestConfig(t)
	var want []Shell
	if runtime.GOOS == "windows" {
		want = []Shell{{Command: "pwsh", Args: []string{"-NoLogo", "-NoProfile", "-Command"}, LongShellArgs: []string{"-NoLogo", "-NoProfile"}, CommandPrefix: windowsPowerShellCommandPrefix(), OS: []string{"windows"}}}
	} else {
		want = []Shell{
			{Command: "zsh", Args: []string{"-ic"}, LongShellArgs: []string{}, OS: []string{"darwin", "linux"}},
			{Command: "bash", Args: []string{"-ic"}, LongShellArgs: []string{}, OS: []string{"darwin", "linux"}},
			{Command: "sh", Args: []string{"-lc"}, LongShellArgs: []string{}, OS: []string{"darwin", "linux"}},
		}
	}
	if got := cfg.Shells; !reflect.DeepEqual(got, want) {
		t.Fatalf("shells = %#v, want %#v", got, want)
	}
}

func TestNormalizeConfigFiltersShellsByOS(t *testing.T) {
	cfg, err := normalizeConfig(Config{
		Shells: []Shell{
			{Command: "zsh", Args: []string{"-ic"}, OS: []string{"darwin", "linux"}},
			{Command: "pwsh", Args: []string{"-NoLogo", "-NoProfile", "-Command"}, OS: []string{"windows"}},
			{Command: "portable", Args: []string{"-c"}},
		},
		Agents: []Definition{{Name: "codex", Command: "codex"}},
	})
	if err != nil {
		t.Fatalf("normalizeConfig: %v", err)
	}
	for _, shell := range cfg.Shells {
		if len(shell.OS) == 0 {
			continue
		}
		matched := false
		for _, value := range shell.OS {
			if value == runtime.GOOS {
				matched = true
			}
		}
		if !matched {
			t.Fatalf("shell %q with os %#v should have been filtered on %s", shell.Command, shell.OS, runtime.GOOS)
		}
	}
}

func TestLoadConfigReadsOMPAgent(t *testing.T) {
	cfg := loadPoolTestConfig(t)
	def, ok := cfg.GetAgent("omp")
	if !ok {
		t.Fatalf("expected omp in test agents.json")
	}
	if def.Command != "omp" || def.Protocol != ProtocolACP {
		t.Fatalf("omp definition = command %q protocol %q", def.Command, def.Protocol)
	}
	if len(def.Args) != 1 || def.Args[0] != "acp" {
		t.Fatalf("omp args = %#v", def.Args)
	}
}

func TestLoadConfigReadsCodeBuddyAgent(t *testing.T) {
	cfg := loadPoolTestConfig(t)
	def, ok := cfg.GetAgent("CodeBuddy")
	if !ok {
		t.Fatalf("expected CodeBuddy in test agents.json")
	}
	if def.Command != "codebuddy" || def.Protocol != ProtocolACP {
		t.Fatalf("CodeBuddy definition = command %q protocol %q", def.Command, def.Protocol)
	}
	if len(def.Args) != 1 || def.Args[0] != "--acp" {
		t.Fatalf("CodeBuddy args = %#v", def.Args)
	}
}

func TestMergeConfigsKeepsBundledAgentsAndAppliesUserOverrides(t *testing.T) {
	base := Config{
		Shells: []Shell{{Command: "zsh", Args: []string{"-ic"}}, {Command: "bash", Args: []string{"-ic"}}},
		Agents: []Definition{
			{
				Name:            "codex",
				Brief:           "bundled brief",
				Command:         "codex",
				Protocol:        ProtocolCodexSDK,
				InstallCommands: LifecycleCommands{"install codex"},
				UpdateCommands:  LifecycleCommands{"update codex"},
				ConfigBackup:    ConfigBackupDefaults{FileSources: []string{"~/.codex/auth.json"}, EnvKeys: []string{"CODEX_HOME"}},
			},
			{Name: "new-agent", Command: "new-agent", Protocol: ProtocolACP},
		},
	}
	override := Config{
		Shells: []Shell{{Command: "fish", Args: []string{"-i", "-c"}}, {Command: "zsh", Args: []string{"-ic"}}},
		Agents: []Definition{
			{Name: "codex", Command: "custom-codex", Protocol: ProtocolCodexSDK, Args: []string{"--profile", "work"}},
			{Name: "local-agent", Command: "local-agent", Protocol: ProtocolACP},
		},
	}

	cfg := mergeConfigs(base, override)
	wantShells := []Shell{
		{Command: "fish", Args: []string{"-i", "-c"}},
		{Command: "zsh", Args: []string{"-ic"}},
		{Command: "bash", Args: []string{"-ic"}},
	}
	if !reflect.DeepEqual(cfg.Shells, wantShells) {
		t.Fatalf("shells = %#v, want %#v", cfg.Shells, wantShells)
	}
	if len(cfg.Agents) != 3 {
		t.Fatalf("agents length = %d, want 3", len(cfg.Agents))
	}
	codex, ok := cfg.GetAgent("codex")
	if !ok {
		t.Fatalf("expected codex")
	}
	if codex.Command != "custom-codex" || len(codex.Args) != 2 {
		t.Fatalf("codex override not applied: %+v", codex)
	}
	if codex.Brief != "bundled brief" {
		t.Fatalf("codex brief = %q, want bundled brief", codex.Brief)
	}
	if !reflect.DeepEqual(codex.InstallCommands, LifecycleCommands{"install codex"}) {
		t.Fatalf("codex install commands = %#v", codex.InstallCommands)
	}
	if !reflect.DeepEqual(codex.UpdateCommands, LifecycleCommands{"update codex"}) {
		t.Fatalf("codex update commands = %#v", codex.UpdateCommands)
	}
	if !reflect.DeepEqual(codex.ConfigBackup.FileSources, []string{"~/.codex/auth.json"}) {
		t.Fatalf("codex config backup file sources = %#v", codex.ConfigBackup.FileSources)
	}
	if !reflect.DeepEqual(codex.ConfigBackup.EnvKeys, []string{"CODEX_HOME"}) {
		t.Fatalf("codex config backup env keys = %#v", codex.ConfigBackup.EnvKeys)
	}
	if _, ok := cfg.GetAgent("new-agent"); !ok {
		t.Fatalf("expected bundled new-agent to be preserved")
	}
	if _, ok := cfg.GetAgent("local-agent"); !ok {
		t.Fatalf("expected user local-agent to be appended")
	}
}

func TestLoadConfigFiltersLifecycleCommandsByOS(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "agents.json")
	otherOS := "linux"
	if runtime.GOOS == "linux" {
		otherOS = "darwin"
	}
	payload := `{
  "agents": [
    {
      "name": "codex",
      "command": "codex",
      "installCommands": [
        "legacy install",
        {"os": "` + runtime.GOOS + `", "command": "current install"},
        {"os": "` + otherOS + `", "command": "other install"}
      ],
      "updateCommands": [
        {"os": ["` + runtime.GOOS + `"], "command": "current update"},
        {"os": ["` + otherOS + `"], "command": "other update"}
      ]
    }
  ]
}`
	if err := os.WriteFile(configPath, []byte(payload), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	def, ok := cfg.GetAgent("codex")
	if !ok {
		t.Fatalf("expected codex")
	}
	if !reflect.DeepEqual(def.InstallCommands, LifecycleCommands{"legacy install", "current install"}) {
		t.Fatalf("install commands = %#v", def.InstallCommands)
	}
	if !reflect.DeepEqual(def.UpdateCommands, LifecycleCommands{"current update"}) {
		t.Fatalf("update commands = %#v", def.UpdateCommands)
	}
}

func TestInstalledDefaultConfigPathPrefersExecutableDirectory(t *testing.T) {
	tempDir := t.TempDir()
	exeDir := filepath.Join(tempDir, "archive")
	if err := os.MkdirAll(exeDir, 0o755); err != nil {
		t.Fatalf("mkdir exe dir: %v", err)
	}
	configPath := filepath.Join(exeDir, "agents.json")
	if err := os.WriteFile(configPath, []byte(`{"agents":[{"name":"zip-agent","command":"zip-agent"}]}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	got := installedDefaultConfigPathFromExecutable(filepath.Join(exeDir, "mindfs.exe"))
	if got != configPath {
		t.Fatalf("installedDefaultConfigPathFromExecutable() = %q, want %q", got, configPath)
	}
}

func TestInstalledDefaultConfigPathFallsBackToInstalledLayout(t *testing.T) {
	tempDir := t.TempDir()
	exeDir := filepath.Join(tempDir, "bin")
	want := filepath.Join(tempDir, "share", "mindfs", "agents.json")

	got := installedDefaultConfigPathFromExecutable(filepath.Join(exeDir, "mindfs.exe"))
	if got != want {
		t.Fatalf("installedDefaultConfigPathFromExecutable() = %q, want %q", got, want)
	}
}

func TestLoadConfigPrefersAgentsConfigEnv(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "agents.json")
	if err := os.WriteFile(configPath, []byte(`{"agents":[{"name":"env-agent","command":"env-agent"}]}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv(configPathEnvKey, configPath)

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if _, ok := cfg.GetAgent("env-agent"); !ok {
		t.Fatalf("expected env-agent from %s", configPathEnvKey)
	}
}

func TestLoadConfigWithExtraMergesSingleExtraConfigAfterDefaultConfig(t *testing.T) {
	tempDir := t.TempDir()
	userConfigPath := filepath.Join(tempDir, "user-agents.json")
	extraConfigPath := filepath.Join(tempDir, "extra-agents.json")
	if err := os.WriteFile(userConfigPath, []byte(`{
  "tokenStationURL": "https://token.user.example.com",
  "agents": [
    {"name":"user-agent","command":"user-agent","brief":"from user"},
    {"name":"shared-agent","command":"user-shared","brief":"from user"}
  ]
}`), 0o644); err != nil {
		t.Fatalf("write user config: %v", err)
	}
	if err := os.WriteFile(extraConfigPath, []byte(`{
  "tokenStationURL": "https://token.extra.example.com",
  "agents": [
    {"name":"extra-agent","command":"extra-agent","brief":"from extra"},
    {"name":"shared-agent","command":"extra-shared","brief":"from extra"}
  ]
}`), 0o644); err != nil {
		t.Fatalf("write extra config: %v", err)
	}
	t.Setenv(configPathEnvKey, userConfigPath)

	cfg, err := LoadConfigWithExtra(extraConfigPath)
	if err != nil {
		t.Fatalf("LoadConfigWithExtra failed: %v", err)
	}
	if _, ok := cfg.GetAgent("user-agent"); !ok {
		t.Fatalf("expected user-agent to remain")
	}
	if _, ok := cfg.GetAgent("extra-agent"); !ok {
		t.Fatalf("expected extra-agent to be appended")
	}
	shared, ok := cfg.GetAgent("shared-agent")
	if !ok {
		t.Fatalf("expected shared-agent")
	}
	if shared.Command != "extra-shared" || shared.Brief != "from extra" {
		t.Fatalf("extra config should override same-name user config, got %+v", shared)
	}
	if cfg.TokenStationURL != "https://token.extra.example.com" {
		t.Fatalf("token station url = %q", cfg.TokenStationURL)
	}
}
