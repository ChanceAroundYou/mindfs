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

func TestRouteBasePrefersDirectThenRelay(t *testing.T) {
	if got := (NodeConnection{URL: "https://direct", RelayURL: "https://relay"}).RouteBase(); got != "https://direct" {
		t.Fatalf("直连优先：%q", got)
	}
	if got := (NodeConnection{RelayURL: "https://relay"}).RouteBase(); got != "https://relay" {
		t.Fatalf("无直连时用 relay：%q", got)
	}
	if got := (NodeConnection{}).RouteBase(); got != "" {
		t.Fatalf("两者皆空应返回空：%q", got)
	}
}

// relay 是同一节点上的第二通道：normalize 必须保留「只有 relayUrl」的行，
// 丢掉遗留的 relay:* 独立行，并把同 InstanceID 的直连行与 relay 行并成一行。
func TestNormalizeKeepsRelayOnlyDropsLegacyAndMergesByInstance(t *testing.T) {
	in := []NodeConnection{
		{ID: "node-pc", Name: "pc-wsl", URL: "https://pc.example/mindfs", Color: "#abc", InstanceID: "pc", Remote: true},
		{ID: "pc", Name: "edge-pc", RelayURL: "https://home.example/mindfs/relay/edge-pc", InstanceID: "pc", Remote: true},
		{ID: "relay:hasee", Name: "hasee", URL: "https://home.example/mindfs/relay/edge", Remote: true},
		{ID: "hasee", Name: "edge", RelayURL: "https://home.example/mindfs/relay/edge", InstanceID: "hasee", Remote: true},
	}
	got := normalize(in)
	if len(got) != 2 {
		t.Fatalf("normalize 后 %d 行，want 2（pc 合并、hasee 去 relay:* 前缀行）：%+v", len(got), got)
	}
	pc := got[0]
	if pc.ID != "node-pc" || pc.URL != "https://pc.example/mindfs" {
		t.Fatalf("应保留直连行：%+v", pc)
	}
	if pc.RelayURL != "https://home.example/mindfs/relay/edge-pc" {
		t.Fatalf("relay 未并进直连行：%+v", pc)
	}
	if pc.Color != "#abc" {
		t.Fatalf("颜色丢失：%+v", pc)
	}
	if got[1].ID != "hasee" || got[1].RelayURL == "" {
		t.Fatalf("relay-only 行应保留且不带 relay: 前缀：%+v", got[1])
	}
}

// 前端整表 PUT 可能比 relay 发布晚一拍（页面先加载、隧道后接通）：
// 同 ID 的行必须把本机已知的 relay 通道与设备身份带回来，否则一次改名/改色
// 就会把兜底地址抹掉，只能等隧道重连才恢复。
func TestReplaceKeepsRelayFieldsFromStore(t *testing.T) {
	store, err := NewStoreAt(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Upsert(NodeConnection{ID: "node-pc", Name: "pc-wsl", URL: "https://pc.example/mindfs", InstanceID: "pc"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetRelay("pc", "edge-pc", "https://home.example/mindfs/relay/edge-pc"); err != nil {
		t.Fatal(err)
	}
	// 旧缓存里没有 relayUrl / instanceId（隧道是页面加载之后才通的）。
	if err := store.Replace([]NodeConnection{
		{ID: "node-pc", Name: "pc-wsl", URL: "https://pc.example/mindfs", Color: "#abc"},
	}); err != nil {
		t.Fatal(err)
	}
	list := store.List()
	if len(list) != 1 {
		t.Fatalf("Replace 后 %d 行：%+v", len(list), list)
	}
	if list[0].RelayURL != "https://home.example/mindfs/relay/edge-pc" {
		t.Fatalf("relay 兜底被整表替换抹掉：%+v", list[0])
	}
	if list[0].InstanceID != "pc" {
		t.Fatalf("设备身份被抹掉：%+v", list[0])
	}
	if list[0].Color != "#abc" {
		t.Fatalf("本次写入的字段反而丢了：%+v", list[0])
	}
}

func TestSetRelayIsIdempotentAndKeepsDirect(t *testing.T) {
	store, err := NewStoreAt(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Upsert(NodeConnection{ID: "node-pc", Name: "pc", URL: "https://pc.example/mindfs", InstanceID: "pc"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetRelay("pc", "edge-pc", "https://home.example/mindfs/relay/edge-pc"); err != nil {
		t.Fatal(err)
	}
	// 再调一次（重连）不应新增行。
	if _, err := store.SetRelay("pc", "edge-pc", "https://home.example/mindfs/relay/edge-pc"); err != nil {
		t.Fatal(err)
	}
	list := store.List()
	if len(list) != 1 {
		t.Fatalf("SetRelay 幂等失败，有 %d 行：%+v", len(list), list)
	}
	if list[0].ID != "node-pc" || list[0].URL != "https://pc.example/mindfs" {
		t.Fatalf("直连行被改：%+v", list[0])
	}
	if list[0].RelayURL != "https://home.example/mindfs/relay/edge-pc" {
		t.Fatalf("RelayURL = %q", list[0].RelayURL)
	}
}
