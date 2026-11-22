package usecase

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// 会话卫生的活跃时长要跨进程重启保留：原先 activeSeconds 只在内存里，每次
// 服务重启都归零，刚重启的机器上看「持续久」永远是 false。这里按会话落一份
// 很小的事实表，启动时回填 lastSeen 与已累计活跃秒数；harness 压缩时把两者
// 一起重置，于是「久」始终表示「距上次压缩的累计活跃时长」。
const hygieneStoreVersion = 1

type hygienePersisted struct {
	Version       int       `json:"version"`
	Scope         string    `json:"scope"`
	ActiveSeconds float64   `json:"active_seconds"`
	LastSeen      time.Time `json:"last_seen"`
}

type hygieneStore struct{ dir string }

func newHygieneStore(configDir string) *hygieneStore {
	if strings.TrimSpace(configDir) == "" {
		return nil
	}
	dir := filepath.Join(configDir, "session-hygiene")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Printf("[session-hygiene] store.disabled err=%v", err)
		return nil
	}
	return &hygieneStore{dir: dir}
}

// NewHygieneTrackerAt 在普通 tracker 上接一份磁盘状态；目录不可用时退化为
// 纯内存，不让一个诊断用的状态表挡住会话发送。
func NewHygieneTrackerAt(configDir string, cfg HygieneConfig) *HygieneTracker {
	t := NewHygieneTracker(cfg)
	store := newHygieneStore(configDir)
	if store == nil {
		return t
	}
	t.store = store
	for scope, saved := range store.load() {
		rec := t.recordLocked(scope)
		rec.activeSeconds = saved.ActiveSeconds
		rec.lastSeen = saved.LastSeen
	}
	return t
}

// persistLocked 在 tracker 已持锁时把当前事实写盘；写失败只记日志，不影响本轮发送。
func (t *HygieneTracker) persistLocked(scope string, rec *hygieneRecord) {
	if t != nil && t.store != nil {
		t.store.save(scope, rec)
	}
}

func (st *hygieneStore) load() map[string]hygienePersisted {
	out := map[string]hygienePersisted{}
	if st == nil || st.dir == "" {
		return out
	}
	entries, err := os.ReadDir(st.dir)
	if err != nil {
		log.Printf("[session-hygiene] store.read_dir err=%v", err)
		return out
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "hy-") || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(st.dir, entry.Name()))
		if err != nil {
			continue
		}
		var saved hygienePersisted
		if err := json.Unmarshal(raw, &saved); err != nil || saved.Version != hygieneStoreVersion || saved.Scope == "" {
			continue
		}
		out[saved.Scope] = saved
	}
	return out
}

func (st *hygieneStore) save(scope string, rec *hygieneRecord) {
	if st == nil || st.dir == "" {
		return
	}
	path := hygieneStorePath(st.dir, scope)
	tmp, err := os.CreateTemp(st.dir, ".hy-*.tmp")
	if err != nil {
		log.Printf("[session-hygiene] store.create_temp err=%v", err)
		return
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		log.Printf("[session-hygiene] store.chmod err=%v", err)
		return
	}
	saved := hygienePersisted{
		Version:       hygieneStoreVersion,
		Scope:         scope,
		ActiveSeconds: rec.activeSeconds,
		LastSeen:      rec.lastSeen,
	}
	err = json.NewEncoder(tmp).Encode(saved)
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(name, path)
	}
	if err != nil {
		log.Printf("[session-hygiene] store.save err=%v", err)
	}
}

// 文件名只用 scope 的哈希，避免把会话 key 直接写进目录项。
func hygieneStorePath(dir, scope string) string {
	sum := sha256.Sum256([]byte(scope))
	return filepath.Join(dir, fmt.Sprintf("hy-%x.json", sum[:12]))
}
