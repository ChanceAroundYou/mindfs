package session

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

func (m *Manager) createSessionUnsafe(session *Session) error {
	if session == nil {
		return errors.New("session required")
	}
	if _, ok := m.sessions[session.Key]; ok {
		return fmt.Errorf("session already exists: %s", session.Key)
	}
	if _, err := m.getSessionMetaUnsafe(session.Key); err == nil {
		return fmt.Errorf("session already exists: %s", session.Key)
	} else if !errors.Is(err, errSessionNotFound) {
		return err
	}
	if err := m.upsertSessionMetaUnsafe(session); err != nil {
		return err
	}
	path, err := m.exchangePath(session.Key)
	if err != nil {
		return err
	}
	_, statErr := m.root.ReadMetaFile(path)
	if statErr == nil {
		return nil
	}
	if !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	return m.root.WriteMetaFile(path, []byte{})
}

func (m *Manager) getSessionUnsafe(key string, afterSeq int) (*Session, error) {
	if afterSeq <= 0 {
		if cached, ok := m.sessions[key]; ok && cached != nil {
			if m.sessionCacheFreshUnsafe(key) {
				return cached, nil
			}
			// 文件在盘上变了（外部写入 / 手工修文件 / 同步冲突）→ 别拿陈旧缓存当真相：
			// 写路径的 seq 与判重都基于这份缓存（实测 2026-09-14 手工改文件后不重启，
			// 缓存与文件错位 → 重复 seq / 空洞）。交给 loadExchanges 走增量或全量读。
		}
	}
	loaded, err := m.loadSessionUnsafe(key, afterSeq)
	if err != nil {
		return nil, err
	}
	if afterSeq <= 0 {
		m.sessions[key] = loaded
	}
	return loaded, nil
}

// sessionCacheFreshUnsafe 报告内存缓存是否仍然与磁盘一致。
// 只有「stat 成功且 size/mtime 与读盘游标不符」才判定为陈旧；没游标（从未读过盘）或
// stat 失败一律返回 true —— 保持既有行为，不用一次偶发失败去清掉缓存。
func (m *Manager) sessionCacheFreshUnsafe(key string) bool {
	path, err := m.exchangePath(key)
	if err != nil {
		return true
	}
	cursor, ok := m.exchangeCursors[path]
	if !ok || cursor.Offset <= 0 {
		// 没有读盘基线（例如会话是刚 Create 出来的内存缓存）：文件里已经有内容，就说明
		// 缓存可能是空的或陈旧的 —— 读一次盘建立基线，代价只在这一次。
		if info, statErr := m.root.StatMetaFile(path); statErr == nil && info.Size() > 0 {
			return false
		}
		return true
	}
	info, err := m.root.StatMetaFile(path)
	if err != nil {
		return true
	}
	return info.Size() == cursor.Size && info.ModTime().UnixNano() == cursor.ModTimeNs
}

// getSessionWindowUnsafe 返回按窗口切片后的 Session 与窗口元信息。
// beforeSeq>0：取 seq<beforeSeq 的尾部 limit 条（二分定位 idx=首个 seq>=beforeSeq，窗口 all[max(0,idx-limit):idx]，hasMore=idx-limit>0）；
// latest>0：取尾部 limit 条；两者皆 0：默认尾部 limit 条（等价于 latest=limit）。
//
// 缓存已建立时在内存切片；**冷缓存下走 readExchangeWindow 只解析窗口所需的尾部记录**，
// 不再 readExchangesFull 全量解析（全量解析实测 13.8ms/1.2MB 且持 m.mu，会卡住整个 root）。
// 仍不写入 m.sessions 全量缓存，避免污染 afterSeq<=0 缓存分支。
func (m *Manager) loadSessionUnsafe(key string, afterSeq int) (*Session, error) {
	meta, err := m.getSessionMetaWithBindingsUnsafe(key)
	if err != nil {
		return nil, err
	}
	exchanges, _, err := m.loadExchanges(key, afterSeq)
	if err != nil {
		return nil, err
	}
	meta.Exchanges = exchanges
	return meta, nil
}

// getSessionMetaWithBindingsUnsafe 只加载 SQLite meta + bindings，不读 exchanges 文件。
func jsonlScanner(payload []byte) *bufio.Scanner {
	scanner := bufio.NewScanner(strings.NewReader(string(payload)))
	maxTokenSize := len(payload) + 1
	if maxTokenSize < 64*1024 {
		maxTokenSize = 64 * 1024
	}
	scanner.Buffer(make([]byte, 0, 64*1024), maxTokenSize)
	return scanner
}

func generateKey() string {
	now := time.Now().UTC().Unix()
	buf := make([]byte, 6)
	_, err := rand.Read(buf)
	if err != nil {
		return fmt.Sprintf("%d", now)
	}
	return fmt.Sprintf("%d-%s", now, hex.EncodeToString(buf))
}
