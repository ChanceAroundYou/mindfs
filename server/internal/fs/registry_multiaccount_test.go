package fs

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 两个账户添加同一个项目时，meta（会话库/任务库/文件元数据）必须落在各自账户目录，
// 且都不能落进项目里的 .mindfs —— 这是多账户不串号的底线。
func TestAccountRegistriesNeverShareMetaForSameProject(t *testing.T) {
	project := t.TempDir()
	// 项目里预先存在 .mindfs（主账户的历史数据），非主账户绝不能碰它
	projectMeta := filepath.Join(project, metaDirName)
	if err := os.MkdirAll(projectMeta, 0o755); err != nil {
		t.Fatalf("mkdir project meta: %v", err)
	}

	acctA := t.TempDir()
	acctB := t.TempDir()
	regA := NewRegistryAt(filepath.Join(acctA, "registry.json"), filepath.Join(acctA, "meta"))
	regB := NewRegistryAt(filepath.Join(acctB, "registry.json"), filepath.Join(acctB, "meta"))

	rootA, err := regA.Upsert(project)
	if err != nil {
		t.Fatalf("upsert A: %v", err)
	}
	rootB, err := regB.Upsert(project)
	if err != nil {
		t.Fatalf("upsert B: %v", err)
	}

	metaA, metaB := rootA.MetaDir(), rootB.MetaDir()
	if metaA == "" || metaB == "" {
		t.Fatalf("empty meta dir: A=%q B=%q", metaA, metaB)
	}
	if metaA == metaB {
		t.Fatalf("accounts share the same meta dir: %s", metaA)
	}
	for name, meta := range map[string]string{"A": metaA, "B": metaB} {
		if meta == projectMeta {
			t.Fatalf("account %s resolves meta into the project's .mindfs: %s", name, meta)
		}
		if rel, err := filepath.Rel(project, meta); err == nil && !strings.HasPrefix(rel, "..") {
			t.Fatalf("account %s meta %s is inside the project %s", name, meta, project)
		}
	}
	// 账户 meta 必须落在各自账户根下
	checks := []struct {
		name, accountRoot, meta string
	}{
		{"A", acctA, metaA},
		{"B", acctB, metaB},
	}
	for _, c := range checks {
		want := filepath.Join(c.accountRoot, "meta", filepath.Base(project))
		if c.meta != want {
			t.Fatalf("account %s meta = %q, want %q", c.name, c.meta, want)
		}
	}
	if rootA.MetaLocation != MetaLocationHome || rootB.MetaLocation != MetaLocationHome {
		t.Fatalf("account roots must be stamped home meta: A=%q B=%q", rootA.MetaLocation, rootB.MetaLocation)
	}
}

// 主账户（metaRoot 为空）必须保持旧行为：默认项目内 meta，改动前什么样现在什么样。
func TestPrimaryRegistryKeepsLegacyProjectMeta(t *testing.T) {
	project := t.TempDir()
	reg := NewRegistry(filepath.Join(t.TempDir(), "registry.json"))

	root, err := reg.Upsert(project)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if root.MetaRoot != "" {
		t.Fatalf("primary root carries a meta root: %q", root.MetaRoot)
	}
	if got, want := root.MetaDir(), filepath.Join(project, metaDirName); got != want {
		t.Fatalf("primary MetaDir = %q, want %q", got, want)
	}
}

// MetaRoot 不落盘：同一个项目被不同账户添加时，路径必须由所属账户决定。
func TestAccountMetaRootIsNotPersisted(t *testing.T) {
	project := t.TempDir()
	acct := t.TempDir()
	registryPath := filepath.Join(acct, "registry.json")
	metaRoot := filepath.Join(acct, "meta")

	reg := NewRegistryAt(registryPath, metaRoot)
	if _, err := reg.Upsert(project); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	payload, err := os.ReadFile(registryPath)
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	// 只查小写 JSON key：t.TempDir() 路径里含测试函数名（自带 "MetaRoot" 字样），
	// 拿 "MetaRoot" 当探针会命中自己的临时路径。
	if bytes.Contains(payload, []byte(`"meta_root"`)) {
		t.Fatalf("MetaRoot leaked into registry.json: %s", payload)
	}

	// 重新装载同一个文件但换个账户根，meta 必须跟着新账户走
	other := t.TempDir()
	reloaded := NewRegistryAt(registryPath, filepath.Join(other, "meta"))
	if err := reloaded.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	list := reloaded.List()
	if len(list) != 1 {
		t.Fatalf("reloaded %d roots, want 1", len(list))
	}
	want := filepath.Join(other, "meta", filepath.Base(project))
	if got := list[0].MetaDir(); got != want {
		t.Fatalf("reloaded MetaDir = %q, want %q", got, want)
	}
}

func containsField(payload []byte, field string) bool {
	return len(payload) > 0 && (indexOf(payload, field) >= 0)
}

func indexOf(haystack []byte, needle string) int {
	n := len(needle)
	for i := 0; i+n <= len(haystack); i++ {
		if string(haystack[i:i+n]) == needle {
			return i
		}
	}
	return -1
}
