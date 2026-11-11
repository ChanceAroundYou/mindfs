package usecase

import (
	"strings"
	"testing"

	agenttypes "mindfs/server/internal/agent/types"
)

func TestValidateModelAgainstModelsAcceptsCustomCatalogEntry(t *testing.T) {
	models := []agenttypes.ModelInfo{{ID: "gpt-6-luna"}, {ID: "gpt-6-sol"}}

	if err := validateModelAgainstModels("codex", "gpt-6-luna", models); err != nil {
		t.Fatalf("built-in model rejected: %v", err)
	}
	if err := validateModelAgainstModels("codex", "of", models); err == nil {
		t.Fatal("custom model accepted without being in the catalog")
	}

	merged := withCustomModels(models, []string{" of ", "gpt-6-luna", "", "of"})
	if len(merged) != 3 {
		t.Fatalf("merged models = %v, want built-ins plus one custom entry", merged)
	}
	if err := validateModelAgainstModels("codex", "of", merged); err != nil {
		t.Fatalf("custom model rejected after merge: %v", err)
	}
}

func TestValidateModelAgainstModelsErrorListsCustomEntries(t *testing.T) {
	merged := withCustomModels([]agenttypes.ModelInfo{{ID: "gpt-6-luna"}}, []string{"of"})
	err := validateModelAgainstModels("codex", "sonnet4.6", merged)
	if err == nil {
		t.Fatal("unknown model was accepted")
	}
	if !strings.Contains(err.Error(), `"sonnet4.6"`) || !strings.Contains(err.Error(), "gpt-6-luna") || !strings.Contains(err.Error(), "of") {
		t.Fatalf("error should list the merged catalog, got %v", err)
	}
}

func TestWithCustomModelsKeepsClaudeAliasCanonicalization(t *testing.T) {
	models := []agenttypes.ModelInfo{{ID: "gpt-6-luna"}}
	merged := withCustomModels(models, []string{"opus"})
	if err := validateModelAgainstModels("claude", "op[1m]", merged); err != nil {
		t.Fatalf("claude alias should canonicalize against custom entries: %v", err)
	}
}
