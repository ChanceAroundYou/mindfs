package pins

import (
	"errors"
	"strings"
)

// Restore 系列是同步内核专用的「按远端值落库」入口。
//
// 为什么不复用 SetProjectPin / SetSessionPin：那两个用 time.Now() 当时间戳，
// 而同步必须把远端的**原始**时间戳写回去 —— 否则应用完一条记录，本地算出的 hash
// 和远端不一样，两边会永远认为「还没对齐」，来回推成永动机。
//
// 空值语义 = 取消置顶，与布尔置顶的墓碑一致。

// RestoreProjectPin 按给定毫秒时间戳写入或删除一条项目置顶。
func (s *Store) RestoreProjectPin(key string, atMillis int64) error {
	if s == nil {
		return errors.New("pin store not configured")
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("project scope key required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, had := s.data.Projects[key]
	if atMillis <= 0 {
		if !had {
			return nil
		}
		delete(s.data.Projects, key)
		if err := s.saveLocked(); err != nil {
			s.data.Projects[key] = previous
			return err
		}
		return nil
	}
	if !had && len(s.data.Projects) >= maxPins {
		return errors.New("too many pinned projects")
	}
	s.data.Projects[key] = atMillis
	if err := s.saveLocked(); err != nil {
		if had {
			s.data.Projects[key] = previous
		} else {
			delete(s.data.Projects, key)
		}
		return err
	}
	return nil
}

// RestoreSessionPin 按给定 RFC3339 原文写入或删除一条会话置顶。
//
// 值保持**原始字符串**而不是 time.Time：存量里有解析不了的时间戳，原样搬运才能
// 让两台机器看到同一份数据；解析失败在这里不是错误，是数据的一部分。
func (s *Store) RestoreSessionPin(key, pinnedAt string) error {
	if s == nil {
		return errors.New("pin store not configured")
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("session scope key required")
	}
	pinnedAt = strings.TrimSpace(pinnedAt)
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, had := s.data.Sessions[key]
	if pinnedAt == "" {
		if !had {
			return nil
		}
		delete(s.data.Sessions, key)
		if err := s.saveLocked(); err != nil {
			s.data.Sessions[key] = previous
			return err
		}
		return nil
	}
	if !had && len(s.data.Sessions) >= maxPins {
		return errors.New("too many pinned sessions")
	}
	s.data.Sessions[key] = pinnedAt
	if err := s.saveLocked(); err != nil {
		if had {
			s.data.Sessions[key] = previous
		} else {
			delete(s.data.Sessions, key)
		}
		return err
	}
	return nil
}
