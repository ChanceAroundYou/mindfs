// Package auth 提供 MindFS 的账户表与登录校验。
//
// 注意（设计约束，见 docs/multi-user-prd.md）：这是「多配置档」，不是安全边界。
// API 层不做鉴权，服务端按客户端声明的账户 id 分区存储，因此密码是装饰性的——
// 把请求里的 user= 换掉就能读走别人的项目与会话。不要拿它当隔离用。
package auth

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	configpkg "mindfs/server/internal/config"
)

const (
	// 9 字节 → 12 个 base64url 字符，手机上敲得动
	passwordBytes = 9
	usersFileName = "users.json"
	legacyLogin   = "login.json"
)

// 角色
const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

var (
	// ErrInvalidCredentials 用户名或密码不对（刻意不区分，避免枚举账户）
	ErrInvalidCredentials = errors.New("invalid_credentials")
	// ErrDisabled 账户被禁用
	ErrDisabled = errors.New("user_disabled")
	// ErrUsernameTaken 用户名重复
	ErrUsernameTaken = errors.New("username_taken")
	// ErrLastAdmin 不允许删掉/降级最后一个管理员
	ErrLastAdmin = errors.New("last_admin")
	// ErrUserNotFound 账户不存在
	ErrUserNotFound = errors.New("user_not_found")
	// ErrPrimaryUser 主账户不允许删除（它拥有迁移前的存量数据）
	ErrPrimaryUser = errors.New("primary_user_protected")
)

// User 是一条账户记录。密码只以 bcrypt 哈希落盘。
type User struct {
	ID           string `json:"id"`
	Username     string `json:"username"`
	PasswordHash string `json:"password_hash"`
	Role         string `json:"role"`
	CreatedAt    string `json:"created_at"`
	Disabled     bool   `json:"disabled"`
}

// PublicUser 是可以安全回给前端的投影（无口令哈希）。
type PublicUser struct {
	ID        string `json:"id"`
	Username  string `json:"username"`
	Role      string `json:"role"`
	CreatedAt string `json:"created_at"`
	Disabled  bool   `json:"disabled"`
	// Primary 标记主账户：它的数据根是 <cfg>/ 与项目内 .mindfs/（存量数据归属）
	Primary bool `json:"primary,omitempty"`
}

// Public 是回给前端的投影（无口令哈希）。任何出网响应都必须走这里。
func (u User) Public() PublicUser {
	return PublicUser{
		ID:        u.ID,
		Username:  u.Username,
		Role:      u.Role,
		CreatedAt: u.CreatedAt,
		Disabled:  u.Disabled,
	}
}

type usersFile struct {
	Users []User `json:"users"`
	// PrimaryUserID 是「主账户」：它的数据根是 <cfg>/ 与项目内 .mindfs/（迁移前的存量数据），
	// 其余账户的数据根是 <cfg>/users/<id>/。见 docs/multi-user-prd.md §3.3。
	PrimaryUserID string `json:"primary_user_id,omitempty"`
}

// Store 持有账户表。落盘为 <configDir>/users.json（0600）。
type Store struct {
	mu        sync.Mutex
	path      string
	users     []User
	primaryID string
}

// DefaultConfigPath 返回账户表路径。
func DefaultConfigPath() (string, error) {
	dir, err := configpkg.MindFSConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, usersFileName), nil
}

// EnsureStore 读取账户表；不存在则从旧的单密码 login.json 迁移出 admin 账户。
func EnsureStore() (*Store, error) {
	path, err := DefaultConfigPath()
	if err != nil {
		return nil, err
	}
	return EnsureStoreAt(path)
}

// EnsureStoreAt 是 EnsureStore 的指定路径版本（测试与多实例用）。
// 迁移用的 login.json 取同目录下的同名文件。
func EnsureStoreAt(path string) (*Store, error) {
	store := &Store{path: path}
	file, err := loadUsers(path)
	if err != nil {
		return nil, err
	}
	if len(file.Users) == 0 {
		file.Users, err = migrateFromLegacy(path)
		if err != nil {
			return nil, err
		}
		if err := writeUsers(path, file.Users, file.PrimaryUserID); err != nil {
			return nil, err
		}
		if len(file.Users) > 0 {
			log.Printf("[auth] 已从 %s 迁移出管理员账户 %q", filepath.Join(filepath.Dir(path), legacyLogin), file.Users[0].Username)
		}
	}
	store.users = file.Users
	store.primaryID = strings.TrimSpace(file.PrimaryUserID)

	// 主账户缺省：迁移出来的第一个管理员；旧文件没有该字段时在这里补齐并落盘
	if store.indexLocked(store.primaryID) < 0 {
		if id := store.firstAdminLocked(); id != "" {
			store.primaryID = id
			if err := writeUsers(path, store.users, store.primaryID); err != nil {
				return nil, err
			}
			log.Printf("[auth] 主账户（存量数据归属）判定为 %q", store.users[store.indexLocked(id)].Username)
		}
	}
	log.Printf("[auth] 账户表已加载：%d 个账户（%s）", len(store.users), path)
	return store, nil
}

