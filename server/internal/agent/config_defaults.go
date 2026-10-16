package agent

import (
	"runtime"
)

// defaultConfig returns built-in agent definitions.
func defaultConfig() Config {
	shells := []Shell{
		{Command: "zsh", Args: []string{"-ic"}, LongShellArgs: []string{}},
		{Command: "bash", Args: []string{"-ic"}, LongShellArgs: []string{}},
		{Command: "sh", Args: []string{"-lc"}, LongShellArgs: []string{}},
	}
	if runtime.GOOS == "windows" {
		shells = []Shell{
			{Command: "pwsh", Args: []string{"-NoLogo", "-NoProfile", "-Command"}, LongShellArgs: []string{"-NoLogo", "-NoProfile"}, CommandPrefix: windowsPowerShellCommandPrefix()},
			{Name: "ps", Command: "powershell.exe", Args: []string{"-NoLogo", "-NoProfile", "-Command"}, LongShellArgs: []string{"-NoLogo", "-NoProfile"}, CommandPrefix: windowsPowerShellCommandPrefix()},
			{Command: "bash.exe", Args: []string{"-lc"}, LongShellArgs: []string{}},
			{Command: "wsl.exe", Args: []string{"--exec", "bash", "-lc"}, LongShellArgs: []string{"--exec", "bash"}},
			{Command: "cmd.exe", Args: []string{"/D", "/S", "/C"}, LongShellArgs: []string{"/Q", "/K"}, CommandPrefix: "chcp 65001 >NUL &"},
		}
	}
	return Config{
		Shells: shells,
		Agents: []Definition{
			{
				Name:     "claude",
				Command:  "claude",
				Protocol: ProtocolClaudeSDK,
			},
			{
				Name:     "gemini",
				Command:  "gemini",
				Protocol: ProtocolACP,
				Args:     []string{"--experimental-acp"},
			},
			{
				Name:     "codex",
				Command:  "codex",
				Protocol: ProtocolCodexSDK,
			},
		},
	}
}

func windowsPowerShellCommandPrefix() string {
	return "[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false); [Console]::InputEncoding = [Console]::OutputEncoding; $OutputEncoding = [Console]::OutputEncoding;"
}
