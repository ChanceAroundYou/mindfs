package auth

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// 同步内核专用入口。与 Create / Update / Delete 的差别：
//
//   - 口令 hash 原样搬运 —— 同步的是账户表本身，不是「再设一次密码」。
//   - 时间戳、角色、禁用状态按给定值落库，不掺 time.Now()，否则应用完一条记录
//     本地算出的 hash 与远端不同，两边会永远认为没对齐。
//   - 幂等：同一份记录应用两次，表内容不变。

// ErrDerivedIDMismatch 表示记录里的账户 id 与「用户名派生的 id」不一致。
// id 是派生值（见 DeriveAccountID），漂移等于两台机器对不上同一个账户。
var ErrDerivedIDMismatch = errors.New("account_id_not_derived")

// ExportUsers 返回整份账户表（含口令 hash）与主账户 id 的深拷贝。
func (s *Store) ExportUsers() ([]User, string) {
	if s == nil {
		return nil, ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]User, len(s.users))
	copy(out, s.users)
	return out, s.primaryID
}

// ApplyUser 按用户名 upsert 一条账户记录（含口令 hash）。
//
// 按用户名而不是按 id 对齐：id 是派生值，用户名才是跨机器稳定的身份
// （与 Resolve 的认人顺序一致）。
func (s *Store) ApplyUser(u User) error {
	u.Username = strings.TrimSpace(u.Username)
	if u.Username == "" {
		return errors.New("username_required")
	}
	if u.Role != RoleAdmin && u.Role != RoleUser {
		return fmt.Errorf("invalid_role: %q", u.Role)
	}
	if strings.TrimSpace(u.PasswordHash) == "" {
		return errors.New("password_hash_required")
	}
	want := DeriveAccountID(u.Username)
	if u.ID == "" {
		u.ID = want
	} else if u.ID != want {
		return fmt.Errorf("%w: got %q, want %q", ErrDerivedIDMismatch, u.ID, want)
	}
	if strings.TrimSpace(u.CreatedAt) == "" {
		u.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	next := append([]User{}, s.users...)
	idx := -1
	for i, cur := range next {
		if strings.EqualFold(cur.Username, u.Username) {
			idx = i
			break
		}
	}
	if idx >= 0 {
		cur := next[idx]
		// 同一条记录重复应用时必须什么都不改，否则本地反复产出新变更、自激同步。
		if cur == u {
			return nil
		}
		// 降级或禁用最后一个管理员：与 Update 同一条规矩。
		if cur.Role == RoleAdmin && (u.Role != RoleAdmin || u.Disabled) && s.countAdminsLocked() <= 1 {
			return ErrLastAdmin
		}
		next[idx] = u
	} else {
		for _, cur := range next {
			if cur.ID == u.ID {
				return ErrUsernameTaken
			}
		}
		next = append(next, u)
	}
	if err := writeUsers(s.path, next, s.primaryID); err != nil {
		return err
	}
	s.users = next
	return nil
}

// RemoveUserByName 按用户名删账户（同步墓碑）。不存在即成功。
//
// 主账户拒绝删除，与 Delete 保持同一语义：墓碑指向主账户只可能是远端出错，
// 静默把存量数据归属删掉比报错危险得多。
func (s *Store) RemoveUserByName(username string) error {
	username = strings.TrimSpace(username)
	if username == "" {
		return errors.New("username_required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make([]User, 0, len(s.users))
	found := false
	for _, cur := range s.users {
		if !strings.EqualFold(cur.Username, username) {
			next = append(next, cur)
			continue
		}
		if cur.ID == s.primaryID {
			return ErrPrimaryUser
		}
		if cur.Role == RoleAdmin && s.countAdminsLocked() <= 1 {
			return ErrLastAdmin
		}
		found = true
	}
	if !found {
		return nil
	}
	if err := writeUsers(s.path, next, s.primaryID); err != nil {
		return err
	}
	s.users = next
	return nil
}
