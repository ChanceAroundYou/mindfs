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
	// PresenceCapable 由客户端在订阅上报时声明（`presence: true`）：此后服务端只在这个
	// 订阅还有页面心跳时才投递。升级前落库的旧订阅保持 false（fail-open），
	// 避免「服务端先升级、浏览器还在跑旧 bundle」时把合法通知一起静音。
	// 一旦置真就不再回落 —— 它描述的是客户端能力，不是某一次请求的参数。
	PresenceCapable bool      `json:"presence_capable,omitempty"`
	LastError       string    `json:"last_error,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
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
		items[i].PresenceCapable = items[i].PresenceCapable || in.PresenceCapable
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

// MarkPresenceCapable 把 endpoint 对应的订阅标成具备 presence 能力。
//
// 收到任意 presence 上报（页面心跳、关页 beacon、service worker 零窗口自愈）都证明
// 页面跑的是新版本。旧订阅若等浏览器下次重存订阅才补声明，两次加载之间会继续
// fail-open —— 用户看到的就是「已经升级了，关页还弹」。只提升标志位：不改 Enabled、
// 不刷新 UpdatedAt（心跳每 30 秒一次，不能变成持续写盘）；未知 endpoint 直接忽略，
// 不能凭空造一条没有密钥的记录。
func (s *Store) MarkPresenceCapable(endpoint string) error {
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
	for i := range items {
		if items[i].Endpoint != endpoint {
			continue
		}
		if items[i].PresenceCapable {
			return nil
		}
		items[i].PresenceCapable = true
		return s.saveLocked(items)
	}
	return nil
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
