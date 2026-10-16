package types

type ToolKind string

type ToolCallLocation struct {
	Path string `json:"path"`
	Line *int   `json:"line,omitempty"`
}

type ToolCallContentItem struct {
	Type       string  `json:"type"`
	Text       string  `json:"text,omitempty"`
	Path       string  `json:"path,omitempty"`
	ChangeKind string  `json:"changeKind,omitempty"`
	OldText    *string `json:"oldText,omitempty"`
	NewText    string  `json:"newText,omitempty"`
}

type ToolCall struct {
	CallID    string                `json:"callId"`
	Title     string                `json:"title,omitempty"`
	Status    string                `json:"status"`
	Kind      ToolKind              `json:"kind"`
	Content   []ToolCallContentItem `json:"content,omitempty"`
	Locations []ToolCallLocation    `json:"locations,omitempty"`
	RawType   string                `json:"rawType,omitempty"`
	Meta      map[string]any        `json:"meta,omitempty"`
}

func (tc ToolCall) IsWriteOperation() bool {
	switch tc.Kind {
	case ToolKindEdit, ToolKindDelete, ToolKindMove:
		return true
	default:
		return false
	}
}

func (tc ToolCall) GetAffectedPaths() []string {
	paths := make([]string, 0, len(tc.Locations))
	for _, loc := range tc.Locations {
		if loc.Path != "" {
			paths = append(paths, loc.Path)
		}
	}
	return paths
}
