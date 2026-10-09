package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	configpkg "mindfs/server/internal/config"
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

// LoadConfig loads agent configuration from the given path or default location.
func LoadConfig(path string) (Config, error) {
	if path != "" {
		return loadConfigFile(path)
	}

	resolved, err := defaultConfigPath()
	if err != nil {
		return Config{}, err
	}
	baseCfg, basePath, err := loadInstalledDefaultConfig()
	if err != nil {
		return Config{}, err
	}
	if samePath(resolved, basePath) {
		return baseCfg, nil
	}

	userCfg, err := loadConfigFile(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return baseCfg, nil
		}
		return Config{}, err
	}
	return mergeConfigs(baseCfg, userCfg), nil
}

// LoadUserConfig 只读用户层那一份（ResolveConfigPath），**不**叠加安装自带的定义。
//
// 写用户层的地方必须用它。那些写入点原本读 LoadConfig("")（= 安装自带 ⊕ 用户层），
// 改一两个字段后把整份合并结果写回用户层，于是安装自带的定义被整份冻进用户层，
// 上游之后改 agents.json 永远被这层旧快照盖住 —— dsh 的 installCommands 就这样
// 一直停在坏的版本上。
func LoadUserConfig() (Config, error) {
	path, err := ResolveConfigPath()
	if err != nil {
		return Config{}, err
	}
	cfg, err := loadConfigFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, nil
		}
		return Config{}, err
	}
	return cfg, nil
}

