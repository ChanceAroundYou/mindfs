package session

import (
	"context"
	"crypto/sha1"
	"fmt"
	"strings"

	agenttypes "mindfs/server/internal/agent/types"
)

type exchangeModelDisplayNameContextKey struct{}
type exchangeTokenUsageContextKey struct{}
type exchangeSourceContextKey struct{}
type exchangeIdentityContextKey struct{}

// ExchangeIdentity 是落在 exchange 行上的**轮次**次元数据（方案 §3.1）。
//
// 为什么用 context 而不是给 addExchangeForAgentAt 加参数：identity 是轮次级 ——
// 同一轮的用户行与助手行必须带**同一个** mindfs_id，而调用点已有 8 处。
// 与 WithExchangeSource / WithExchangeModelDisplayName 同一套机制。
type ExchangeIdentity struct {
	// MindFSID 是 MindFS 权威身份：落盘即定、永不改。由写入点统一 mint
	// (NewMindFSID, UUIDv5 确定性)，不再由客户端生成、也不再用随机 UUIDv4。
	MindFSID string
	// TurnSeq 是「打开该轮的 user 行 seq」，排序 / 人读用。
	TurnSeq int
	// AgentID 是 agent 侧的身份（可空、可后补）：claude 转录 uuid / ACP messageId。
	AgentID string
	// Partial 标记「本轮未收尾」：崩溃 / 中断丢缓冲区时靠 import 补齐（方案 D13）。
	Partial bool
}

// WithExchangeIdentity 标注这一行所属的轮次身份。零值 = 历史数据 / 无身份。
func WithExchangeIdentity(ctx context.Context, identity ExchangeIdentity) context.Context {
	if ctx == nil {
		return ctx
	}
	return context.WithValue(ctx, exchangeIdentityContextKey{}, identity)
}

// mindfsUUIDNamespace 是 MindFS 铸 id 使用的 UUIDv5 namespace。
//
// 固定常量而非随机生成：确定性 id 的全部意义在于「同一个 seed 在任何机器、
// 任何一次进程重启都算出同一个 id」。namespace 变了，历史 id 就对不上了。
// 换 namespace = 全库身份作废，只在「身份方案重做」时动它。
var mindfsUUIDNamespace = "6f2b1c4e-5a3d-4f87-9e12-3c8a7b0d5f61"

// NewMindFSID mint 一个**确定性** mindfs_id（UUIDv5，方案 D4/D5）。
//
// 为什么从 UUIDv4 换成 UUIDv5：v4 每次调用都给新 id，于是「同一条消息」在两个写入者
// （实时 live / 转录导入 import）手里各有一个 id，判重只能退回内容比对 —— 那正是
// 2026-10-07 实测 9 对 live/import 重复行的成因。UUIDv5 让 id 由内容派生：
// 同一个 seed 永远得到同一个 id，重导天然幂等，两个写入者也自动对齐。
//
// seed 优先级：agentID（agent 侧身份）优先，为空时退到 fallbackSeed（sessionKey|role|seq）。
// 两者都空返回空串 = 「这一行没有身份」，调用方自己决定怎么处理。
func NewMindFSID(agentID, fallbackSeed string) string {
	seed := strings.TrimSpace(agentID)
	if seed == "" {
		seed = strings.TrimSpace(fallbackSeed)
	}
	if seed == "" {
		return ""
	}
	// UUIDv5 = SHA1(namespaceBytes + seed)，自己算而不引 google/uuid：
	// 只需「格式正确的确定性 UUID」，crypto/sha1 在 stdlib 里。
	h := sha1.New() //nolint:gosec // UUIDv5 规范指定 SHA-1，非安全用途
	h.Write([]byte(mindfsUUIDNamespace))
	h.Write([]byte{0}) // namespace 与 name 的分隔，避免拼接歧义
	h.Write([]byte(seed))
	sum := h.Sum(nil)
	var buf [16]byte
	copy(buf[:], sum[:16])
	buf[6] = (buf[6] & 0x0f) | 0x50 // version 5
	buf[8] = (buf[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", buf[0:4], buf[4:6], buf[6:8], buf[8:10], buf[10:16])
}

// NewUserTurnMindFSID derives a user's turn identity from the allocated user
// seq. The live path passes the persisted value downstream; this helper also
// supports callers that only have the seq available.
func NewUserTurnMindFSID(sessionKey string, userSeq int) string {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return ""
	}
	return NewMindFSID("", fmt.Sprintf("%s|user|%d", sessionKey, userSeq))
}

// IsTurnIDShape 校验 mindfs_id 的形态（8-4-4-4-12，十六进制，大小写不敏感）。
//
// S2e 起 pending 的回合身份也用 mindfs_id，两处必须认同一种形状 —— 于是判据只有一份，
// 放在 MindFSID 字段旁边。ACP 的 messageId 规范（「客户端与 agent 必须使用 UUID 格式」）
// 要求的正是这一种；不认识的形状一律当「无身份」，别拿它当键去对。
func IsMindFSIDShape(raw string) bool {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if len(normalized) != 36 {
		return false
	}
	for i, r := range normalized {
		switch {
		case i == 8 || i == 13 || i == 18 || i == 23:
			if r != '-' {
				return false
			}
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f':
		default:
			return false
		}
	}
	return true
}

func exchangeIdentityFromContext(ctx context.Context) ExchangeIdentity {
	if ctx == nil {
		return ExchangeIdentity{}
	}
	identity, _ := ctx.Value(exchangeIdentityContextKey{}).(ExchangeIdentity)
	return identity
}

// WithExchangeSource 标注这一行由哪个写入者落盘（ExchangeSourceLive / ExchangeSourceImport）。
// 不传则 Source 为空 = 历史数据；来源字段供会话所有权与同步逻辑使用。
func WithExchangeSource(ctx context.Context, source string) context.Context {
	source = strings.TrimSpace(source)
	if source == "" {
		return ctx
	}
	return context.WithValue(ctx, exchangeSourceContextKey{}, source)
}

func exchangeSourceFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(exchangeSourceContextKey{}).(string)
	return strings.TrimSpace(value)
}

func WithExchangeModelDisplayName(ctx context.Context, displayName string) context.Context {
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		return ctx
	}
	return context.WithValue(ctx, exchangeModelDisplayNameContextKey{}, displayName)
}

func exchangeModelDisplayNameFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(exchangeModelDisplayNameContextKey{}).(string)
	return strings.TrimSpace(value)
}

func WithExchangeTokenUsage(ctx context.Context, usage *agenttypes.TokenUsage) context.Context {
	if ctx == nil || usage == nil {
		return ctx
	}
	copy := *usage
	return context.WithValue(ctx, exchangeTokenUsageContextKey{}, &copy)
}

func exchangeTokenUsageFromContext(ctx context.Context) *agenttypes.TokenUsage {
	if ctx == nil {
		return nil
	}
	usage, _ := ctx.Value(exchangeTokenUsageContextKey{}).(*agenttypes.TokenUsage)
	if usage == nil {
		return nil
	}
	copy := *usage
	return &copy
}
