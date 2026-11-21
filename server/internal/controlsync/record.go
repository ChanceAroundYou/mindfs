// Package controlsync 是多节点控制面同步的底座：记录模型、变更索引、域适配器
// 与模式状态机。它**不**拥有任何业务数据 —— users.json / preferences.json /
// nodes.json / prompts.json / session_pins.json / workflows.json 仍是唯一权威，
// 这里的记录只保存「这条数据现在长什么样」的规范化快照，用于比对与合并。
//
// 边界：
//   - 只同步控制面；项目 / 会话 / 任务 / 文件 / git / agent 池一律不出本机。
//   - 不提供自动冲突裁决：两侧都改同一记录时只落冲突，等人决定。
//   - 本包不依赖 server/app 或 api，避免把 HTTP 语义带进同步内核。
package controlsync

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Record 是一条控制面数据在某个时刻的规范化快照。
//
// Hash 只覆盖 Payload（不含 Deleted / Rev / UpdatedAt / Origin）：
// 删除 tombstone 保留最后一次内容的 hash，接收方才能判断「我删的」和
// 「远端改再删的」是不是同一件事。Rev 由主节点单调分配，边缘离线期间不猜。
type Record struct {
	Domain    string          `json:"domain"`
	Key       string          `json:"key"`
	Hash      string          `json:"hash"`
	Rev       int64           `json:"rev"`
	UpdatedAt time.Time       `json:"updated_at"`
	Origin    string          `json:"origin"`
	Deleted   bool            `json:"deleted"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

// CanonicalJSON 返回与键序、空白无关的 JSON 字节。
//
// 用 UseNumber 保留数字字面量：走 float64 会把 2^53 以上的整数舍入，
// 让同一份数据在不同机器上算出不同 hash。
func CanonicalJSON(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var decoded any
	if err := dec.Decode(&decoded); err != nil {
		return nil, err
	}
	return json.Marshal(decoded)
}

// HashRecord 返回 paylaod 的规范化 hash（sha256:hex）。
func HashRecord(rec Record) (string, error) {
	canonical, err := CanonicalJSON(rec.Payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

var errRecordKeyControl = errors.New("record key contains control character")

// ValidateRecordKey 校验 key：非空、≤256 字节、无控制字符。
// key 是跨机器身份，必须稳定 —— 用用户名、URL、文本 hash 这类派生值，
// 不要用随机 id 或自增序号。
func ValidateRecordKey(key string) error {
	if strings.TrimSpace(key) == "" {
		return errors.New("record key required")
	}
	if len(key) > 256 {
		return fmt.Errorf("record key too long: %d bytes (max 256)", len(key))
	}
	for _, r := range key {
		if r < 0x20 || r == 0x7f {
			return errRecordKeyControl
		}
	}
	return nil
}

var domainPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

// ValidateRecord 校验域与 key。域用固定小写短名（users / preferences / nodes /
// prompts / pins / workflows），拼错时要能立刻报错，而不是静默落一条没人读的记录。
func ValidateRecord(rec Record) error {
	if !domainPattern.MatchString(rec.Domain) {
		return fmt.Errorf("invalid record domain %q", rec.Domain)
	}
	return ValidateRecordKey(rec.Key)
}