// SaveUserConfig 把用户层写回 ResolveConfigPath()。
func SaveUserConfig(cfg Config) error {
	path, err := ResolveConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	payload, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func LoadConfigWithExtra(extraPath string) (Config, error) {
	cfg, err := LoadConfig("")
	if err != nil {
		return Config{}, err
	}
	extraPath = strings.TrimSpace(extraPath)
	if extraPath == "" {
		return cfg, nil
	}
	extraCfg, err := loadConfigFile(extraPath)
	if err != nil {
		return Config{}, err
	}
	return mergeConfigs(cfg, extraCfg), nil
}

func loadConfigFile(path string) (Config, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	return DecodeConfig(payload)
}

func DecodeConfig(payload []byte) (Config, error) {
	var cfg Config
	if err := json.Unmarshal(payload, &cfg); err != nil {
		return Config{}, err
	}
	return normalizeConfig(cfg)
}

func MergeHostedConfig(hosted Config, local Config) Config {
	return mergeConfigs(hosted, local)
}

func loadInstalledDefaultConfig() (Config, string, error) {
	if fallbackPath, fallbackErr := installedDefaultConfigPath(); fallbackErr == nil {
		if cfg, err := loadConfigFile(fallbackPath); err == nil {
			return cfg, fallbackPath, nil
		} else if !os.IsNotExist(err) {
			return Config{}, "", err
		}
	}
	cfg, err := normalizeConfig(defaultConfig())
	return cfg, "", err
}

func normalizeConfig(cfg Config) (Config, error) {
	cfg.TokenStationURL = strings.TrimSpace(cfg.TokenStationURL)
	shells := make([]Shell, 0, len(cfg.Shells))
	for _, shell := range cfg.Shells {
		if trimmed := strings.TrimSpace(shell.Command); trimmed != "" {
			shell.Name = strings.TrimSpace(shell.Name)
			shell.Command = trimmed
			shell.CommandPrefix = strings.TrimSpace(shell.CommandPrefix)
			shell.OS = normalizeShellOS(shell.OS)
			if !shellMatchesCurrentOS(shell) {
				continue
			}
			shells = append(shells, shell)
		}
	}
	cfg.Shells = shells
	for i := range cfg.Agents {
		name := strings.TrimSpace(cfg.Agents[i].Name)
		if name == "" {
			return Config{}, fmt.Errorf("agent name required")
		}
		cfg.Agents[i].Name = name
		cfg.Agents[i].Brief = strings.TrimSpace(cfg.Agents[i].Brief)
		if cfg.Agents[i].Protocol == "" {
			cfg.Agents[i].Protocol = DefaultProtocol(name)
		}
		cfg.Agents[i].InstallCommands = normalizeCommandList(cfg.Agents[i].InstallCommands)
		cfg.Agents[i].UpdateCommands = normalizeCommandList(cfg.Agents[i].UpdateCommands)
	}
	return cfg, nil
}

func normalizeCommandList(commands LifecycleCommands) LifecycleCommands {
	normalized := make(LifecycleCommands, 0, len(commands))
	for _, command := range commands {
		if trimmed := strings.TrimSpace(command); trimmed != "" {
			normalized = append(normalized, trimmed)
		}
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func mergeConfigs(base Config, override Config) Config {
	merged := Config{
		Agents:          append([]Definition(nil), base.Agents...),
		Shells:          append([]Shell(nil), base.Shells...),
		TokenStationURL: base.TokenStationURL,
	}
	if len(override.Shells) > 0 {
		merged.Shells = mergeShells(base.Shells, override.Shells)
	}
	if override.TokenStationURL != "" {
		merged.TokenStationURL = override.TokenStationURL
	}

	agentIndexes := make(map[string]int, len(merged.Agents))
	for i, agent := range merged.Agents {
		agentIndexes[agent.Name] = i
	}
	for _, agent := range override.Agents {
		if index, ok := agentIndexes[agent.Name]; ok {
			merged.Agents[index] = mergeAgentDefinition(merged.Agents[index], agent)
			continue
		}
		agentIndexes[agent.Name] = len(merged.Agents)
		merged.Agents = append(merged.Agents, agent)
	}
	return merged
}

func mergeAgentDefinition(base Definition, override Definition) Definition {
	merged := override
	if merged.Brief == "" {
		merged.Brief = base.Brief
	}
	// 空字段一律回退 base —— 后面这组以前漏了，于是一个只写了 name 的覆盖条目
	// （用户层里为「只配了 env」而补的最小条目）会把上游的 command/args 清成空，
	// agent 直接就起不来了。入库的配置经过 normalizeConfig 时 Protocol 会被填上，
	// 只有内存里现造的条目才可能是空串。
	if merged.Command == "" {
		merged.Command = base.Command
	}
	if merged.Protocol == "" {
		merged.Protocol = base.Protocol
	}
	if len(merged.Args) == 0 {
		merged.Args = append([]string(nil), base.Args...)
	}
	if len(merged.InstallCommands) == 0 {
		merged.InstallCommands = append(LifecycleCommands(nil), base.InstallCommands...)
	}
	if len(merged.UpdateCommands) == 0 {
		merged.UpdateCommands = append(LifecycleCommands(nil), base.UpdateCommands...)
	}
	if len(merged.ConfigBackup.FileSources) == 0 {
		merged.ConfigBackup.FileSources = append([]string(nil), base.ConfigBackup.FileSources...)
	}
	if len(merged.ConfigBackup.EnvKeys) == 0 {
		merged.ConfigBackup.EnvKeys = append([]string(nil), base.ConfigBackup.EnvKeys...)
	}
	if merged.CwdTemplate == "" {
		merged.CwdTemplate = base.CwdTemplate
	}
	if len(merged.ProbeArgs) == 0 {
		merged.ProbeArgs = append([]string(nil), base.ProbeArgs...)
	}
	// Env 也必须回退，否则「后加的层没写 env」会把前面层的 env 清成 nil：
	// 生效的 agents.json 是三层合并出来的（安装自带的那份 + ~/.config 的用户层
	// + -agent-config 指定的 extra），而 extra 通常就是安装自带的那份、不含 env。
	// 用户层里那些按机器配的环境变量（凭据/端点）就会被这一层抹掉 ——
	// 运行期靠 SetAgentEnv 撑着看不出来，重启后才暴露成「agent 认不到 provider」。
	if len(merged.Env) == 0 {
		merged.Env = cloneEnvMap(base.Env)
	}
	return merged
}

// cloneEnvMap 返回一份独立副本：合并结果会被各账户共享并长期持有，
// 不能与解码时的那份共享底层 map。
func cloneEnvMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func mergeShells(base []Shell, override []Shell) []Shell {
	if len(override) == 0 {
		return append([]Shell(nil), base...)
	}
	merged := append([]Shell(nil), override...)
	shellIndexes := make(map[string]int, len(merged))
	for i, shell := range merged {
		if key := shellMergeKey(shell); key != "" {
			shellIndexes[key] = i
		}
	}
	for _, shell := range base {
		key := shellMergeKey(shell)
		if key == "" {
			continue
		}
		if _, ok := shellIndexes[key]; ok {
			continue
		}
		shellIndexes[key] = len(merged)
		merged = append(merged, shell)
	}
	return merged
}

func shellMergeKey(shell Shell) string {
	return strings.ToLower(strings.TrimSpace(shell.Command))
}

func parseShellOS(raw interface{}) []string {
	switch value := raw.(type) {
	case nil:
		return nil
	case string:
		return []string{value}
	case []interface{}:
		out := make([]string, 0, len(value))
		for _, item := range value {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func normalizeShellOS(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.ToLower(strings.TrimSpace(value))
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func shellMatchesCurrentOS(shell Shell) bool {
	if len(shell.OS) == 0 {
		return true
	}
	for _, value := range shell.OS {
		if value == runtime.GOOS {
			return true
		}
	}
	return false
}

func lifecycleCommandMatchesCurrentOS(values []string) bool {
	if len(values) == 0 {
		return true
	}
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), runtime.GOOS) {
			return true
		}
	}
	return false
}

func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA == nil && errB == nil {
		return absA == absB
	}
	return a == b
}

func defaultConfigPath() (string, error) {
	return ResolveConfigPath()
}

func ResolveConfigPath() (string, error) {
	if hinted := strings.TrimSpace(os.Getenv(configPathEnvKey)); hinted != "" {
		return hinted, nil
	}
	configDir, err := configpkg.MindFSConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "agents.json"), nil
}

func installedDefaultConfigPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return installedDefaultConfigPathFromExecutable(exe), nil
}

func installedDefaultConfigPathFromExecutable(exe string) string {
	exeDir := filepath.Dir(exe)
	candidate := filepath.Join(exeDir, "agents.json")
	if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
		return candidate
	}
	return filepath.Join(filepath.Dir(exeDir), "share", "mindfs", "agents.json")
}

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

