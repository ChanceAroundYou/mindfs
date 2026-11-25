package nodes

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"mindfs/server/internal/config"
)

const nodesFileName = "nodes.json"

type NodeConnection struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	URL   string `json:"url"`
	Color string `json:"color"`
	// Remote 标记这是**另一台机器**上的节点。跨节点请求不带 user=
	// （账户表每台机器独立，id 带不过去），由服务端回落到对方主节点；
	// 认用户名那条老路仍然有效，但调用方不必再猜对方有没有这个账户。
	//
	// 零值 false = 本机节点，行为与加此字段之前逐字节一致，老 nodes.json 免迁移。
	Remote bool `json:"remote,omitempty"`
}

type Store struct {
	mu   sync.RWMutex
	path string
	data []NodeConnection
}

func NewStore() (*Store, error) {
	configDir, err := config.MindFSConfigDir()
	if err != nil {
		return nil, err
	}
	return NewStoreAt(configDir)
}

// NewStoreAt 把节点表放在指定目录（多账户：每个账户一套节点）。
func NewStoreAt(configDir string) (*Store, error) {
	store := &Store{path: filepath.Join(configDir, nodesFileName)}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return nil
	}
	var data []NodeConnection
	if err := json.Unmarshal(b, &data); err != nil {
		return err
	}
	s.data = normalize(data)
	return nil
}

func normalize(in []NodeConnection) []NodeConnection {
	out := make([]NodeConnection, 0, len(in))
	seen := map[string]struct{}{}
	for _, n := range in {
		n.ID = strings.TrimSpace(n.ID)
		n.Name = strings.TrimSpace(n.Name)
		n.URL = strings.TrimSpace(strings.TrimRight(n.URL, "/"))
		n.Color = strings.TrimSpace(n.Color)
		if n.ID == "" || n.Name == "" || n.URL == "" {
			continue
		}
		if _, ok := seen[n.ID]; ok {
			continue
		}
		seen[n.ID] = struct{}{}
		out = append(out, n)
	}
	return out
}

// NameConflicts 判定一次整表写入是否**新引入**了重名（trim + 忽略大小写）。
//
// 语义按「只拒绝新增/改名的冲突，不强行修历史数据」定：
//   - 同一对 id 在 existing 里就已经同名并存 → 是历史数据，放行；
//   - 其余任何两个不同 id 同名 → 拒绝，返回其中后一个作为冲突提示。
//
// 为什么按整表比较而不是逐条 upsert：前端保存节点表走 PUT（整表替换），
// 逐条检查会被整体替换绕过。
func NameConflicts(existing, incoming []NodeConnection) (NodeConnection, bool) {
	fold := func(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
	existingNames := make(map[string]string, len(existing))
	for _, n := range existing {
		existingNames[n.ID] = fold(n.Name)
	}
	historicallyPaired := func(a, b string) bool {
		nameA, okA := existingNames[a]
		nameB, okB := existingNames[b]
		return okA && okB && nameA != "" && nameA == nameB
	}

	groups := map[string][]NodeConnection{}
	for _, n := range incoming {
		key := fold(n.Name)
		if key == "" {
			continue
		}
		groups[key] = append(groups[key], n)
	}
	for _, group := range groups {
		for i := 0; i < len(group); i++ {
			for j := i + 1; j < len(group); j++ {
				if historicallyPaired(group[i].ID, group[j].ID) {
					continue
				}
				return group[j], true
			}
		}
	}
	return NodeConnection{}, false
}

func (s *Store) Get(id string) (NodeConnection, bool) {
	id = strings.TrimSpace(id)
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, n := range s.data {
		if n.ID == id {
			return n, true
		}
	}
	return NodeConnection{}, false
}

func (s *Store) List() []NodeConnection {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]NodeConnection, len(s.data))
	copy(out, s.data)
	return out
}

func (s *Store) Replace(nodes []NodeConnection) error {
	nodes = normalize(nodes)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = nodes
	return s.saveLocked()
}

func (s *Store) Upsert(node NodeConnection) (NodeConnection, error) {
	node.ID = strings.TrimSpace(node.ID)
	node.Name = strings.TrimSpace(node.Name)
	node.URL = strings.TrimSpace(strings.TrimRight(node.URL, "/"))
	node.Color = strings.TrimSpace(node.Color)
	if node.ID == "" {
		return NodeConnection{}, errors.New("id required")
	}
	if node.Name == "" {
		return NodeConnection{}, errors.New("name required")
	}
	if node.URL == "" {
		return NodeConnection{}, errors.New("url required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, n := range s.data {
		if n.ID == node.ID {
			s.data[i] = node
			return node, s.saveLocked()
		}
	}
	s.data = append(s.data, node)
	return node, s.saveLocked()
}

func (s *Store) Remove(id string) (NodeConnection, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return NodeConnection{}, errors.New("id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, n := range s.data {
		if n.ID == id {
			removed := n
			s.data = append(s.data[:i], s.data[i+1:]...)
			return removed, s.saveLocked()
		}
	}
	return NodeConnection{}, errors.New("node not found")
}

func (s *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(s.path)
		if retryErr := os.Rename(tmp, s.path); retryErr != nil {
			return err
		}
	}
	return nil
}
