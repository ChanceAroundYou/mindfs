package agent

import (
	"encoding/json"
	"strings"
)

const configPathEnvKey = "MINDFS_AGENTS_CONFIG"

// Config holds all agent configurations.
type Config struct {
	Agents          []Definition `json:"agents"`
	Shells          []Shell      `json:"shells,omitempty"`
	TokenStationURL string       `json:"tokenStationURL,omitempty"`
}

type Shell struct {
	Name          string   `json:"name,omitempty"`
	Command       string   `json:"command"`
	Args          []string `json:"args,omitempty"`
	LongShellArgs []string `json:"longShellArgs,omitempty"`
	CommandPrefix string   `json:"commandPrefix,omitempty"`
	OS            []string `json:"os,omitempty"`
}

func (s *Shell) UnmarshalJSON(payload []byte) error {
	var rawString string
	if err := json.Unmarshal(payload, &rawString); err == nil {
		s.Command = rawString
		s.Args = nil
		s.OS = nil
		return nil
	}
	var raw struct {
		Name          string      `json:"name"`
		Command       string      `json:"command"`
		Args          []string    `json:"args"`
		LongShellArgs []string    `json:"longShellArgs"`
		CommandPrefix string      `json:"commandPrefix"`
		OS            interface{} `json:"os"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return err
	}
	s.Name = raw.Name
	s.Command = raw.Command
	s.Args = raw.Args
	s.LongShellArgs = raw.LongShellArgs
	s.CommandPrefix = raw.CommandPrefix
	s.OS = parseShellOS(raw.OS)
	return nil
}

// Definition defines how to spawn and communicate with an agent.
type Definition struct {
	// Name is the logical agent name (e.g. codex/claude/gemini).
	Name string `json:"name"`

	// Brief describes the agent's strengths for UI lists.
	Brief string `json:"brief,omitempty"`

	// Command is the executable name or path.
	Command string `json:"command"`

	// Protocol specifies the communication protocol (stream-json, acp, mcp).
	// If empty, defaults based on agent name.
	Protocol Protocol `json:"protocol,omitempty"`

	// Args are base arguments always passed to the command.
	Args []string `json:"args,omitempty"`

	// Env are additional environment variables.
	Env map[string]string `json:"env,omitempty"`

	// ConfigBackup stores default inputs for config backup flows.
	ConfigBackup ConfigBackupDefaults `json:"configBackup,omitempty"`

	// InstallCommands are shell commands used to install this agent.
	InstallCommands LifecycleCommands `json:"installCommands,omitempty"`

	// UpdateCommands are shell commands used to update this agent.
	UpdateCommands LifecycleCommands `json:"updateCommands,omitempty"`

	// CwdTemplate is the working directory template ({root} is replaced).
	CwdTemplate string `json:"cwdTemplate,omitempty"`

	// ProbeArgs are arguments for availability check.
	ProbeArgs []string `json:"probeArgs,omitempty"`
}

type ConfigBackupDefaults struct {
	FileSources []string `json:"fileSources,omitempty"`
	EnvKeys     []string `json:"envKeys,omitempty"`
}

type LifecycleCommands []string

func (c *LifecycleCommands) UnmarshalJSON(payload []byte) error {
	var items []json.RawMessage
	if err := json.Unmarshal(payload, &items); err != nil {
		return err
	}
	commands := make(LifecycleCommands, 0, len(items))
	for _, item := range items {
		var rawString string
		if err := json.Unmarshal(item, &rawString); err == nil {
			if trimmed := strings.TrimSpace(rawString); trimmed != "" {
				commands = append(commands, trimmed)
			}
			continue
		}
		var raw struct {
			Command string      `json:"command"`
			OS      interface{} `json:"os"`
		}
		if err := json.Unmarshal(item, &raw); err != nil {
			return err
		}
		command := strings.TrimSpace(raw.Command)
		if command == "" || !lifecycleCommandMatchesCurrentOS(parseShellOS(raw.OS)) {
			continue
		}
		commands = append(commands, command)
	}
	*c = normalizeCommandList(commands)
	return nil
}
