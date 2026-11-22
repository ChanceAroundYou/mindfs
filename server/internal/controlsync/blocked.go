package controlsync

import "strings"

// 追平锁的结构化原因代码。
//
// `blocked_reason` 是诊断串（`err.Error()` 拼出来的，例如
// `controlsync: primary instance changed: recorded=home primary=hasee2`），字面随时
// 可能改。前端要据此决定「给不给『接受新主节点』这个会丢副本的入口」，跨语言猜自然
// 语言是最脆的一种耦合 —— 所以由产生原因的一侧归一成有限集合，前端只认代码。
const (
	// BlockedCodeOriginMismatch：主节点自报的 instance_id 与本机记下的不一致，
	// 通常意味着主节点被重装或换了机器。这一档可以人工采纳新主节点。
	BlockedCodeOriginMismatch = "origin-mismatch"
	// BlockedCodeSchemaMismatch：两端同步 schema 版本不同。采纳也没用 —— 换台机器
	// 还是同一个不兼容版本，只能升级落后的一端，所以不给采纳入口。
	BlockedCodeSchemaMismatch = "schema-mismatch"
)

// BlockedCode 把持久化的阻塞原因归一成上面两个代码之一。
//
// 不认识的原因返回空串：前端据此只显示原因文本，不给任何会动数据的按钮。宁可让人
// 去命令行，也不能让界面在没看懂原因时提供一个「丢掉副本」的入口。
func BlockedCode(reason string) string {
	switch {
	case reason == "":
		return ""
	case strings.Contains(reason, ErrReplicaOriginMismatch.Error()):
		return BlockedCodeOriginMismatch
	case strings.Contains(reason, ErrReplicaSchemaMismatch.Error()):
		return BlockedCodeSchemaMismatch
	default:
		return ""
	}
}
