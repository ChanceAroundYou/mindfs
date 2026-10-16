package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	configpkg "mindfs/server/internal/config"
)

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
