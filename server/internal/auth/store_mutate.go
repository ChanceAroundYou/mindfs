package auth

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

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
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.users {
		if strings.EqualFold(u.Username, name) {
			return PublicUser{}, ErrUsernameTaken
		}
	}
	user := User{
		ID:           DeriveAccountID(name),
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
