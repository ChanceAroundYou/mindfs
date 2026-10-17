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