// EnsureAgent 返回配置里该 agent 的条目指针；没有就补一个只有名字的最小条目。
//
// 最小条目靠 mergeAgentDefinition 的「空字段回退 base」把 command/args/协议等
// 全部交给上游那份供给，所以写用户层时只需写用户自己配的东西（env、configBackup），
// 不必也不该把合并结果整份搬进去。
func (c *Config) EnsureAgent(name string) *Definition {
	for i := range c.Agents {
		if c.Agents[i].Name == name {
			return &c.Agents[i]
		}
	}
	c.Agents = append(c.Agents, Definition{Name: name})
	return &c.Agents[len(c.Agents)-1]
}

// GetAgent returns an agent definition by name.
func (c Config) GetAgent(name string) (Definition, bool) {
	for _, a := range c.Agents {
		if a.Name == name {
			return a, true
		}
	}
	return Definition{}, false
}

// BuildArgs constructs the full argument list for spawning.
func (d Definition) BuildArgs(rootPath string) []string {
	args := append([]string{}, d.Args...)
	if d.CwdTemplate != "" && rootPath != "" {
		// Some agents need explicit path argument
	}
	return args
}

// ResolveCwd returns the working directory for the agent.
func (d Definition) ResolveCwd(rootPath string) string {
	if d.CwdTemplate == "" {
		return rootPath
	}
	return strings.ReplaceAll(d.CwdTemplate, "{root}", rootPath)
}
