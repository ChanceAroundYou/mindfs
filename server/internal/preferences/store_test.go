package preferences

import (
	"path/filepath"
	"testing"

	"mindfs/server/internal/agent"
	agenttypes "mindfs/server/internal/agent/types"
)

func TestSessionNamingDefaultsPersistAndReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), preferencesFileName)
	store := &Store{
		path: path,
		data: UserPreferences{Agents: map[string]AgentDefaults{}},
	}
	if err := store.UpdateSessionNamingDefaults(" codex ", " gpt-5.4 "); err != nil {
		t.Fatalf("UpdateSessionNamingDefaults: %v", err)
	}
	if got := store.SessionNamingDefaults(); got != (SessionNamingDefaults{Agent: "codex", Model: "gpt-5.4"}) {
		t.Fatalf("SessionNamingDefaults = %#v", got)
	}

	reloaded := &Store{
		path: path,
		data: UserPreferences{Agents: map[string]AgentDefaults{}},
	}
	if err := reloaded.load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := reloaded.SessionNamingDefaults(); got != (SessionNamingDefaults{Agent: "codex", Model: "gpt-5.4"}) {
		t.Fatalf("reloaded SessionNamingDefaults = %#v", got)
	}
}

func TestSessionNamingDefaultsAllowDefaultModel(t *testing.T) {
	store := &Store{
		path: filepath.Join(t.TempDir(), preferencesFileName),
		data: UserPreferences{Agents: map[string]AgentDefaults{}},
	}
	if err := store.UpdateSessionNamingDefaults("codex", ""); err != nil {
		t.Fatalf("UpdateSessionNamingDefaults with default model: %v", err)
	}
	if got := store.SessionNamingDefaults(); got != (SessionNamingDefaults{Agent: "codex"}) {
		t.Fatalf("SessionNamingDefaults = %#v", got)
	}
	if err := store.UpdateSessionNamingDefaults("", "gpt-5.4"); err == nil {
		t.Fatal("UpdateSessionNamingDefaults without agent succeeded")
	}
}

func TestApplyAgentDefaultsStaleModelFallback(t *testing.T) {
	models := []agenttypes.ModelInfo{
		{ID: "default"},
		{ID: "d4p"},
		{ID: "d4"},
	}
	statuses := []agent.Status{{Name: "claude", DefaultModelID: "of", Models: models}}

	// 过期默认模型（of，provider 切换前遗留）回退到目录第一个非 default。
	store := &Store{
		path: filepath.Join(t.TempDir(), preferencesFileName),
		data: UserPreferences{Agents: map[string]AgentDefaults{
			"claude": {Model: "of"},
		}},
	}
	out := store.ApplyAgentDefaults(statuses)
	if out[0].DefaultModelID != "d4p" {
		t.Fatalf("stale default = %q, want d4p", out[0].DefaultModelID)
	}

	// 目录内模型保持，不被回退。
	validStore := &Store{
		path: filepath.Join(t.TempDir(), preferencesFileName),
		data: UserPreferences{Agents: map[string]AgentDefaults{
			"claude": {Model: "d4p"},
		}},
	}
	out = validStore.ApplyAgentDefaults(statuses)
	if out[0].DefaultModelID != "d4p" {
		t.Fatalf("valid default = %q, want d4p", out[0].DefaultModelID)
	}
}
