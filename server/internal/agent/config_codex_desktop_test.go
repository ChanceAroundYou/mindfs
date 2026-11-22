package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func makeDesktopCodexFile(t *testing.T, programFiles, packageName string) string {
	t.Helper()
	path := filepath.Join(programFiles, "WindowsApps", packageName, "app", "resources", "codex.exe")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir desktop codex dir: %v", err)
	}
	if err := os.WriteFile(path, []byte("fake codex"), 0o755); err != nil {
		t.Fatalf("write desktop codex: %v", err)
	}
	return path
}

func TestPreferWindowsDesktopCodexReplacesStalePluginBinary(t *testing.T) {
	programFiles := t.TempDir()
	desktop := makeDesktopCodexFile(t, programFiles, "OpenAI.Codex_26.1117.4957.0_x64__abc")
	cfg := Config{Agents: []Definition{{
		Name:     "codex",
		Protocol: ProtocolCodexSDK,
		Command:  `C:\Users\10178\.codex\plugins\.plugin-appserver\codex.exe`,
	}}}

	preferWindowsDesktopCodex(&cfg, "windows", programFiles)

	if got := cfg.Agents[0].Command; got != desktop {
		t.Fatalf("codex command = %q, want desktop %q", got, desktop)
	}
}

func TestPreferWindowsDesktopCodexPicksNewestPackage(t *testing.T) {
	programFiles := t.TempDir()
	makeDesktopCodexFile(t, programFiles, "OpenAI.Codex_25.1000.0_x64__old")
	newest := makeDesktopCodexFile(t, programFiles, "OpenAI.Codex_26.1117.4957.0_x64__new")
	cfg := Config{Agents: []Definition{{
		Name:     "codex",
		Protocol: ProtocolCodexSDK,
		Command:  "codex",
	}}}

	preferWindowsDesktopCodex(&cfg, "windows", programFiles)

	if got := cfg.Agents[0].Command; got != newest {
		t.Fatalf("codex command = %q, want newest %q", got, newest)
	}
}

func TestPreferWindowsDesktopCodexDropsStalePluginWithoutDesktop(t *testing.T) {
	cfg := Config{Agents: []Definition{{
		Name:     "codex",
		Protocol: ProtocolCodexSDK,
		Command:  `C:\Users\10178\.codex\plugins\.plugin-appserver\codex.exe`,
	}}}

	preferWindowsDesktopCodex(&cfg, "windows", t.TempDir())

	if got := cfg.Agents[0].Command; got != "codex" {
		t.Fatalf("codex command = %q, want PATH fallback %q", got, "codex")
	}
}

func TestPreferWindowsDesktopCodexIgnoresNonWindows(t *testing.T) {
	programFiles := t.TempDir()
	makeDesktopCodexFile(t, programFiles, "OpenAI.Codex_26.1117.4957.0_x64__abc")
	cfg := Config{Agents: []Definition{{
		Name:     "codex",
		Protocol: ProtocolCodexSDK,
		Command:  `C:\Users\10178\.codex\plugins\.plugin-appserver\codex.exe`,
	}}}

	preferWindowsDesktopCodex(&cfg, "linux", programFiles)

	if got := cfg.Agents[0].Command; !isStalePluginCodexCommand(got) {
		t.Fatalf("non-Windows command changed to %q", got)
	}
}

func TestPreferWindowsDesktopCodexKeepsExplicitCustomCommand(t *testing.T) {
	programFiles := t.TempDir()
	makeDesktopCodexFile(t, programFiles, "OpenAI.Codex_26.1117.4957.0_x64__abc")
	const custom = `C:\tools\my-codex.exe`
	cfg := Config{Agents: []Definition{{
		Name:     "codex",
		Protocol: ProtocolCodexSDK,
		Command:  custom,
	}}}

	preferWindowsDesktopCodex(&cfg, "windows", programFiles)

	if got := cfg.Agents[0].Command; got != custom {
		t.Fatalf("custom command = %q, want %q", got, custom)
	}
}

func TestPreferWindowsDesktopCodexLeavesOtherAgentsAlone(t *testing.T) {
	programFiles := t.TempDir()
	makeDesktopCodexFile(t, programFiles, "OpenAI.Codex_26.1117.4957.0_x64__abc")
	const claude = `C:\Users\10178\.local\bin\claude.exe`
	cfg := Config{Agents: []Definition{
		{Name: "codex", Protocol: ProtocolCodexSDK, Command: "codex"},
		{Name: "claude", Protocol: ProtocolClaudeSDK, Command: claude},
	}}

	preferWindowsDesktopCodex(&cfg, "windows", programFiles)

	if got := cfg.Agents[1].Command; got != claude {
		t.Fatalf("claude command = %q, want %q", got, claude)
	}
}
