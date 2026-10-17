package pins

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

)


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
