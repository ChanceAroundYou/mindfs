package pins

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	store, err := NewStoreAt(dir)
	if err != nil {
		t.Fatalf("NewStoreAt: %v", err)
	}
	return store
}

// 与 nodes.Store / preferences.Store 同一形状：文件落 tmp 再 rename，中途读到的
// 永远是完整 JSON。写坏了会让这个账户的置顶一次性全没，这条守的是那个形状。
func TestStoreWritesAtomically(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStoreAt(dir)
	if err != nil {
		t.Fatalf("NewStoreAt: %v", err)
	}
	if _, _, err := store.SetSessionPin("root::k1", true); err != nil {
		t.Fatalf("SetSessionPin: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, pinsFileName)); err != nil {
		t.Fatalf("pin file must exist: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("临时文件残留: %s", e.Name())
		}
	}
}

func TestStoreSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStoreAt(dir)
	if err != nil {
		t.Fatalf("NewStoreAt: %v", err)
	}
	if _, _, err := store.SetSessionPin("pc::CMAI::s1", true); err != nil {
		t.Fatalf("SetSessionPin: %v", err)
	}
	if _, _, err := store.SetProjectPin("pc::CMAI", true); err != nil {
		t.Fatalf("SetProjectPin: %v", err)
	}

	reopened, err := NewStoreAt(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := reopened.SessionKeys(); len(got) != 1 || got[0] != "pc::CMAI::s1" {
		t.Fatalf("重启后会话置顶键丢了: %v", got)
	}
	ts, ok := reopened.SessionPinnedAt("pc::CMAI::s1")
	if !ok {
		t.Fatal("重启后读不到置顶时间")
	}
	if ts.IsZero() {
		t.Fatal("置顶时间不该为零值")
	}
	if got := reopened.ProjectPins(); len(got) != 1 || got["pc::CMAI"] == 0 {
		t.Fatalf("重启后项目置顶丢了: %v", got)
	}
}

// 两种置顶住在同一份文件里，但**互不干扰**：取消会话不该动项目，反之亦然。
// 这是把它们合并到一个存储的核心风险，必须钉住。
func TestProjectAndSessionPinsAreIndependent(t *testing.T) {
	store := newTestStore(t)
	if _, _, err := store.SetProjectPin("pc::CMAI", true); err != nil {
		t.Fatalf("SetProjectPin: %v", err)
	}
	if _, _, err := store.SetSessionPin("pc::CMAI::s1", true); err != nil {
		t.Fatalf("SetSessionPin: %v", err)
	}
	if _, _, err := store.SetSessionPin("pc::CMAI::s1", false); err != nil {
		t.Fatalf("取消会话置顶: %v", err)
	}
	if got := store.ProjectPins(); len(got) != 1 {
		t.Fatalf("取消会话置顶不该动项目置顶: %v", got)
	}
	if got := store.SessionKeys(); len(got) != 0 {
		t.Fatalf("会话置顶没取消掉: %v", got)
	}
}

// 已置顶时再点一次不刷新时间戳 —— 反复点「置顶」不该让这一条在列表里跳来跳去。
// 与 session.Manager.SetPinned 的既有规则一致。
func TestSetPinnedTwiceKeepsTimestamp(t *testing.T) {
	store := newTestStore(t)
	first, _, err := store.SetSessionPin("root::k", true)
	if err != nil {
		t.Fatalf("first Set: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	second, pinned, err := store.SetSessionPin("root::k", true)
	if err != nil {
		t.Fatalf("second Set: %v", err)
	}
	if !pinned {
		t.Fatal("重复置顶后状态应为已置顶")
	}
	if !second.Equal(first) {
		t.Fatalf("重复置顶不该刷新时间戳: %v → %v", first, second)
	}
	// 项目置顶同一规则
	pFirst, _, err := store.SetProjectPin("pc::CMAI", true)
	if err != nil {
		t.Fatalf("first SetProjectPin: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	pSecond, _, err := store.SetProjectPin("pc::CMAI", true)
	if err != nil {
		t.Fatalf("second SetProjectPin: %v", err)
	}
	if pSecond != pFirst {
		t.Fatalf("重复置顶项目不该刷新时间戳: %d → %d", pFirst, pSecond)
	}
}

// 取消 → 重置 应当拿到新时间：这才是有意义的「重新置顶」。
func TestUnpinThenRepinGetsFreshTimestamp(t *testing.T) {
	store := newTestStore(t)
	first, _, err := store.SetSessionPin("root::k", true)
	if err != nil {
		t.Fatalf("first Set: %v", err)
	}
	if _, pinned, err := store.SetSessionPin("root::k", false); err != nil || pinned {
		t.Fatalf("unpin: pinned=%v err=%v", pinned, err)
	}
	if got := store.SessionKeys(); len(got) != 0 {
		t.Fatalf("取消后键还在: %v", got)
	}
	time.Sleep(2 * time.Millisecond)
	third, _, err := store.SetSessionPin("root::k", true)
	if err != nil {
		t.Fatalf("repin: %v", err)
	}
	if !third.After(first) {
		t.Fatalf("重新置顶应拿到更新的时间: %v 不晚于 %v", third, first)
	}
}

// 取消一个本来就没置顶的键：不写库、不报错、不建文件。
func TestUnpinUnknownKeyIsANoop(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStoreAt(dir)
	if err != nil {
		t.Fatalf("NewStoreAt: %v", err)
	}
	if _, pinned, err := store.SetSessionPin("root::never", false); err != nil || pinned {
		t.Fatalf("取消未置顶的会话键：pinned=%v err=%v", pinned, err)
	}
	if _, pinned, err := store.SetProjectPin("never", false); err != nil || pinned {
		t.Fatalf("取消未置顶的项目键：pinned=%v err=%v", pinned, err)
	}
	if _, err := os.Stat(filepath.Join(dir, pinsFileName)); !os.IsNotExist(err) {
		t.Fatalf("没发生变更就不该创建文件, stat err=%v", err)
	}
}

// SessionKeys / ProjectPins 必须返回拷贝：调用方改了它不能影响存储。
func TestAccessorsReturnACopy(t *testing.T) {
	store := newTestStore(t)
	if _, _, err := store.SetSessionPin("root::k", true); err != nil {
		t.Fatalf("SetSessionPin: %v", err)
	}
	if _, _, err := store.SetProjectPin("pc::CMAI", true); err != nil {
		t.Fatalf("SetProjectPin: %v", err)
	}
	keys := store.SessionKeys()
	keys[0] = "篡改"
	if got := store.SessionKeys(); len(got) != 1 || got[0] != "root::k" {
		t.Fatalf("SessionKeys 泄露了内部 map: %v", got)
	}
	projects := store.ProjectPins()
	projects["pc::CMAI"] = 1
	delete(projects, "pc::CMAI")
	projects["注入"] = 123
	if got := store.ProjectPins(); len(got) != 1 || got["pc::CMAI"] == 1 {
		t.Fatalf("ProjectPins 泄露了内部 map: %v", got)
	}
}

// 并发写不能把文件写坏，也不能丢键。
func TestConcurrentSetsKeepEveryKey(t *testing.T) {
	store := newTestStore(t)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, _, err := store.SetSessionPin("root::k"+string(rune('a'+i%26))+string(rune('a'+i/26)), true); err != nil {
				t.Errorf("SetSessionPin: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if got := len(store.SessionKeys()); got != 50 {
		t.Fatalf("并发置顶丢了键: %d/50", got)
	}
}

// 坏 JSON 不能让整个服务起不来：读成空表，好过一次启动失败。
func TestCorruptFileLoadsAsEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, pinsFileName), []byte("{不是 JSON"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	store, err := NewStoreAt(dir)
	if err != nil {
		t.Fatalf("坏文件不该让 NewStoreAt 失败: %v", err)
	}
	if got := store.SessionKeys(); len(got) != 0 {
		t.Fatalf("坏文件应读成空表, got %v", got)
	}
}

// 落库失败必须把内存**回滚成原值**，否则置顶在内存里消失了、盘上还留着：
// 用户看到「取消了」，重启后又回来了，而报告里没有任何错误。
//
// 变异测试确认过这条有意义：去掉回滚，全套照样绿 —— 那个分支此前无人看守。
// 触发方式是让 tmp 写入失败：把 pin 文件所在的目录变成只读，os.WriteFile 就报错。
func TestUnpinRollsBackInMemoryWhenSaveFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root 无视目录权限，这条测不了")
	}
	store := newTestStore(t)
	pinnedAt, _, err := store.SetSessionPin("root::k", true)
	if err != nil {
		t.Fatalf("SetSessionPin: %v", err)
	}
	dir := filepath.Dir(store.path)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if _, _, err := store.SetSessionPin("root::k", false); err == nil {
		t.Fatal("目录只读时取消置顶应当失败")
	}
	// 内存必须还在，而且时间戳是**原来那个**（回滚成 now 会让它在列表里跳位）。
	after, ok := store.SessionPinnedAt("root::k")
	if !ok {
		t.Fatal("落库失败后内存里的置顶被丢了：下次读盘它会回来，用户看到「取消了」却又出现")
	}
	if !after.Equal(pinnedAt) {
		t.Fatalf("回滚应恢复原时间戳: %v → %v", pinnedAt, after)
	}
}

// 置顶落库失败同理：不能留下「内存里有、盘上没有」的幽灵置顶。
func TestPinRollsBackInMemoryWhenSaveFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root 无视目录权限，这条测不了")
	}
	store := newTestStore(t)
	dir := filepath.Dir(store.path)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if _, pinned, err := store.SetSessionPin("root::k", true); err == nil || pinned {
		t.Fatalf("目录只读时置顶应当失败, pinned=%v err=%v", pinned, err)
	}
	if _, ok := store.SessionPinnedAt("root::k"); ok {
		t.Fatal("落库失败后内存里不该留下这条置顶")
	}
}

// 项目置顶的回滚路径同样要守（与会话置顶是两份独立代码，变异测试各做一次）。
func TestProjectPinRollsBackInMemoryWhenSaveFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root 无视目录权限，这条测不了")
	}
	store := newTestStore(t)
	ts, _, err := store.SetProjectPin("pc::CMAI", true)
	if err != nil {
		t.Fatalf("SetProjectPin: %v", err)
	}
	dir := filepath.Dir(store.path)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if _, _, err := store.SetProjectPin("pc::CMAI", false); err == nil {
		t.Fatal("目录只读时取消项目置顶应当失败")
	}
	after, ok := store.ProjectPins()["pc::CMAI"]
	if !ok {
		t.Fatal("落库失败后内存里的项目置顶被丢了")
	}
	if after != ts {
		t.Fatalf("回滚应恢复原时间戳: %d → %d", ts, after)
	}
}

