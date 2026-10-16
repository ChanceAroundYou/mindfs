package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

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

// DeriveAccountID 从用户名派生一个确定性的账户 id。
//
// 为什么需要它：worker 没有账户表（方案 B），但数据分区仍然需要 id。
// 账户 id 跨机器稳定后，同一台 worker 上反复解析同一个用户名，
// 得到的目录名一致 —— 否则每次请求都可能落到不同目录，数据全丢。
//
// 用 SHA256 而不是直接用用户名：用户名可能含路径不友好的字符
// （空格、中文、/），而 id 要拿去拼 users/<id>/。
func DeriveAccountID(username string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(username)))
	return "u_" + hex.EncodeToString(sum[:])[:16]
}

// Resolve 把客户端声明的身份解析成**本机**的账户 id。
//
// 为什么需要它：账户表每台机器独立，用户 id 是各自随机生成的（实测本机
// u_pc_admin、另一台 u_2d_hoAd3ZMEimSSH），所以客户端没法把别处的 id 带过来用；
// 跨机器能对齐的只有**用户名**（同一个人在两边都叫 xiaokubao）。
// 先按 id 认，认不到再按用户名认（不区分大小写，与建号时的重名校验一致）。
//
// 找不到返回空串——调用方据此判定「本机没有这个账户」，**不要**回落到主账户，
// 否则会把别人机器上的数据当成当前用户的返回回去。
func (s *Store) Resolve(who string) string {
	want := strings.TrimSpace(who)
	if want == "" || s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.users {
		if s.users[i].ID == want {
			return s.users[i].ID
		}
	}
	for i := range s.users {
		if strings.EqualFold(strings.TrimSpace(s.users[i].Username), want) {
			return s.users[i].ID
		}
	}
	return ""
}

// Exists 判断账户是否存在，接受 id 或用户名（见 Resolve）。
func (s *Store) Exists(id string) bool {
	return s.Resolve(id) != ""
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
