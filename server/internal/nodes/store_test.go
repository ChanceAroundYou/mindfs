package nodes

import "testing"

func TestStoreNormalizeNodeURL(t *testing.T) {
	tests := []struct {
		name string
		in   NodeConnection
		want string
	}{
		{name: "strips trailing slash", in: NodeConnection{ID: "a", Name: "A", URL: "https://h.example/"}, want: "https://h.example"},
		{name: "trims whitespace", in: NodeConnection{ID: "a", Name: "A", URL: "  https://h.example  "}, want: "https://h.example"},
		{name: "keeps no-slash url", in: NodeConnection{ID: "a", Name: "A", URL: "https://h.example"}, want: "https://h.example"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalize([]NodeConnection{tt.in})
			if len(got) != 1 {
				t.Fatalf("normalize() returned %d nodes, want 1", len(got))
			}
			if got[0].URL != tt.want {
				t.Fatalf("normalized URL = %q, want %q", got[0].URL, tt.want)
			}
		})
	}
}

func TestStoreNormalizeDedupsAndDropsInvalid(t *testing.T) {
	in := []NodeConnection{
		{ID: "a", Name: "A", URL: "https://h.example/"},
		{ID: "a", Name: "A dup", URL: "https://h.example/"},
		{ID: "", Name: "B", URL: "https://h.example/"},
		{ID: "c", Name: "", URL: "https://h.example/"},
		{ID: "d", Name: "D", URL: ""},
	}
	got := normalize(in)
	if len(got) != 1 {
		t.Fatalf("normalize() returned %d nodes, want 1 (unique valid id only)", len(got))
	}
	if got[0].ID != "a" {
		t.Fatalf("kept ID = %q, want %q", got[0].ID, "a")
	}
	if got[0].URL != "https://h.example" {
		t.Fatalf("URL = %q, want %q", got[0].URL, "https://h.example")
	}
}
