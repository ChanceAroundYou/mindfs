// 置顶：唯一的权威存储。**按账户分文件**，两种置顶都住在这里。
//
// 为什么独立成一个包，而不是塞进 preferences：
//
//  1. **归属不同**。偏好是多账户**共享**的一份（workspace.go 的 m.shared.prefs），
//     而置顶必须**按账户分开** —— 你的置顶不该出现在别人的会话栏里。项目置顶原本
//     就住在共享偏好里，那正是它跨账户泄漏的原因。
//  2. **两种置顶要一起取**。项目置顶与会话置顶总是一起被读到（会话栏渲染），
//     分两个存储就得两次请求、两个失败模式。
//
// 为什么权威在**主节点**的这份文件里，而不在各机器的会话库：
// 会话数据面按机器分（PC 上的会话只有 PC 有），但「顶哪些」是跨设备的一致性偏好。
// 存两处就必然分叉 —— 这正是 nodes.json 当年漂移成「同一台机器两个 id」的形状。
// 会话库里的 sessions.pinned_at 列就此退役（只留列，不再读写；实测两端存量都是 0，
// 所以不需要任何数据回填）。
//
// **不做跨设备实时推送**（用户 2026-10-04 明确）：置顶变更只在切换项目等导航动作
// 时顺带刷新一次，设备之间最多差一次刷新。这是刻意的取舍 —— 真正的实时需要一条
// 主节点 → 各 worker 浏览器连接的新通道，而那些连接是浏览器直连 worker 的，
// 主节点根本碰不到。
package pins

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"mindfs/server/internal/config"
)

const pinsFileName = "session_pins.json"

// ScopeSep 是复合键分隔符，与前端 scope.ts 的 S 保持一致。
//
// 必须逐字节一致：项目置顶的键是在浏览器侧拼出来的（scopeKey），服务端只当不透明
// 字符串存。这里若改成别的，前端算出来的键就永远查不到 —— 而且是**静默**查不到，
// 置顶看起来「点了没反应」。
const ScopeSep = "::"

// maxPins 挡住「一个人把整个会话库都顶起来」这种用法。
//
// 上限存在的原因是文件按账户整份读写：没有上限的话一次误操作（比如全选 + 置顶）
// 就能把几十万条键写进去，而每个列表请求都要读它。
// 5000 条对应正常用法远远用不完（一个账户顶几百个会话已经很不寻常了）。
const maxPins = 5000

// persisted 是落盘结构。两种置顶分两个字段而不是加前缀混在一张表里：可读性
// （人工排查时能一眼看出哪些是项目、哪些是会话），以及值类型本来就不同
// （项目置顶存毫秒时间戳沿用旧格式；会话置顶存 RFC3339，前端 Date.parse 只认后者）。
type persisted struct {
	Projects map[string]int64  `json:"projects,omitempty"`
	Sessions map[string]string `json:"sessions,omitempty"`
}

// Store 是一个账户的置顶表。
type Store struct {
	mu   sync.RWMutex
	path string
	data persisted
	// needSeed：文件此前不存在或读不出内容，可以从共享偏好回填一次。
	// 文件一旦能被解析就永不再回填。
	needSeed bool
}

// NewStore 建在缺省配置目录（主账户：<config-dir>/session_pins.json）。
func NewStore() (*Store, error) {
	configDir, err := config.MindFSConfigDir()
	if err != nil {
		return nil, err
	}
	return NewStoreAt(configDir)
}

