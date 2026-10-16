// Package auth 提供 MindFS 的账户表与登录校验。
//
// 注意（设计约束，见 docs/multi-user-prd.md）：这是「多配置档」，不是安全边界。
// API 层不做鉴权，服务端按客户端声明的账户 id 分区存储，因此密码是装饰性的——
// 把请求里的 user= 换掉就能读走别人的项目与会话。不要拿它当隔离用。
package auth

import (
	"errors"
	"sync"
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
