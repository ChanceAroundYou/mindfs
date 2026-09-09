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
}

type Store struct {
	mu   sync.RWMutex
	path string
	data []NodeConnection
}

func NewStore() (*Store, error) {
	configDir, err := config.MindFSConfigDir()
	if err != nil { return nil, err }
	store := &Store{path: filepath.Join(configDir, nodesFileName)}
	if err := store.load(); err != nil { return nil, err }
	return store, nil
}

func (s *Store) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) { return nil }
		return err
	}
	if len(strings.TrimSpace(string(b))) == 0 { return nil }
	var data []NodeConnection
	if err := json.Unmarshal(b, &data); err != nil { return err }
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
		if n.ID == "" || n.Name == "" || n.URL == "" { continue }
		if _, ok := seen[n.ID]; ok { continue }
		seen[n.ID] = struct{}{}
		out = append(out, n)
	}
	return out
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
	if node.ID == "" { return NodeConnection{}, errors.New("id required") }
	if node.Name == "" { return NodeConnection{}, errors.New("name required") }
	if node.URL == "" { return NodeConnection{}, errors.New("url required") }
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
	if id == "" { return NodeConnection{}, errors.New("id required") }
	if id == "local" { return NodeConnection{}, errors.New("cannot_remove_local") }
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
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil { return err }
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil { return err }
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil { return err }
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(s.path)
		if retryErr := os.Rename(tmp, s.path); retryErr != nil { return err }
	}
	return nil
}
