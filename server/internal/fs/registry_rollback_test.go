package fs

import "testing"

// 落盘失败必须回滚内存。
//
// 为什么：这些方法都是「先改内存、再 saveLocked()」。saveLocked 失败时若不回滚，
// 失败的写入会**留在列表里**，用户看到项目出现了、点进去却什么都没有 ——
// 只读空工作区（path==""，即「这个账户在本机没有数据」）正是这条路径的高频场景。
// Rename 早就做了回滚（previousDirs/previousOrder），这三个方法漏了；
// 本测试把同一条纪律钉到全部「先改内存再落盘」的方法上。
func TestRegistryRollsBackInMemoryStateOnSaveFailure(t *testing.T) {
	t.Run("upsert 不留幽灵条目", func(t *testing.T) {
		r := &Registry{path: "", dirs: map[string]RootInfo{}, order: nil}
		if _, err := r.Upsert("/tmp/ghost"); err == nil {
			t.Fatal("path 为空时 Upsert 必须报错")
		}
		if got := r.List(); len(got) != 0 {
			t.Fatalf("落盘失败后列表必须仍为空，实际 %d 条：%v", len(got), got)
		}
	})

	t.Run("remove 不误删条目", func(t *testing.T) {
		r := &Registry{
			path:  "",
			dirs:  map[string]RootInfo{"keep": NewRootInfo("keep", "keep", "/tmp/keep")},
			order: []string{"keep"},
		}
		if _, err := r.Remove("/tmp/keep"); err == nil {
			t.Fatal("path 为空时 Remove 必须报错")
		}
		if got := r.List(); len(got) != 1 {
			t.Fatalf("落盘失败后原条目必须保留，实际 %d 条", len(got))
		}
	})

	t.Run("update_display_name 不回滚显示名", func(t *testing.T) {
		r := &Registry{
			path:  "",
			dirs:  map[string]RootInfo{"keep": NewRootInfo("keep", "keep", "/tmp/keep")},
			order: []string{"keep"},
		}
		if _, err := r.UpdateDisplayName("keep", "别名"); err == nil {
			t.Fatal("path 为空时 UpdateDisplayName 必须报错")
		}
		got, ok := r.Get("keep")
		if !ok {
			t.Fatal("落盘失败后条目必须还在")
		}
		if got.DisplayName != "" {
			t.Fatalf("落盘失败后显示名必须回滚，实际 %q", got.DisplayName)
		}
	})
}
