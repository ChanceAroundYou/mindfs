package auth

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	configpkg "mindfs/server/internal/config"
)

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
	// 方案 B：账户 id 从随机改为按用户名派生，这样 worker 没有账户表也能解析。
	// 迁移在返回前做，旧目录重命名 + id 更新一次落盘。
	if err := store.migrateIDsToDerived(); err != nil {
		return nil, err
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

// migrateIDsToDerived 把旧的随机 id 迁移成按用户名派生的确定性 id。
//
// 为什么需要迁移：改造前 id 是 randomSecret 生成的（本机 u_pc_admin、
// fn u_yQgvkYcI7Na1tzIR），改造后 id 由用户名算出。不迁移的话，
// worker 上按用户名算出的目录名和 primary 上实际的目录名对不上，
// 两边各建一个目录、数据永远不通。
//
// 做法：遍历账户表，id 不等于 DeriveAccountID(username) 的，
// 把 users/<旧id>/ 重命名成 users/<新id>/（存在才改），再更新表并落盘。
// 主账户 id 也跟着改。新目录已存在（哈希碰撞，几乎不可能）时跳过重命名。
func (s *Store) migrateIDsToDerived() error {
	base := filepath.Dir(s.path)
	usersDir := filepath.Join(base, "users")

	changed := false
	for i := range s.users {
		want := DeriveAccountID(s.users[i].Username)
		if s.users[i].ID == want {
			continue
		}
		oldID := s.users[i].ID
		oldDir := filepath.Join(usersDir, oldID)
		newDir := filepath.Join(usersDir, want)
		if info, err := os.Stat(oldDir); err == nil && info.IsDir() {
			if _, err := os.Stat(newDir); err == nil {
				// 新目录已存在（哈希碰撞），跳过重命名，只更新表
				log.Printf("[auth] 账户 %q 的新目录 %s 已存在，跳过重命名", s.users[i].Username, newDir)
			} else if err := os.Rename(oldDir, newDir); err != nil {
				return fmt.Errorf("迁移账户目录 %s → %s 失败: %w", oldID, want, err)
			}
		}
		s.users[i].ID = want
		if s.primaryID == oldID {
			s.primaryID = want
		}
		changed = true
		log.Printf("[auth] 账户 %q 的 id 从 %s 迁移为 %s", s.users[i].Username, oldID, want)
	}
	if changed {
		if err := writeUsers(s.path, s.users, s.primaryID); err != nil {
			return err
		}
	}
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
	if generated {
		log.Printf("[auth] 已生成管理员初始密码: %s", password)
	}
	return []User{{
		ID:           DeriveAccountID("admin"),
		Username:     "admin",
		PasswordHash: string(hash),
		Role:         RoleAdmin,
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
	}}, nil
}
