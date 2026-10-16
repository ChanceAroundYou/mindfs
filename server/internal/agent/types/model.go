package types

type ModelInfo struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Description   string   `json:"description,omitempty"`
	Hidden        bool     `json:"hidden,omitempty"`
	SupportEffort bool     `json:"supportEffort,omitempty"`
	Efforts       []string `json:"efforts,omitempty"`
}

type ModelList struct {
	CurrentModelID string      `json:"current_model_id,omitempty"`
	Models         []ModelInfo `json:"models,omitempty"`
}

type ModeInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type ModeList struct {
	CurrentModeID string     `json:"current_mode_id,omitempty"`
	Modes         []ModeInfo `json:"modes,omitempty"`
}

type CommandInfo struct {
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	ArgumentHint string `json:"argument_hint,omitempty"`
}

type CommandList struct {
	Commands []CommandInfo `json:"commands,omitempty"`
}
