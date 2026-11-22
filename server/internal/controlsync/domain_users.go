package controlsync

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"mindfs/server/internal/auth"
)

// usersPrimaryKey 是「主账户是谁」这条记录的 key。
//
// 主账户标记单独一条，不塞进账户载荷：它是**存量数据归属**这个全局属性，
// 挂在某条账户记录上会让「改主账户」与「改这个账户」变成同一条记录，冲突卡分不清。
// 用户名不允许以 @ 开头，因此这个 key 不会与账户撞名。
const usersPrimaryKey = "@primary"

// errUsersKeyMismatch 表示记录 key 与载荷里的用户名对不上。
// 两台机器对同一个账户必须用同一个 key，否则同步会把同一个人当成两个人。
var errUsersKeyMismatch = errors.New("controlsync: user record key does not match payload username")

type primaryUserPayload struct {
	ID string `json:"id"`
}

// userStore 是账户表需要的能力面。
type userStore interface {
	ExportUsers() ([]auth.User, string)
	ApplyUser(u auth.User) error
	RemoveUserByName(username string) error
	SetPrimary(id string) error
}

// UsersDomain 同步账户表。载荷里带 bcrypt hash —— 离线要能直接登录，
// 复制的是「账户本身」，不是「重新设一次密码」。
type UsersDomain struct {
	store userStore
}

func NewUsersDomain(store *auth.Store) *UsersDomain {
	if store == nil {
		return &UsersDomain{}
	}
	return &UsersDomain{store: store}
}

func (d *UsersDomain) Name() string { return DomainUsers }

func (d *UsersDomain) Snapshot(ctx context.Context) ([]Record, error) {
	if d == nil || d.store == nil {
		return nil, errors.New("controlsync: users store required")
	}
	users, primaryID := d.store.ExportUsers()
	out := make([]Record, 0, len(users)+1)
	for _, u := range users {
		key := strings.ToLower(strings.TrimSpace(u.Username))
		if key == "" || strings.HasPrefix(key, "@") {
			return nil, fmt.Errorf("controlsync: unusable username %q", u.Username)
		}
		rec, err := newRecord(DomainUsers, key, u)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	if primaryID != "" {
		rec, err := newRecord(DomainUsers, usersPrimaryKey, primaryUserPayload{ID: primaryID})
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	// 主账户标记排最后：应用方必须先有账户，才能把主账户指过去。
	sort.Slice(out, func(i, j int) bool {
		iPrimary, jPrimary := out[i].Key == usersPrimaryKey, out[j].Key == usersPrimaryKey
		if iPrimary != jPrimary {
			return jPrimary
		}
		return out[i].Key < out[j].Key
	})
	return out, nil
}

func (d *UsersDomain) Apply(ctx context.Context, rec Record) error {
	if d == nil || d.store == nil {
		return errors.New("controlsync: users store required")
	}
	if rec.Domain != DomainUsers {
		return fmt.Errorf("controlsync: users domain got %q record", rec.Domain)
	}
	key := strings.TrimSpace(rec.Key)
	if key == "" {
		return errors.New("controlsync: empty user key")
	}
	if key == usersPrimaryKey {
		if rec.Deleted {
			return errors.New("controlsync: primary account marker cannot be deleted")
		}
		marker, err := decodePayload[primaryUserPayload](rec)
		if err != nil {
			return err
		}
		if strings.TrimSpace(marker.ID) == "" {
			return errors.New("controlsync: empty primary account id")
		}
		return d.store.SetPrimary(marker.ID)
	}
	if strings.HasPrefix(key, "@") {
		return fmt.Errorf("controlsync: reserved user key %q", key)
	}
	if rec.Deleted {
		return d.store.RemoveUserByName(key)
	}
	u, err := decodePayload[auth.User](rec)
	if err != nil {
		return err
	}
	if strings.ToLower(strings.TrimSpace(u.Username)) != key {
		return fmt.Errorf("%w: key %q, payload %q", errUsersKeyMismatch, key, u.Username)
	}
	return d.store.ApplyUser(u)
}
