// Package auth 是 Web 主页面的一道登录闸门。
//
// 刻意只做「页面级」：所有 REST/WS 接口仍然匿名可访问，这里只回答一个问题——
// 「这个浏览器会话输对过密码没有」。密码明文存在 <config>/login.json（0600），
// 改完重启服务即生效。
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	configpkg "mindfs/server/internal/config"
)

const (
	// 9 字节 → 12 个 base64url 字符，手机上敲得动
	passwordBytes = 9
	tokenBytes    = 32
	// 浏览器记 7 天；服务重启也会让所有 token 失效
	sessionTTL = 7 * 24 * time.Hour
)

// Config 是磁盘上的密码文件结构。
type Config struct {
	Password string `json:"password"`
}

// Store 持有密码与已签发的登录 token。
type Store struct {
	mu       sync.Mutex
	password string
	tokens   map[string]time.Time
}

// DefaultConfigPath 返回密码文件路径。
func DefaultConfigPath() (string, error) {
	dir, err := configpkg.MindFSConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "login.json"), nil
}

// EnsureStore 读取密码文件，不存在就生成一个随机密码并落盘。
func EnsureStore() (*Store, error) {
	path, err := DefaultConfigPath()
	if err != nil {
		return nil, err
	}
	return EnsureStoreAt(path)
}

// EnsureStoreAt 是 EnsureStore 的指定路径版本（测试与多实例用）。
func EnsureStoreAt(path string) (*Store, error) {
	cfg, generated, err := loadOrCreate(path)
	if err != nil {
		return nil, err
	}
	if generated {
		log.Printf("[auth] 已生成主页面登录密码: %s", cfg.Password)
	}
	log.Printf("[auth] 主页面登录已启用，密码文件: %s", path)
	return &Store{password: cfg.Password, tokens: map[string]time.Time{}}, nil
}

// Password 返回明文密码（仅测试与本地工具使用）。
func (s *Store) Password() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.password
}

func loadOrCreate(path string) (Config, bool, error) {
	payload, err := os.ReadFile(path)
	switch {
	case err == nil:
		var cfg Config
		if err := json.Unmarshal(payload, &cfg); err != nil {
			return Config{}, false, err
		}
		if password := strings.TrimSpace(cfg.Password); password != "" {
			cfg.Password = password
			return cfg, false, nil
		}
	case !os.IsNotExist(err):
		return Config{}, false, err
	}

	password, err := randomSecret(passwordBytes)
	if err != nil {
		return Config{}, false, err
	}
	cfg := Config{Password: password}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return Config{}, false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Config{}, false, err
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o600); err != nil {
		return Config{}, false, err
	}
	return cfg, true, nil
}

// Verify 校验明文密码。
func (s *Store) Verify(password string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return subtle.ConstantTimeCompare([]byte(strings.TrimSpace(password)), []byte(s.password)) == 1
}

// Issue 签发一个登录 token。
func (s *Store) Issue() (string, error) {
	token, err := randomSecret(tokenBytes)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tokens == nil {
		s.tokens = map[string]time.Time{}
	}
	for id, expiry := range s.tokens {
		if time.Now().UTC().After(expiry) {
			delete(s.tokens, id)
		}
	}
	s.tokens[token] = time.Now().UTC().Add(sessionTTL)
	return token, nil
}

// Valid 判断 token 是否仍然有效。
func (s *Store) Valid(token string) bool {
	if s == nil || token == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	expiry, ok := s.tokens[token]
	return ok && time.Now().UTC().Before(expiry)
}

func randomSecret(n int) (string, error) {
	if n <= 0 {
		return "", errors.New("positive byte length required")
	}
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
