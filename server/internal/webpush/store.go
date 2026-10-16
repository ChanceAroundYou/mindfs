package webpush

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"mindfs/server/internal/config"
)

type Store struct {
	mu   sync.Mutex
	path string
}

func NewStore() (*Store, error) {
	configDir, err := config.MindFSConfigDir()
	if err != nil {
		return nil, err
	}
	return NewStoreAt(configDir), nil
}

// NewStoreAt 把订阅表放在指定目录（多账户：每个账户一套订阅）。
// CUSTOM(G-AJ): 多账户各一套订阅表；NewStore 只是它的默认目录特例
func NewStoreAt(configDir string) *Store {
	return &Store{path: filepath.Join(configDir, subscriptionsFileName)}
}

type SubscriptionKeys struct {
	Auth   string `json:"auth"`
	P256dh string `json:"p256dh"`
}

type Subscription struct {
	ID        string           `json:"id"`
	Endpoint  string           `json:"endpoint"`
	Keys      SubscriptionKeys `json:"keys"`
	UserAgent string           `json:"user_agent,omitempty"`
	Platform  string           `json:"platform,omitempty"`
	Enabled   bool             `json:"enabled"`
	LastError string           `json:"last_error,omitempty"`
	CreatedAt time.Time        `json:"created_at"`
	UpdatedAt time.Time        `json:"updated_at"`
}

func (s *Store) List() ([]Subscription, error) {
	if s == nil {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

func (s *Store) GetByEndpoint(endpoint string) (Subscription, bool, error) {
	if s == nil {
		return Subscription{}, false, nil
	}
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return Subscription{}, false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := s.loadLocked()
	if err != nil {
		return Subscription{}, false, err
	}
	for _, item := range items {
		if item.Endpoint == endpoint {
			return item, true, nil
		}
	}
	return Subscription{}, false, nil
}

func (s *Store) Upsert(in Subscription) (Subscription, error) {
	if s == nil {
		return Subscription{}, errors.New("web push store not configured")
	}
	in.Endpoint = strings.TrimSpace(in.Endpoint)
	in.Keys.Auth = strings.TrimSpace(in.Keys.Auth)
	in.Keys.P256dh = strings.TrimSpace(in.Keys.P256dh)
	if in.Endpoint == "" || in.Keys.Auth == "" || in.Keys.P256dh == "" {
		return Subscription{}, errors.New("web push subscription endpoint and keys are required")
	}
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := s.loadLocked()
	if err != nil {
		return Subscription{}, err
	}
	for i := range items {
		if items[i].Endpoint != in.Endpoint {
			continue
		}
		items[i].Keys = in.Keys
		items[i].UserAgent = strings.TrimSpace(in.UserAgent)
		items[i].Platform = strings.TrimSpace(in.Platform)
		items[i].Enabled = true
		items[i].LastError = ""
		items[i].UpdatedAt = now
		if err := s.saveLocked(items); err != nil {
			return Subscription{}, err
		}
		return items[i], nil
	}
	in.ID = subscriptionID(in.Endpoint)
	in.Enabled = true
	in.CreatedAt = now
	in.UpdatedAt = now
	items = append(items, in)
	if err := s.saveLocked(items); err != nil {
		return Subscription{}, err
	}
	return in, nil
}

func (s *Store) Delete(endpoint string) error {
	if s == nil {
		return nil
	}
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := s.loadLocked()
	if err != nil {
		return err
	}
	next := items[:0]
	for _, item := range items {
		if item.Endpoint != endpoint {
			next = append(next, item)
		}
	}
	return s.saveLocked(next)
}

func (s *Store) DeleteByID(id string) error {
	if s == nil {
		return nil
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := s.loadLocked()
	if err != nil {
		return err
	}
	next := items[:0]
	for _, item := range items {
		if item.ID != id {
			next = append(next, item)
		}
	}
	return s.saveLocked(next)
}

func (s *Store) MarkError(id, message string) error {
	if s == nil {
		return nil
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := s.loadLocked()
	if err != nil {
		return err
	}
	for i := range items {
		if items[i].ID == id {
			items[i].LastError = strings.TrimSpace(message)
			items[i].UpdatedAt = time.Now().UTC()
			break
		}
	}
	return s.saveLocked(items)
}

func (s *Store) loadLocked() ([]Subscription, error) {
	b, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Subscription{}, nil
		}
		return nil, err
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return []Subscription{}, nil
	}
	var items []Subscription
	if err := json.Unmarshal(b, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *Store) saveLocked(items []Subscription) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(items, "", "  ")
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
