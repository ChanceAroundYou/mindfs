package kanban

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"
)

// newID 生成带前缀的随机 id。随机源失败时退化成时间戳，仍然保证唯一性可用。
//
// 原先住在 template_store.go 里；模板库下线（Phase 3）后搬到独立文件，
// 因为任务/阶段流水/事件都在用。
func newID(prefix string) string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return prefix + "_" + strings.ReplaceAll(time.Now().UTC().Format("20060102150405.000000000"), ".", "")
	}
	return prefix + "_" + hex.EncodeToString(b[:])
}
