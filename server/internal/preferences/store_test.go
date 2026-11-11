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
	if err := store.UpdateSessionNamingDefaults(" codex ", " gpt-5.4 ", true); err != nil {
		t.Fatalf("UpdateSessionNamingDefaults: %v", err)
	}
	if got := store.SessionNamingDefaults(); got != (SessionNamingDefaults{Agent: "codex", Model: "gpt-5.4", Disabled: true}) {
		t.Fatalf("SessionNamingDefaults = %#v", got)
	}

	reloaded := &Store{
		path: path,
		data: UserPreferences{Agents: map[string]AgentDefaults{}},
	}
	if err := reloaded.load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := reloaded.SessionNamingDefaults(); got != (SessionNamingDefaults{Agent: "codex", Model: "gpt-5.4", Disabled: true}) {
		t.Fatalf("reloaded SessionNamingDefaults = %#v", got)
	}
}

func TestSessionNamingDefaultsAllowDefaultModel(t *testing.T) {
	store := &Store{
		path: filepath.Join(t.TempDir(), preferencesFileName),
		data: UserPreferences{Agents: map[string]AgentDefaults{}},
	}
	if err := store.UpdateSessionNamingDefaults("codex", "", false); err != nil {
		t.Fatalf("UpdateSessionNamingDefaults with default model: %v", err)
	}
	if got := store.SessionNamingDefaults(); got != (SessionNamingDefaults{Agent: "codex"}) {
		t.Fatalf("SessionNamingDefaults = %#v", got)
	}
	if err := store.UpdateSessionNamingDefaults("", "gpt-5.4", false); err == nil {
		t.Fatal("UpdateSessionNamingDefaults without agent succeeded")
	}
	if err := store.UpdateSessionNamingDefaults("", "", true); err != nil {
		t.Fatalf("UpdateSessionNamingDefaults disabled without agent: %v", err)
	}
	if got := store.SessionNamingDefaults(); got != (SessionNamingDefaults{Disabled: true}) {
		t.Fatalf("disabled SessionNamingDefaults = %#v", got)
	}
}

func TestIdleSessionResourceReleaseHoursDefaultAndPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), preferencesFileName)
	store := &Store{path: path, data: UserPreferences{Agents: map[string]AgentDefaults{}}}
	if got := store.IdleSessionResourceReleaseHours(); got != 72 {
		t.Fatalf("default hours = %d, want 72", got)
	}
	if err := store.UpdateIdleSessionResourceReleaseHours(24); err != nil {
		t.Fatalf("UpdateIdleSessionResourceReleaseHours: %v", err)
	}
	reloaded := &Store{path: path, data: UserPreferences{Agents: map[string]AgentDefaults{}}}
	if err := reloaded.load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := reloaded.IdleSessionResourceReleaseHours(); got != 24 {
		t.Fatalf("reloaded hours = %d, want 24", got)
	}
	if err := store.UpdateIdleSessionResourceReleaseHours(0); err == nil {
		t.Fatal("zero hours unexpectedly accepted")
	}
}

func TestNewProjectMetaLocationDefaultAndPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), preferencesFileName)
	store := &Store{path: path, data: UserPreferences{Agents: map[string]AgentDefaults{}}}
	if got := store.NewProjectMetaLocation(); got != "project" {
		t.Fatalf("default location = %q", got)
	}
	if err := store.UpdateNewProjectMetaLocation("home"); err != nil {
		t.Fatal(err)
	}
	reloaded := &Store{path: path, data: UserPreferences{Agents: map[string]AgentDefaults{}}}
	if err := reloaded.load(); err != nil {
		t.Fatal(err)
	}
	if got := reloaded.NewProjectMetaLocation(); got != "home" {
		t.Fatalf("reloaded location = %q", got)
	}
	if err := store.UpdateNewProjectMetaLocation("other"); err == nil {
		t.Fatal("invalid location unexpectedly accepted")
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

func TestAgentCustomModelsPersistAndMergeIntoCatalog(t *testing.T) {
	path := filepath.Join(t.TempDir(), preferencesFileName)
	store := &Store{
		path: path,
		data: UserPreferences{Agents: map[string]AgentDefaults{}},
	}
	if err := store.UpdateAgentCustomModels(" codex ", []string{" of ", "", "of", "gpt-6-luna"}); err != nil {
		t.Fatalf("UpdateAgentCustomModels: %v", err)
	}
	if got := store.AgentCustomModels("codex"); len(got) != 2 || got[0] != "of" || got[1] != "gpt-6-luna" {
		t.Fatalf("custom models = %#v", got)
	}

	reloaded := &Store{path: path, data: UserPreferences{Agents: map[string]AgentDefaults{}}}
	if err := reloaded.load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := reloaded.AgentCustomModels("codex"); len(got) != 2 || got[0] != "of" {
		t.Fatalf("reloaded custom models = %#v", got)
	}

	statuses := []agent.Status{{
		Name:           "codex",
		DefaultModelID: "of",
		Models:         []agenttypes.ModelInfo{{ID: "gpt-6-luna", Name: "Luna"}},
	}}
	out := reloaded.ApplyAgentDefaults(statuses)
	// gpt-6-luna 既在厂商目录也在自定义列表里，只保留一份；新增的是 of。
	if len(out[0].Models) != 2 || out[0].Models[1].ID != "of" {
		t.Fatalf("models = %#v, want built-in + custom of", out[0].Models)
	}
	// 自定义模型必须能在默认模型校验里活下来，否则刚添加就被回退。
	if out[0].DefaultModelID != "of" {
		t.Fatalf("default model = %q, want of", out[0].DefaultModelID)
	}

	if err := reloaded.UpdateAgentCustomModels("codex", nil); err != nil {
		t.Fatalf("clear custom models: %v", err)
	}
	if got := reloaded.AgentCustomModels("codex"); len(got) != 0 {
		t.Fatalf("custom models after clear = %#v", got)
	}
}