func (s *Store) firstAdminLocked() string {
	for _, u := range s.users {
		if u.Role == RoleAdmin {
			return u.ID
		}
	}
	return ""
}

// PrimaryUserID 返回主账户 id（空串表示账户表为空）。
func (s *Store) PrimaryUserID() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.primaryID
}

// SetPrimary 转移主账户身份（存量数据的归属）。只允许指向启用中的管理员。
func (s *Store) SetPrimary(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.indexLocked(id)
	if idx < 0 {
		return ErrUserNotFound
	}
	if s.users[idx].Role != RoleAdmin || s.users[idx].Disabled {
		return errors.New("主账户必须是一个启用中的管理员")
	}
	if err := writeUsers(s.path, s.users, s.users[idx].ID); err != nil {
		return err
	}
	s.primaryID = s.users[idx].ID
	return nil
}

// loadUsers 读账户表；文件不存在返回空表而非错误。
func loadUsers(path string) (usersFile, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return usersFile{}, nil
		}
		return usersFile{}, err
	}
	var parsed usersFile
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return usersFile{}, fmt.Errorf("解析账户表失败 %s: %w", path, err)
	}
	out := make([]User, 0, len(parsed.Users))
	for _, u := range parsed.Users {
		u.Username = strings.TrimSpace(u.Username)
		if u.ID == "" || u.Username == "" {
			continue
		}
		if u.Role == "" {
			u.Role = RoleUser
		}
		out = append(out, u)
	}
	return usersFile{Users: out, PrimaryUserID: strings.TrimSpace(parsed.PrimaryUserID)}, nil
}

// writeUsers 原子落盘（临时文件 + rename），避免半截 JSON 毁掉账户表。
func writeUsers(path string, users []User, primaryID string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(usersFile{Users: users, PrimaryUserID: primaryID}, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".users-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(payload); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// migrateFromLegacy 用旧的单密码 login.json 建出第一个管理员。
// 没有旧文件就生成一个随机密码并打进日志（与旧行为一致）。
func migrateFromLegacy(usersPath string) ([]User, error) {
	legacyPath := filepath.Join(filepath.Dir(usersPath), legacyLogin)
	password := ""
	if payload, err := os.ReadFile(legacyPath); err == nil {
		var cfg struct {
			Password string `json:"password"`
		}
		if err := json.Unmarshal(payload, &cfg); err != nil {
			return nil, fmt.Errorf("解析 %s 失败: %w", legacyPath, err)
		}
		password = strings.TrimSpace(cfg.Password)
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	generated := password == ""
	if generated {
		var err error
		password, err = randomSecret(passwordBytes)
		if err != nil {
			return nil, err
		}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	id, err := randomSecret(12)
	if err != nil {
		return nil, err
	}
	if generated {
		log.Printf("[auth] 已生成管理员初始密码: %s", password)
	}
	return []User{{
		ID:           "u_" + id,
		Username:     "admin",
		PasswordHash: string(hash),
		Role:         RoleAdmin,
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
	}}, nil
}

// Authenticate 校验用户名 + 口令。失败一律返回 ErrInvalidCredentials（不区分"用户不存在"与"密码错"）。
func (s *Store) Authenticate(username, password string) (User, error) {
	if s == nil {
		return User{}, ErrInvalidCredentials
	}
	want := strings.TrimSpace(username)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.users {
		if !strings.EqualFold(u.Username, want) {
			continue
		}
		if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
			return User{}, ErrInvalidCredentials
		}
		if u.Disabled {
			return User{}, ErrDisabled
		}
		return u, nil
	}
	// 账户不存在也走一次 bcrypt，避免时序上暴露用户名是否存在
	_ = bcrypt.CompareHashAndPassword([]byte("$2a$10$................................"), []byte(password))
	return User{}, ErrInvalidCredentials
}

// List 返回全部账户投影，管理员在前，其余按用户名排序。
func (s *Store) List() []PublicUser {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listLocked()
}

func (s *Store) listLocked() []PublicUser {
	out := make([]PublicUser, 0, len(s.users))
	for _, u := range s.users {
		out = append(out, s.publicLocked(u))
	}
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].Role == RoleAdmin) != (out[j].Role == RoleAdmin) {
			return out[i].Role == RoleAdmin
		}
		return out[i].Username < out[j].Username
	})
	return out
}

