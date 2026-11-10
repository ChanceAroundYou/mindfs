package app

import (
	"mindfs/internal/codexsync"
	"mindfs/server/internal/preferences"
)

func initializeCodexProfileSelection(prefs *preferences.Store) error {
	paths, err := codexsync.ResolvePaths()
	if err != nil {
		return err
	}
	selections := prefs.AgentLastConfigSelections()
	selection := selections["codex"]
	name := ""
	switch {
	case selection.Type == "api_provider":
		if selection.ID == "api-llmux" {
			name = "llmux"
		} else {
			name = "default"
		}
	case selection.Type == "backup" && selection.ID == "shared:codex-llmux":
		name = "llmux"
	case selection.Type == "backup" && selection.ID == "shared:codex-default":
		name = "default"
	}

	var active string
	if name == "" {
		active, err = codexsync.InitializeActiveProfileFromRuntimeAt(paths)
	} else {
		active, err = codexsync.InitializeActiveProfileAt(paths, name)
	}
	if err != nil {
		return err
	}
	profileID := "shared:codex-default"
	profileName := "default"
	if active == "llmux" {
		profileID = "shared:codex-llmux"
		profileName = "llmux"
	}
	return prefs.UpdateAgentLastConfigSelection("codex", preferences.LastConfigSelection{
		Type: "backup",
		ID:   profileID,
		Name: profileName,
	})
}
