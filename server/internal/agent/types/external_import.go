package types

import (
	"context"
	"strings"
	"time"
)

type ExternalSessionSummary struct {
	Agent          string    `json:"agent"`
	AgentSessionID string    `json:"agent_session_id"`
	Cwd            string    `json:"cwd,omitempty"`
	Title          string    `json:"title,omitempty"`
	FirstUserText  string    `json:"-"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type ListExternalSessionsInput struct {
	RootPath    string
	Agent       string
	BeforeTime  time.Time
	AfterTime   time.Time
	Limit       int
	FilterBound bool
}

type ListExternalSessionsResult struct {
	Items []ExternalSessionSummary `json:"items"`
}

type ExternalSessionVisitFunc func(ExternalSessionSummary) (bool, error)

// TranscriptNoisePrefixes 是 Claude CLI 自己写进转录、比较时应忽略的标记（小写）。
//
// 它们与 <local-command-caveat> 那批同类：不是用户输入，但 isMeta 为 None（实测），
// 所以按 isMeta 过滤拦不住，只能按内容认。标记通常是**独立的一条 user 条目**，
// 紧跟着才是真人正文，而「相邻同角色合并」会把两者粘成 "[请求标记]\n\n正文" ——
// 正文此前已被实时路径写过一次，粘连版多 31 字前缀、与干净版既不相等也不构成前缀
// 关系，判重于是放行、落成第二条。实测 2026-09-14 17:21 的会话 1789190353：
// seq 136（干净正文）与 seq 138（标记+正文）并存，后者还排在助手回复之后。
//
// 导入侧用它判定「剥完什么都不剩 = 整条是噪声」，判重侧用它剥掉粘连的标记。
// 一侧定义、两侧共用，避免名单走散。
var TranscriptNoisePrefixes = []string{
	"[request interrupted by user]",
	"[request interrupted by user for tool use]",
	"[request interrupted for tool use]",
	"[your previous response had no visible output. please continue and produce a user-visible response.]",
}

// StripTranscriptNoisePrefixes 反复剥掉开头的已知标记（可叠加出现），
// 并去掉首尾空白。剥完为空说明整条就是标记。
func StripTranscriptNoisePrefixes(s string) string {
	for {
		trimmed := strings.TrimSpace(s)
		lower := strings.ToLower(trimmed)
		matched := ""
		for _, prefix := range TranscriptNoisePrefixes {
			if strings.HasPrefix(lower, prefix) {
				matched = prefix
				break
			}
		}
		if matched == "" {
			return trimmed
		}
		s = trimmed[len(matched):]
	}
}

// IsTranscriptNoiseEntry 判断整条内容是否只是 CLI 标记（剥完为空）。
func IsTranscriptNoiseEntry(s string) bool {
	return strings.TrimSpace(s) != "" && StripTranscriptNoisePrefixes(s) == ""
}

type ExternalSessionCursor struct {
	SourcePath string
	// Offset 是上次读取时的**文件长度**，只用于「文件没变就跳过」的判据。
	Offset          int64
	ModTimeUnixNano int64
	// CommittedOffset 是**已提交给 MindFS 的字节位置**，也是下次增量读的起点。
	// 它必须落在 item 边界上：轮次还在进行时不推进，避免把半成品当成一条 exchange 落库，
	// 也避免同一轮被反复当作「新内容」追加（旧实现用会被 tool_result 持续改写的 Timestamp
	// 当判据，实测同一轮落库 6 次）。
	CommittedOffset int64
}

type ImportedExchange struct {
	Role      string
	Content   string
	Timestamp time.Time
	Aux       []ImportedExchangeAux
	// NativeID 是这条行在 agent 侧的身份：claude 转录条目的 `uuid`、ACP 的
	// `messageId`（user 行取 `UserMessageChunk.MessageId`，agent 行取
	// `AgentMessageChunk.MessageId`）。
	//
	// 为什么必须带：导入器会把**相邻同角色**条目合并成一条（`appendMerged…`），
	// 合并后的 Content 与实时路径写的行**逐字不同**，于是「内容相等 + ±5 秒」
	// 判重必然漏判，同一轮被两个写入者各落一行（实测 2026-10-07：某会话 9 对
	// live/import 重复，导入侧时间戳还是 `time.Now()` 伪造的）。有了 native id，
	// 判重就是「同一条 agent 原生消息」的铁证，与内容无关（方案 §6 / D10）。
	//
	// 空串 = 这条行没有 agent 侧身份（codex 未实证、S2c 之前的历史数据）。
	// 此时判重退回内容比对，并打一条日志 —— 那是「还在猜」的可见信号，
	// 不是可以默默接受的常态。
	NativeID string
	// SynthesizedAt 非空表示 Timestamp **不是**那一轮真实发生的时间，而是导入时
	// 的观测时刻。目前只有 ACP 会置它：ACP 的 `SessionUpdate` 协议里根本没有
	// 时间字段（SDK `SessionUpdate` 无 timestamp），重放转录得不到真实时间。
	//
	// 为什么必须标：ACP 于是**无法**用时间地板挡住重放（`TimestampFloor` 对 ACP
	// 恒不成立），它的护栏只能是 native id 判重。把「这个时间是假的」写进数据里，
	// 下游就不会拿它当权威时间去做窗口裁剪。
	SynthesizedAt time.Time
}

// ImportedExchangeAux is structured historical content attached to an imported
// exchange. Seq is assigned by the session manager when the exchange is
// persisted; importers only need to preserve its position within the assistant
// text and the normalized payload.
type ImportedExchangeAux struct {
	Line int
	// Thought = reasoning 文本。**必须用这个字段，不要用 kind=think 的 ToolCall**：
	// 前端把 `reasoning` 块渲染成灰显可折叠的思考文本，而把 think 工具卡渲染成
	// 带💭 图标的工具卡片 —— 两者外观不同。实时路径的 thought_chunk 走的正是
	// Thought，导入侧也必须走同一个字段，否则「实时看得到、导入后变样」。
	Thought  string
	ToolCall *ToolCall
	Plan     *PlanUpdate
}

type ImportedExternalSession struct {
	Agent          string
	AgentSessionID string
	Cwd            string
	Title          string
	Exchanges      []ImportedExchange
	Subagents      []ImportedSubagentSession
	Cursor         ExternalSessionCursor
}

// ImportedSubagentSession describes an externally persisted child agent session.
// ParentAgentSessionID is empty when the child belongs directly to the imported
// root session; otherwise it references another item in Subagents.
type ImportedSubagentSession struct {
	AgentSessionID       string
	ParentAgentSessionID string
	ParentToolCallID     string
	Title                string
	Model                string
	Exchanges            []ImportedExchange
}

type ExternalSessionImporter interface {
	AgentName() string
	ListExternalSessions(ctx context.Context, in ListExternalSessionsInput) (ListExternalSessionsResult, error)
	ImportExternalSession(ctx context.Context, in ImportExternalSessionInput) (ImportedExternalSession, error)
	// HasTranscriptTimestamps 报告该 agent 的转录条目是否带**真实**时间戳。
	//
	// 为什么需要：`TimestampFloor` 的意义是「只补比库里最新一条更新的尾段」，它只有在
	// 导入器能给出行真实发生时间时才成立。ACP 的 `SessionUpdate` 协议里**根本没有
	// 时间戳字段**，它的时间只能是读到那一刻的 `time.Now()` —— 拿这种时间去比地板，
	// 重放出来的旧回合永远「比库里新」，地板等于没有（实测 2026-10-07 某会话 9 对
	// live/import 重复就是这么来的）。所以没有真时间的 agent 不设地板，改由身份判重兜。
	HasTranscriptTimestamps() bool
}

type StreamingExternalSessionImporter interface {
	ExternalSessionImporter
	ScanExternalSessions(ctx context.Context, in ListExternalSessionsInput, visit ExternalSessionVisitFunc) error
}