// NewStoreAt 把置顶表放在指定目录（多账户：每个账户一套）。
//
// 目录即归属 —— 与 nodes.Store / webpush.StoreAt 同一形状，不另设 account id 字段：
// 账户表的 id 是每台机器自己生成的（users.json 各机独立），拿它当文件名会让同一个
// 账户在两台机器上落到不同路径，正是这次要消灭的那种漂移。
func NewStoreAt(configDir string) (*Store, error) {
	store := &Store{
		path: filepath.Join(configDir, pinsFileName),
		data: persisted{Projects: map[string]int64{}, Sessions: map[string]string{}},
	}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// 文件不存在 = 这个账户还没在新的存储里置过顶，允许回填一次。
			s.needSeed = true
			return nil
		}
		return err
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		s.needSeed = true
		return nil
	}
	var raw persisted
	// 坏 JSON **降级成空表，不让服务起不来** —— 与 nodes/preferences 的既有做法相反，
	// 刻意如此：置顶是纯装饰性的排序数据，为它拒绝启动不划算（用户在开机时就看到
	// mindfs 挂了，却只因为一个排序偏好文件坏了）。丢掉的置顶用户重新点一次就有了。
	// 真丢数据也不怕：文件坏了会留在盘上（这里只读不写），可以手工捞回来。
	if err := json.Unmarshal(b, &raw); err != nil {
		s.needSeed = true
		return nil
	}
	if raw.Projects == nil {
		raw.Projects = map[string]int64{}
	}
	if raw.Sessions == nil {
		raw.Sessions = map[string]string{}
	}
	s.data = raw
	return nil
}

// NeedsLegacySeed 报「这个账户的项目置顶还没从共享偏好回填过」。
//
// 调用方（workspace.go）拿它当条件去读共享偏好的 session_project_pins 字段，
// 一次性搬过来。刻意做成显式两步而不是让 NewStoreAt 直接收参数：迁移源在
// preferences 包里，store 不该知道它的存在 —— 那样 pins 就再也独立不回去了。
func (s *Store) NeedsLegacySeed() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.needSeed
}

// ProjectPins 返回项目置顶（键 = scopeKey，值 = 毫秒时间戳）。
//
// 返回拷贝：调用方改它不影响存储。
func (s *Store) ProjectPins() map[string]int64 {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]int64, len(s.data.Projects))
	for k, v := range s.data.Projects {
		out[k] = v
	}
	return out
}

// SetProjectPin 置顶/取消一个项目，返回落库后的时间戳与最终态。
//
// 已置顶时再点一次**不刷新时间戳**：反复点「置顶」不该让这一条在列表里跳来跳去。
// 只有取消后再置顶才拿到新时间 —— 那是有意义的重新置顶。与会话置顶同规则。
func (s *Store) SetProjectPin(key string, pinned bool) (int64, bool, error) {
	if s == nil {
		return 0, false, errors.New("pin store not configured")
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return 0, false, errors.New("project scope key required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !pinned {
		previous, ok := s.data.Projects[key]
		if !ok {
			return 0, false, nil // 已是目标态：不写库
		}
		delete(s.data.Projects, key)
		if err := s.saveLocked(); err != nil {
			// 内存已删但落库失败 —— 回滚**原值**（回滚成 now 会让排序位置整个乱掉）。
			// 不回滚则更糟：下次读盘又读回来，表现成「取消置顶后刷新又回来了」。
			s.data.Projects[key] = previous
			return 0, false, err
		}
		return 0, false, nil
	}
	if ts, ok := s.data.Projects[key]; ok {
		return ts, true, nil
	}
	if len(s.data.Projects) >= maxPins {
		return 0, false, errors.New("too many pinned projects")
	}
	ts := time.Now().UnixMilli()
	s.data.Projects[key] = ts
	if err := s.saveLocked(); err != nil {
		delete(s.data.Projects, key)
		return 0, false, err
	}
	return ts, true, nil
}

// SessionKeys 返回已置顶会话的复合键集合。
//
// 排序只为可重现：map 迭代序随机，同一份数据两次启动返回不同顺序会让
// 「对账两次输出是否一致」这类检查没法做。
func (s *Store) SessionKeys() []string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.data.Sessions))
	for k := range s.data.Sessions {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// SessionPinnedAt 返回某键的置顶时间；没置顶返回 false。
func (s *Store) SessionPinnedAt(key string) (time.Time, bool) {
	if s == nil {
		return time.Time{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	raw, ok := s.data.Sessions[strings.TrimSpace(key)]
	if !ok {
		return time.Time{}, false
	}
	ts, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(raw))
	if err != nil {
		return time.Time{}, false
	}
	return ts.UTC(), true
}

// SessionPinnedAtAll 返回整张会话置顶表（键 = rootID::sessionKey，值 = RFC3339 字符串）。
//
// 单独一个 getter 而不是让调用方遍历 SessionKeys + SessionPinnedAt：后者要拿两次锁、
// 拼两次字符串，而键与值本来就是同一个 map 里的东西。
//
// 值保持**原始字符串**而不是 time.Time：这是要原样发给前端的负载，
// 转成 time.Time 再格式化回去只会多一次解析失败的机会（存量里有解析不了的
// 时间戳，SessionPinnedAt 对那种键返回零值 + false，那正是它该被丢掉的场合）。
func (s *Store) SessionPinnedAtAll() map[string]string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]string, len(s.data.Sessions))
	for k, v := range s.data.Sessions {
		out[k] = v
	}
	return out
}

