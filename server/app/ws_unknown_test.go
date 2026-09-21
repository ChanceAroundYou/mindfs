package app

import "testing"

func TestEmptyWorkspaceListRootsIsEmpty(t *testing.T) {
	m := &workspaceManager{shared: sharedServices{}}
	ctx, err := m.emptyWorkspace()
	if err != nil {
		t.Fatalf("emptyWorkspace: %v", err)
	}
	if got := ctx.ListRoots(); len(got) != 0 {
		t.Errorf("ListRoots() = %d 项，want 0", len(got))
	}
}

func TestValidAccountID(t *testing.T) {
	cases := map[string]bool{
		"u_QenYufpQ7zUypmGP": true,
		"xingxingbao":        true,
		"xiaokubao":          true,
		"a":                  true,
		"":                   false,
		".":                  false,
		"..":                 false,
		"../etc":             false,
		"a/b":                false,
		"a b":                false,
		"u_带中文":              false,
	}
	for in, want := range cases {
		if got := validAccountID(in); got != want {
			t.Errorf("validAccountID(%q) = %v, want %v", in, got, want)
		}
	}
}
