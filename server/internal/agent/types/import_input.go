package types

import (
	"time"
)

type ImportExternalSessionInput struct {
	RootPath       string
	Agent          string
	AgentSessionID string
	AfterTimestamp time.Time
	Cursor         ExternalSessionCursor
	// TimestampFloor 非零时，无论有没有字节游标，都只接受「时间戳晚于它」的条目。
	// 用途：MindFS 自己在驱动的会话（live-owned）平时不让导入器写，只在重启后兜底补齐一次；
	// 那一次的游标是**冻结已久**的，直接按游标读会把已经落过库的旧回合整段重导一遍
	// （实测 2026-09-16：BP 会话被重导 09-14 的内容，与实时路径写的行并排显示成重复）。
	// 兜底补齐真正要的只是「比库里最新一条还新」的尾轮，所以给它一道时间地板。
	TimestampFloor time.Time
	// ForceRead 为 true 时禁用「源文件未变即整个跳过导入」的快速路径，强制重新读取。
	// 有子代理会话的会话必须置 true：子代理转录可能在 root 文件不变的情况下增长，
	// 跳过会漏掉它们。注意它与 Cursor 是两件事——Cursor 提供增量起点（byte offset），
	// 二者不可混淆（曾因此让增量读失效、每次全量解析数十 MB 转录）。
	ForceRead bool
}
