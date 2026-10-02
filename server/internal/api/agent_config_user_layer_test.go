package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mindfs/server/internal/agent"
	"mindfs/server/internal/testutil"
)

// 三个写用户层的地方（env / configBackup / provider switch）都只能写「用户自己配的东西」。
//
// 它们以前读的是 LoadConfig("")（安装自带 ⊕ 用户层），改一两个字段后把**整份合并结果**
// 写回用户层 —— 上游定义（command/args/installCommands…）被整份冻进用户层，之后改
// agents.json 永远被那层旧快照盖住。dsh 的 installCommands 就这样一直停在坏的 0.4.9 上，
// 而 mindfs 界面里的「安装/更新 agent」读的正是它。
//
// 判据：写入后用户层里除了那个 agent 不该多出上游条目（测试环境的上游是内置的
// claude/gemini/codex），也不该出现 install/update 命令。
func TestAgentConfigWritersKeepUserLayerMinimal(t *testing.T) {
	cases := []struct {
		name  string
		write func() error
	}{
		{"env", func() error {
			return updateAgentEnvConfig("dsh", map[string]string{"DEEPSEEK_API_KEY": "sk-1"})
		}},
		{"configBackup-defaults", func() error {
			return updateAgentConfigDefaults("dsh", []string{"~/.dsh/.credentials.yaml"}, []string{"DEEPSEEK_API_KEY"})
		}},
		{"provider-switch", func() error {
			_, err := replaceAgentConfiguredEnv("dsh", map[string]string{"DEEPSEEK_API_KEY": "sk-2"})
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			testutil.IsolateUserDirs(t, home)
			configPath := filepath.Join(home, "agents.json")
			t.Setenv("MINDFS_AGENTS_CONFIG", configPath)
			writeJSON(t, configPath, agent.Config{Agents: []agent.Definition{{
				Name:     "dsh",
				Command:  "dsh",
				Protocol: agent.ProtocolACP,
				Args:     []string{"--profile", "mindfs-acp"},
				Env:      map[string]string{"KEEP": "1"},
			}}})

			if err := tc.write(); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}

			var got agent.Config
			payload, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatalf("read %s: %v", configPath, err)
			}
			if err := json.Unmarshal(payload, &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}

			if len(got.Agents) != 1 || got.Agents[0].Name != "dsh" {
				t.Fatalf("用户层被写进了上游定义（%d 条：%s）", len(got.Agents), joinAgentNames(got))
			}
			def := got.Agents[0]
			if len(def.InstallCommands) > 0 || len(def.UpdateCommands) > 0 {
				t.Fatalf("上游的 install/update 命令被冻进用户层：%#v", def)
			}
			if def.Brief != "" {
				t.Fatalf("上游字段被冻进用户层：%#v", def)
			}
		})
	}
}

// 用户层里还没有这个 agent 时补一个最小条目（只有 name 和用户自己配的字段），
// 其余定义交给加载时的合并 —— 条目里不写 command/args，靠 mergeAgentDefinition 回退上游。
func TestWriteAgentEnvConfigSeedsMinimalUserEntry(t *testing.T) {
	home := t.TempDir()
	testutil.IsolateUserDirs(t, home)
	configPath := filepath.Join(home, "agents.json")
	t.Setenv("MINDFS_AGENTS_CONFIG", configPath)

	baseURL := "https://openwrt.xiaokubao.space/llmux/v1"
	// 测试环境的上游是内置的 claude/gemini/codex，所以这里用 claude 当「上游已有的 agent」。
	env, err := mergeAgentEnvConfig("claude", map[string]string{"ANTHROPIC_BASE_URL": baseURL})
	if err != nil {
		t.Fatalf("mergeAgentEnvConfig: %v", err)
	}
	// 回给调用方的是**生效** env（SetAgentEnv 是整份替换），不能丢刚写的键。
	if env["ANTHROPIC_BASE_URL"] != baseURL {
		t.Fatalf("返回的生效 env 丢了刚写的键：%#v", env)
	}

	var got agent.Config
	payload, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read %s: %v", configPath, err)
	}
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Agents) != 1 || got.Agents[0].Name != "claude" {
		t.Fatalf("用户层被写进了上游定义（%d 条：%s）", len(got.Agents), joinAgentNames(got))
	}
	if got.Agents[0].Env["ANTHROPIC_BASE_URL"] != baseURL {
		t.Fatalf("env 没写进用户层：%#v", got.Agents[0].Env)
	}
}

// 上游根本没有这个名字时不能往用户层里造幽灵 agent（command 为空，列表里会多一条坏条目）。
func TestMutateUserAgentRejectsUnknownAgent(t *testing.T) {
	home := t.TempDir()
	testutil.IsolateUserDirs(t, home)
	configPath := filepath.Join(home, "agents.json")
	t.Setenv("MINDFS_AGENTS_CONFIG", configPath)

	if err := updateAgentEnvConfig("no-such-agent", map[string]string{"A": "1"}); err == nil {
		t.Fatal("上游没有这个 agent，写入却成功了")
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("失败的写入不该落盘：%v", err)
	}
}

func joinAgentNames(cfg agent.Config) string {
	names := make([]string, 0, len(cfg.Agents))
	for _, def := range cfg.Agents {
		names = append(names, def.Name)
	}
	return strings.Join(names, ",")
}
