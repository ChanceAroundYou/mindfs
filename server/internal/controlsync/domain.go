package controlsync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// 域名常量与 spec §5 的域清单一一对应。域名字符串会进数据库、日志与冲突卡，
// 不要在这里加别名（`users` 和 `account` 各写一半，合并时就再也对不上了）。
const (
	DomainUsers       = "users"
	DomainPreferences = "preferences"
	DomainNodes       = "nodes"
	DomainPrompts     = "prompts"
	DomainPins        = "pins"
	DomainWorkflows   = "workflows"
)

// Domain 把一个控制面存储翻译成记录流。
//
// 三件事必须由实现保证：
//   - Snapshot 只读，且对同一份存储反复调用得到**逐字节相同**的 payload ——
//     否则每次比对都会以为数据变了，同步通道会被自己刷爆。
//   - Apply 一律走该域既有的 store API（不直接写文件），保证内存缓存与磁盘一致。
//   - Apply 是幂等的：同一份记录应用两次，存储内容不变。
type Domain interface {
	Name() string
	Snapshot(ctx context.Context) ([]Record, error)
	Apply(ctx context.Context, rec Record) error
}

// Registry 按域名索引适配器。缺域就是没接同步，不是「空域」—— 取值失败要显式报错。
type Registry struct {
	byName map[string]Domain
}

// NewRegistry 组装适配器表。域名必须是 spec 里的合法短名且不能重复。
func NewRegistry(domains ...Domain) (*Registry, error) {
	reg := &Registry{byName: make(map[string]Domain, len(domains))}
	for _, d := range domains {
		if d == nil {
			return nil, errors.New("controlsync: nil domain")
		}
		name := d.Name()
		if !domainPattern.MatchString(name) {
			return nil, fmt.Errorf("controlsync: invalid domain name %q", name)
		}
		if _, dup := reg.byName[name]; dup {
			return nil, fmt.Errorf("controlsync: duplicate domain %q", name)
		}
		reg.byName[name] = d
	}
	return reg, nil
}

// Get 取适配器。未注册的域返回错误，让调用方决定是拒绝整批还是跳过。
func (r *Registry) Get(name string) (Domain, error) {
	if r == nil {
		return nil, errors.New("controlsync: nil registry")
	}
	d, ok := r.byName[name]
	if !ok {
		return nil, fmt.Errorf("controlsync: unknown domain %q", name)
	}
	return d, nil
}

// Names 返回已注册域名（排序，便于日志与测试断言）。
func (r *Registry) Names() []string {
	if r == nil {
		return nil
	}
	out := make([]string, 0, len(r.byName))
	for name := range r.byName {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// newRecord 用 payload 生成一条记录（hash 立刻算好）。Payload 走 CanonicalJSON，
// 于是「同一对象、不同键序」在两台机器上落成同一份字节、同一个 hash。
func newRecord(domain, key string, payload any) (Record, error) {
	raw, err := CanonicalJSON(payload)
	if err != nil {
		return Record{}, fmt.Errorf("controlsync: encode %s/%s: %w", domain, key, err)
	}
	rec := Record{Domain: domain, Key: key, Payload: raw}
	hash, err := HashRecord(rec)
	if err != nil {
		return Record{}, err
	}
	rec.Hash = hash
	return rec, nil
}

// decodePayload 解出远端记录的内容。墓碑没有内容，调用方必须先判 Deleted。
func decodePayload[T any](rec Record) (T, error) {
	var out T
	if rec.Deleted {
		return out, errors.New("controlsync: tombstone has no payload")
	}
	if len(rec.Payload) == 0 {
		return out, fmt.Errorf("controlsync: empty payload for %s/%s", rec.Domain, rec.Key)
	}
	dec := json.NewDecoder(strings.NewReader(string(rec.Payload)))
	dec.UseNumber()
	if err := dec.Decode(&out); err != nil {
		return out, fmt.Errorf("controlsync: decode %s/%s: %w", rec.Domain, rec.Key, err)
	}
	return out, nil
}

// mainAccountKey 是主账户在记录 key 与 payload 里的名字。主账户没有私有 id，
// 但 key 里必须有个名字，否则 `main/x` 与 `u_abc/x` 会拼出同一个字符串。
const mainAccountKey = "main"

var accountKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// AccountDir 是一个账户的私有配置目录。
type AccountDir struct {
	Key string // 记录 key 与 payload 里的账户名；主账户是 "main"
	Dir string
}

// AccountDirs 枚举本机每个账户的配置目录：主账户就是 configDir 本身，其余账户住在
// <configDir>/users/<id>/（见 server/app/workspace_build.go）。
//
// prompts 与 pins 都是**每账户一份**的文件：域名里的 key 必须带上账户，
// 否则两台机器上同名账户的置顶会互相覆盖 —— 这和「项目身份必须带节点」是同一条教训。
func AccountDirs(configDir string) ([]AccountDir, error) {
	configDir = strings.TrimSpace(configDir)
	if configDir == "" {
		return nil, errors.New("controlsync: config dir required")
	}
	dirs := []AccountDir{{Key: mainAccountKey, Dir: configDir}}
	entries, err := os.ReadDir(filepath.Join(configDir, "users"))
	if err != nil {
		if os.IsNotExist(err) {
			return dirs, nil
		}
		return nil, fmt.Errorf("controlsync: list account dirs: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		dirs = append(dirs, AccountDir{Key: name, Dir: filepath.Join(configDir, "users", name)})
	}
	return dirs, nil
}

// AccountDirFor 把记录里的账户名映射回目录。账户名来自网络，**必须**先过白名单，
// 否则 `../../` 能把文件写到配置目录外面。
func AccountDirFor(configDir, key string) (string, error) {
	if key == mainAccountKey {
		return configDir, nil
	}
	if !accountKeyPattern.MatchString(key) {
		return "", fmt.Errorf("controlsync: invalid account key %q", key)
	}
	return filepath.Join(configDir, "users", key), nil
}
