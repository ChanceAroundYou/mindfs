package agent

import (
	"reflect"
	"testing"
)

// mergeAgentDefinition 对空 override 字段一律回退 base —— Env 也必须如此。
//
// 漏掉 Env 的后果不是「少一个字段」而是「用户配的凭据被静默抹掉」：
// 生效配置是三层合并出来的（安装自带的一份 + ~/.config 的用户层 + -agent-config
// 指向的 extra，而 extra 通常就是安装自带的那份、不含 env）。用户层里按机器配的
// 环境变量（API key / base url / model）会被最后一层的空 Env 清成 nil。
// 运行期靠 SetAgentEnv 撑着看不出来，重启后才暴露成「agent 认不到 provider」。
// 2026-10-02 实测：dsh 每次重启 mindfs 就从 available=true 退回认证失败。
func TestMergeAgentDefinitionKeepsBaseEnvWhenOverrideHasNone(t *testing.T) {
	base := Definition{
		Name:     "dsh",
		Command:  "dsh",
		Protocol: ProtocolACP,
		Env: map[string]string{
			"DEEPSEEK_API_KEY":  "sk-test",
			"DEEPSEEK_BASE_URL": "https://gateway.example.com/v1",
			"DSH_MODEL":         "os",
		},
	}
	// override 就是 `-agent-config` 那份：同名 agent，但没有 env。
	override := Definition{Name: "dsh", Command: "dsh", Protocol: ProtocolACP}

	merged := mergeConfigs(
		Config{Agents: []Definition{base}},
		Config{Agents: []Definition{override}},
	)
	if len(merged.Agents) != 1 {
		t.Fatalf("agents = %d, want 1", len(merged.Agents))
	}
	if !reflect.DeepEqual(merged.Agents[0].Env, base.Env) {
		t.Fatalf("Env 被抹掉了：got %#v, want %#v", merged.Agents[0].Env, base.Env)
	}

	// 合并结果会被各账户长期共享：改副本不能回头改到解码时的那份。
	merged.Agents[0].Env["DSH_MODEL"] = "mutated"
	if base.Env["DSH_MODEL"] != "os" {
		t.Fatalf("合并结果与 base 共享了底层 map，base 被改成 %q", base.Env["DSH_MODEL"])
	}
}

// 「只配了 env」的最小条目（写用户层时为记 env 补的 `{"name":"dsh"}`）不能把上游定义清空。
//
// 用户层的写入点只改 env/configBackup，其余字段全交给合并回退 —— 所以回退必须覆盖
// command/args/协议。漏了 command 的后果是 agent 直接起不来（命令为空串）。
func TestMergeAgentDefinitionKeepsBaseFieldsForMinimalEntry(t *testing.T) {
	base := Definition{
		Name:            "dsh",
		Brief:           "DeepSeek Harness",
		Command:         "dsh",
		Protocol:        ProtocolACP,
		Args:            []string{"--profile", "mindfs-acp"},
		InstallCommands: LifecycleCommands{"npm i -g @deepseek-ai/dsh"},
	}
	merged := mergeConfigs(
		Config{Agents: []Definition{base}},
		Config{Agents: []Definition{{Name: "dsh"}}},
	)

	got := merged.Agents[0]
	if got.Command != base.Command {
		t.Fatalf("command 被最小条目清空了：%q", got.Command)
	}
	if got.Protocol != base.Protocol {
		t.Fatalf("protocol = %q, want %q", got.Protocol, base.Protocol)
	}
	if !reflect.DeepEqual(got.Args, base.Args) {
		t.Fatalf("args = %#v, want %#v", got.Args, base.Args)
	}
	if got.Brief != base.Brief || len(got.InstallCommands) != 1 {
		t.Fatalf("其余字段没回退 base：%#v", got)
	}
}

// override 自己带了 env 时，仍然以 override 为准（与 Brief/InstallCommands 等同口径），
// 不能反过来被 base 覆盖。
func TestMergeAgentDefinitionPrefersOverrideEnv(t *testing.T) {
	base := Definition{
		Name: "dsh", Command: "dsh", Protocol: ProtocolACP,
		Env: map[string]string{"DEEPSEEK_API_KEY": "sk-base", "DSH_MODEL": "os"},
	}
	override := Definition{
		Name: "dsh", Command: "dsh", Protocol: ProtocolACP,
		Env: map[string]string{"DEEPSEEK_API_KEY": "sk-override"},
	}

	merged := mergeConfigs(
		Config{Agents: []Definition{base}},
		Config{Agents: []Definition{override}},
	)
	want := map[string]string{"DEEPSEEK_API_KEY": "sk-override"}
	if !reflect.DeepEqual(merged.Agents[0].Env, want) {
		t.Fatalf("Env = %#v, want %#v", merged.Agents[0].Env, want)
	}
}