// SetSessionPin 置顶/取消一个会话，返回落库后的最终态与时间。
func (s *Store) SetSessionPin(key string, pinned bool) (time.Time, bool, error) {
	if s == nil {
		return time.Time{}, false, errors.New("pin store not configured")
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return time.Time{}, false, errors.New("session scope key required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !pinned {
		previous, ok := s.data.Sessions[key]
		if !ok {
			return time.Time{}, false, nil
		}
		delete(s.data.Sessions, key)
		if err := s.saveLocked(); err != nil {
			s.data.Sessions[key] = previous
			return time.Time{}, false, err
		}
		return time.Time{}, false, nil
	}
	if raw, ok := s.data.Sessions[key]; ok {
		if ts, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(raw)); err == nil {
			return ts.UTC(), true, nil
		}
		// 存量里存了个解析不了的时间戳：当作没置顶，重写一遍顺手修好它。
	}
	if len(s.data.Sessions) >= maxPins {
		return time.Time{}, false, errors.New("too many pinned sessions")
	}
	ts := time.Now().UTC()
	s.data.Sessions[key] = ts.Format(time.RFC3339Nano)
	if err := s.saveLocked(); err != nil {
		delete(s.data.Sessions, key)
		return time.Time{}, false, err
	}
	return ts, true, nil
}

// SeedProjects 从共享偏好回填项目置顶，**只增不减**。
//
// 只在 NeedsLegacySeed() 为真时被调用一次。语义是并集：已有的键不动、保留原时间戳。
//
// 「只回填一次」不需要额外的标记字段：文件能不能被解析出来本身就是标记。
// 第一次回填就会把文件写出来，此后每次启动都读得到它，于是 needSeed 为假。
// 反过来，用户取消掉的置顶也就回不来了 —— 这正是要的（共享偏好里还留着旧值，
// 每次都搬一次会让取消的顶在每次重启后自己复活）。曾经为此加过一个
// legacy_seeded 字段，变异测试发现它从没被读过：判断已经由文件存在性承担了。
func (s *Store) SeedProjects(pins map[string]int64) error {
	if s == nil {
		return errors.New("pin store not configured")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, ts := range pins {
		key := strings.TrimSpace(k)
		if key == "" || ts <= 0 {
			continue
		}
		if _, ok := s.data.Projects[key]; ok {
			continue
		}
		if len(s.data.Projects) >= maxPins {
			break
		}
		s.data.Projects[key] = ts
	}
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.data, "", "  ")
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

// SessionScopeKey 按前端 scope.ts 的口径拼会话复合键。
//
// 服务端只在**测试**与回填里用它；线上来的键是浏览器拼好的，原样存。
// 留在包内是为了让回填脚本和测试共用同一份实现 —— 这里的分叉会表现为
// 「回填完了但前端看不到置顶」，查起来很费劲。
func SessionScopeKey(nodeID, rootID, sessionKey string) string {
	n := strings.TrimSpace(nodeID)
	r := strings.TrimSpace(rootID)
	k := strings.TrimSpace(sessionKey)
	if n != "" {
		return n + ScopeSep + r + ScopeSep + k
	}
	return r + ScopeSep + k
}