// SessionScopeKey 必须与前端 scope.ts 逐字节一致 —— 这里分叉会表现为
// 「回填完了但前端看不到置顶」，是个很难查的症状。
func TestSessionScopeKeyMatchesFrontendFormat(t *testing.T) {
	cases := []struct{ node, root, key, want string }{
		{"pc", "CMAI", "s1", "pc::CMAI::s1"},
		{"", "CMAI", "s1", "CMAI::s1"},
		{"local", "mindfs", "abc", "local::mindfs::abc"},
		{"  ", "CMAI", "s1", "CMAI::s1"},
	}
	for _, c := range cases {
		if got := SessionScopeKey(c.node, c.root, c.key); got != c.want {
			t.Errorf("SessionScopeKey(%q,%q,%q) = %q, want %q", c.node, c.root, c.key, got, c.want)
		}
	}
}

// ── 回填（共享偏好 → 按账户存储）──

// 没有文件时才允许回填。
func TestNeedsLegacySeedOnlyWhenNoFile(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStoreAt(dir)
	if err != nil {
		t.Fatalf("NewStoreAt: %v", err)
	}
	if !store.NeedsLegacySeed() {
		t.Fatal("文件不存在时应该允许回填")
	}
	if _, _, err := store.SetSessionPin("root::k", true); err != nil {
		t.Fatalf("SetSessionPin: %v", err)
	}
	reopened, err := NewStoreAt(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if reopened.NeedsLegacySeed() {
		t.Fatal("文件已存在时不该再回填：否则用户取消掉的置顶会在每次重启后自己回来")
	}
}

// 回填是**并集**：已有的键不动、保留原时间戳；垃圾键被丢掉。
func TestSeedProjectsUnions(t *testing.T) {
	store := newTestStore(t)
	existing, _, err := store.SetProjectPin("pc::CMAI", true)
	if err != nil {
		t.Fatalf("SetProjectPin: %v", err)
	}
	if err := store.SeedProjects(map[string]int64{
		"pc::CMAI": 999,  // 已有 → 不该被覆盖
		"pc::docs": 1234, // 新增
		"":         1,    // 空键 → 丢
		"pc::bad":  -5,   // 非法时间 → 丢
	}); err != nil {
		t.Fatalf("SeedProjects: %v", err)
	}
	got := store.ProjectPins()
	if got["pc::CMAI"] != existing {
		t.Fatalf("回填不该覆盖已有键: %d ≠ %d", got["pc::CMAI"], existing)
	}
	if got["pc::docs"] != 1234 {
		t.Fatalf("回填应新增键: %v", got)
	}
	if _, ok := got[""]; ok {
		t.Error("空键不该被写进去")
	}
	if _, ok := got["pc::bad"]; ok {
		t.Error("非法时间戳不该被写进去")
	}
}

// 回填后必须重启也不再回填 —— 这是防止「用户取消 → 重启 → 置顶自己回来」的唯一保证。
func TestSeedProjectsSticksAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStoreAt(dir)
	if err != nil {
		t.Fatalf("NewStoreAt: %v", err)
	}
	if err := store.SeedProjects(map[string]int64{"pc::CMAI": 1000}); err != nil {
		t.Fatalf("SeedProjects: %v", err)
	}
	// 用户取消
	if _, _, err := store.SetProjectPin("pc::CMAI", false); err != nil {
		t.Fatalf("取消: %v", err)
	}
	// 重启后共享偏好里还留着旧值，但不该再被搬回来
	reopened, err := NewStoreAt(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if reopened.NeedsLegacySeed() {
		t.Fatal("回填过的账户不该再需要回填")
	}
	if got := reopened.ProjectPins(); len(got) != 0 {
		t.Fatalf("重启后置顶自己回来了: %v", got)
	}
}
