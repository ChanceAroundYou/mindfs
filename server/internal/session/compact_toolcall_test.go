package session

import (
	"testing"

	agenttypes "mindfs/server/internal/agent/types"
)

func strPtr(s string) *string {
	return &s
}

// edit 的 content 是格式化后的 diff，meta.input/output 是同一份 old/new 的
// 未格式化副本。content 非空时这两个字段是纯冗余，必须丢掉；content 为空时
// 必须保留（前端拿它当 fallback 渲染 diff）。
func TestCompactToolCallEditDropsRedundantMeta(t *testing.T) {
	diffContent := []agenttypes.ToolCallContentItem{
		{Type: "diff", Path: "a.go", OldText: strPtr("old"), NewText: "new"},
	}

	cases := []struct {
		name       string
		kind       agenttypes.ToolKind
		content    []agenttypes.ToolCallContentItem
		meta       map[string]any
		wantInput  bool
		wantOutput bool
		wantOther  bool
	}{
		{
			name:       "edit with content drops input/output",
			kind:       agenttypes.ToolKindEdit,
			content:    diffContent,
			meta:       map[string]any{"input": "raw", "output": "out", "filePath": "a.go"},
			wantInput:  false,
			wantOutput: false,
			wantOther:  true,
		},
		{
			name:       "edit without content keeps input/output",
			kind:       agenttypes.ToolKindEdit,
			content:    nil,
			meta:       map[string]any{"input": "raw", "output": "out"},
			wantInput:  true,
			wantOutput: true,
		},
		{
			name:       "ask_user keeps input (questions fallback)",
			kind:       agenttypes.ToolKindAskUser,
			content:    diffContent,
			meta:       map[string]any{"input": `{"questions":[]}`},
			wantInput:  true,
			wantOutput: false,
		},
		{
			name:       "execute keeps command, drops output",
			kind:       agenttypes.ToolKindExecute,
			content:    nil,
			meta:       map[string]any{"command": "ls", "output": "out"},
			wantInput:  false,
			wantOutput: false,
			wantOther:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			call := agenttypes.ToolCall{
				CallID:  "c1",
				Kind:    tc.kind,
				Content: tc.content,
				Meta:    tc.meta,
			}
			got := CompactToolCall(call)
			_, hasInput := got.Meta["input"]
			_, hasOutput := got.Meta["output"]
			if hasInput != tc.wantInput {
				t.Errorf("meta.input present=%v want %v", hasInput, tc.wantInput)
			}
			if hasOutput != tc.wantOutput {
				t.Errorf("meta.output present=%v want %v", hasOutput, tc.wantOutput)
			}
			if tc.wantOther && got.Meta["filePath"] != "a.go" && got.Meta["command"] != "ls" {
				t.Errorf("other meta fields should be preserved, got %v", got.Meta)
			}
		})
	}
}

// 窗口轻压缩：edit/read/execute 的 content 清空（展开时懒加载），
// ask_user/todo 的 content 保留（折叠卡片就要用）。
func TestCompactExchangeAuxLightStripsContentForLazyKinds(t *testing.T) {
	diffContent := []agenttypes.ToolCallContentItem{
		{Type: "diff", Path: "a.go", OldText: strPtr("old"), NewText: "new"},
	}

	cases := []struct {
		name      string
		kind      agenttypes.ToolKind
		wantEmpty bool
	}{
		{"edit content stripped", agenttypes.ToolKindEdit, true},
		{"read content stripped", agenttypes.ToolKindRead, true},
		{"execute content stripped", agenttypes.ToolKindExecute, true},
		{"ask_user content kept", agenttypes.ToolKindAskUser, false},
		{"todo content kept", agenttypes.ToolKindTodo, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			aux := ExchangeAux{
				Seq: 1,
				ToolCall: &agenttypes.ToolCall{
					CallID:  "c1",
					Kind:    tc.kind,
					Content: diffContent,
					Meta:    map[string]any{"input": "raw"},
				},
			}
			got, ok := CompactExchangeAuxLight(aux)
			if !ok {
				t.Fatal("CompactExchangeAuxLight returned ok=false")
			}
			if got.ToolCall == nil {
				t.Fatal("ToolCall should be preserved")
			}
			isEmpty := len(got.ToolCall.Content) == 0
			if isEmpty != tc.wantEmpty {
				t.Errorf("content empty=%v want %v", isEmpty, tc.wantEmpty)
			}
		})
	}
}
