package types

import "testing"

func TestToolPolicyDeniesByDefault(t *testing.T) {
	policy := &ToolPolicy{
		AllowedTools:    []string{RouterToolPrefix + "*"},
		DisallowedTools: []string{RouterToolPrefix + "read_file"},
		DenyByDefault:   true,
	}
	cases := map[string]bool{
		RouterToolPrefix + "say":       true,
		RouterToolPrefix + "read_file": false,
		"Bash":                         false,
		"Read":                         false,
		"mcp__other__say":              false,
		"mcp__mindfs_router":           false,
	}
	for name, want := range cases {
		if got := policy.Allows(name); got != want {
			t.Errorf("Allows(%q) = %v, want %v", name, got, want)
		}
	}

	if !(*ToolPolicy)(nil).Allows("Bash") {
		t.Fatal("nil policy must preserve legacy behavior")
	}
	if !(&ToolPolicy{}).Allows("Bash") {
		t.Fatal("zero policy without DenyByDefault must allow")
	}
	if (&ToolPolicy{DenyByDefault: true}).Allows("Bash") {
		t.Fatal("DenyByDefault with no allow list must deny")
	}
}
