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

// 重名判定只拒绝本次写入**新引入**的冲突，历史并存数据放行 ——
// 否则用户为了改一条 URL 会被迫先改名。
func TestNameConflicts(t *testing.T) {
	existing := []NodeConnection{
		{ID: "a", Name: "书房 PC", URL: "https://a.example"},
		{ID: "b", Name: "书房 pc", URL: "https://b.example"}, // 历史重名（忽略大小写）
		{ID: "c", Name: "客厅", URL: "https://c.example"},
	}
	cases := []struct {
		name     string
		incoming []NodeConnection
		want     bool
	}{
		{
			name:     "历史重名原样写回不拦",
			incoming: existing,
			want:     false,
		},
		{
			name: "改 URL 不拦",
			incoming: []NodeConnection{
				{ID: "a", Name: "书房 PC", URL: "https://a2.example"},
				{ID: "b", Name: "书房 pc", URL: "https://b.example"},
				{ID: "c", Name: "客厅", URL: "https://c.example"},
			},
			want: false,
		},
		{
			name: "新节点撞已有名字",
			incoming: []NodeConnection{
				existing[0], existing[1], existing[2],
				{ID: "d", Name: "  客厅  ", URL: "https://d.example"},
			},
			want: true,
		},
		{
			name: "改名撞已有名字",
			incoming: []NodeConnection{
				existing[0], existing[1],
				{ID: "c", Name: "书房 PC", URL: "https://c.example"},
			},
			want: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, got := NameConflicts(existing, tc.incoming); got != tc.want {
				t.Fatalf("NameConflicts = %v, want %v", got, tc.want)
			}
		})
	}
}

// local 不再是特殊节点：它要么在表里（旧数据），就可以正常删除。
func TestStoreRemoveAllowsLegacyLocalNode(t *testing.T) {
	store, err := NewStoreAt(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Upsert(NodeConnection{ID: "local", Name: "local", URL: "http://127.0.0.1:7332"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Remove("local"); err != nil {
		t.Fatalf("Remove(local) = %v, want nil", err)
	}
	if _, ok := store.Get("local"); ok {
		t.Fatal("local 仍在表里")
	}
}