func (s *Store) publicLocked(u User) PublicUser {
	public := u.Public()
	public.Primary = s.primaryID != "" && u.ID == s.primaryID
	return public
}

// Get 按 id 取账户。
func (s *Store) Get(id string) (PublicUser, error) {
	if s == nil {
		return PublicUser{}, ErrUserNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.indexLocked(id)
	if idx < 0 {
		return PublicUser{}, ErrUserNotFound
	}
	return s.publicLocked(s.users[idx]), nil
}

// Exists 判断账户 id 是否存在（供按账户分区使用）。
func (s *Store) Exists(id string) bool {
	_, err := s.Get(id)
	return err == nil
}

func (s *Store) indexLocked(id string) int {
	want := strings.TrimSpace(id)
	for i := range s.users {
		if s.users[i].ID == want {
			return i
		}
	}
	return -1
}

func (s *Store) countAdminsLocked() int {
	n := 0
	for _, u := range s.users {
		if u.Role == RoleAdmin && !u.Disabled {
			n++
		}
	}
	return n
}

// Create 新建账户。
func (s *Store) Create(username, password, role string) (PublicUser, error) {
	name := strings.TrimSpace(username)
	if name == "" {
		return PublicUser{}, errors.New("用户名不能为空")
	}
	if len(password) < 6 {
		return PublicUser{}, errors.New("密码至少 6 位")
	}
	if role != RoleAdmin {
		role = RoleUser
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return PublicUser{}, err
	}
	id, err := randomSecret(12)
	if err != nil {
		return PublicUser{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.users {
		if strings.EqualFold(u.Username, name) {
			return PublicUser{}, ErrUsernameTaken
		}
	}
	user := User{
		ID:           "u_" + id,
		Username:     name,
		PasswordHash: string(hash),
		Role:         role,
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
	}
	next := append(append([]User{}, s.users...), user)
	if err := writeUsers(s.path, next, s.primaryID); err != nil {
		return PublicUser{}, err
	}
	s.users = next
	return s.publicLocked(user), nil
}

// UpdateInput 描述一次修改；指针为 nil 表示该字段不动。
type UpdateInput struct {
	Username *string
	Password *string
	Role     *string
	Disabled *bool
}

// Update 改账户。拒绝把最后一个启用中的管理员降级或禁用。
func (s *Store) Update(id string, in UpdateInput) (PublicUser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.indexLocked(id)
	if idx < 0 {
		return PublicUser{}, ErrUserNotFound
	}
	next := append([]User{}, s.users...)
	cur := next[idx]

	if in.Username != nil {
		name := strings.TrimSpace(*in.Username)
		if name == "" {
			return PublicUser{}, errors.New("用户名不能为空")
		}
		for i, u := range next {
			if i != idx && strings.EqualFold(u.Username, name) {
				return PublicUser{}, ErrUsernameTaken
			}
		}
		cur.Username = name
	}
	if in.Password != nil {
		if len(*in.Password) < 6 {
			return PublicUser{}, errors.New("密码至少 6 位")
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(*in.Password), bcrypt.DefaultCost)
		if err != nil {
			return PublicUser{}, err
		}
		cur.PasswordHash = string(hash)
	}
	if in.Role != nil {
		role := RoleUser
		if *in.Role == RoleAdmin {
			role = RoleAdmin
		}
		if cur.Role == RoleAdmin && role != RoleAdmin && s.countAdminsLocked() <= 1 {
			return PublicUser{}, ErrLastAdmin
		}
		cur.Role = role
	}
	if in.Disabled != nil {
		if *in.Disabled && cur.Role == RoleAdmin && s.countAdminsLocked() <= 1 {
			return PublicUser{}, ErrLastAdmin
		}
		cur.Disabled = *in.Disabled
	}
	next[idx] = cur
	if err := writeUsers(s.path, next, s.primaryID); err != nil {
		return PublicUser{}, err
	}
	s.users = next
	return s.publicLocked(cur), nil
}

// Delete 删账户。拒绝删掉最后一个启用中的管理员。
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.indexLocked(id)
	if idx < 0 {
		return ErrUserNotFound
	}
	if s.users[idx].Role == RoleAdmin && s.countAdminsLocked() <= 1 {
		return ErrLastAdmin
	}
	// 主账户拥有迁移前的存量数据（<cfg>/ 与项目内 .mindfs/），删了那些数据就没有归属了。
	// 要删就先 SetPrimary 转移出去。
	if s.primaryID != "" && s.users[idx].ID == s.primaryID {
		return ErrPrimaryUser
	}
	next := make([]User, 0, len(s.users)-1)
	next = append(next, s.users[:idx]...)
	next = append(next, s.users[idx+1:]...)
	if err := writeUsers(s.path, next, s.primaryID); err != nil {
		return err
	}
	s.users = next
	return nil
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
