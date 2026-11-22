package usecase

import (
	"strings"
	"time"

	"mindfs/server/internal/agent"
	"mindfs/server/internal/agent/claude"
	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/session"
)

func shouldReopenSessionForSetting(pool *agent.Pool, agentName, currentValue, nextValue string) bool {
	currentValue = strings.TrimSpace(currentValue)
	nextValue = strings.TrimSpace(nextValue)
	if currentValue == nextValue {
		return false
	}
	if pool == nil {
		return false
	}
	def, ok := pool.Config().GetAgent(agentName)
	if !ok {
		return false
	}
	protocol := def.Protocol
	if protocol == "" {
		protocol = agent.DefaultProtocol(agentName)
	}
	return protocol == agent.ProtocolCodexSDK || protocol == agent.ProtocolClaudeSDK
}

// sessionReopen 决定复用已存在的池会话时要不要重建 runtime。
type sessionReopen int

const (
	// sessionReuse 原地改设置（SetModel / SetMode / SetPlanMode）。
	sessionReuse sessionReopen = iota
	// sessionReopenSettings effort / fast service 变了，只能换进程。
	sessionReopenSettings
	// sessionReopenLongContext [1m] 开关翻转，窗口策略写在子进程 env 里，只能换进程。
	sessionReopenLongContext
)

// decideSessionReopen 是「复用还是重建」的唯一判定。
//
// 顺序有意义：1M 翻转优先——它决定新进程要不要带 CLAUDE_CODE_DISABLE_1M_CONTEXT，
// 即使 effort 没变也必须换进程；两者同时变时按 1M 记录日志更贴近用户动作。
func decideSessionReopen(currentModel, nextModel string, settingsNeedReopen bool) sessionReopen {
	if shouldReopenSessionForLongContext(currentModel, nextModel) {
		return sessionReopenLongContext
	}
	if settingsNeedReopen {
		return sessionReopenSettings
	}
	return sessionReuse
}

// shouldReopenSessionForLongContext 判断「1m」开关的翻转是否必须重建 runtime。
//
// 窗口策略写在 CLI 子进程的 env 里（claude.WithLongContextPolicy），进程活着就改不了；
// 原地 SetModel 只能换模型名。所以裸模型 ↔ [1m] 之间切换时要关掉池会话、按新策略重新
// resume —— 历史由转录承接，代价只是一次 CLI 重启。
func shouldReopenSessionForLongContext(currentModel, nextModel string) bool {
	return claude.Has1MSuffix(currentModel) != claude.Has1MSuffix(nextModel)
}

// defaultModelForAgent 给出「写进 agent 默认值」的模型名。
//
// 默认档就是 200k：默认值里留着 [1m] 会让新建会话一开口就是 1M，
// 而前端的 1M 勾选来自会话自身的模型名，两边对不上。1M 只是每次会话里的开关。
func defaultModelForAgent(agentName, model string) string {
	if !strings.EqualFold(strings.TrimSpace(agentName), "claude") {
		return model
	}
	return claude.Strip1MSuffix(model)
}

func resolveRuntimeModel(current *session.Session, runtime agenttypes.Session, requested string) string {
	if model := strings.TrimSpace(requested); model != "" {
		return model
	}
	if runtime != nil {
		if model := strings.TrimSpace(runtime.CurrentModel()); model != "" {
			return model
		}
	}
	if model := resolveSessionExchangeModel(current); model != "" {
		return model
	}
	if current == nil {
		return ""
	}
	return strings.TrimSpace(current.Model)
}

func currentModelForRuntime(current *session.Session, runtime agenttypes.Session) string {
	if runtime != nil {
		if model := strings.TrimSpace(runtime.CurrentModel()); model != "" {
			return model
		}
	}
	if model := resolveSessionExchangeModel(current); model != "" {
		return model
	}
	if current == nil {
		return ""
	}
	return strings.TrimSpace(current.Model)
}

func (s *Service) resolveExchangeModelDisplayName(agentName, model string, rootDir string) string {
	agentName = strings.TrimSpace(agentName)
	model = strings.TrimSpace(model)
	if agentName != "claude" || model == "" || s == nil || s.Registry == nil {
		return ""
	}
	// 同分层 env 解析：display(alias) → effective(真实上游模型)，与 session.go 发送边界同源。
	if claudeName := strings.TrimSpace(agentName); claudeName == "claude" {
		env := s.claudeEnvForExchange(rootDir)
		if effective := claude.ResolveClaudeModelArg(model, env); effective != "" {
			if claude.TierKey(effective) != claude.TierKey(model) {
				return effective // 第三方：直接展示真实上游 ID（如 deepseek-v4-flash[1M]）
			}
			// Anthropic 原生：effective 仍为 tier 名，回退到目录 Name 保持可读（如 Sonnet）
			if prober := s.Registry.GetProber(); prober != nil {
				if status, ok := prober.GetStatus(agentName); ok {
					for _, item := range status.Models {
						if strings.TrimSpace(item.ID) == model {
							if name := strings.TrimSpace(item.Name); name != "" {
								return name
							}
						}
					}
					for _, item := range status.Models {
						if claude.TierKey(item.ID) == claude.TierKey(model) {
							if name := strings.TrimSpace(item.Name); name != "" {
								return name
							}
						}
					}
				}
			}
			return effective
		}
	}
	prober := s.Registry.GetProber()
	if prober == nil {
		return ""
	}
	status, ok := prober.GetStatus(agentName)
	if !ok {
		return ""
	}
	for _, item := range status.Models {
		if strings.TrimSpace(item.ID) == model {
			return strings.TrimSpace(item.Name)
		}
	}
	return ""
}

func (s *Service) ResolveModelDisplayName(agentName, model, rootDir string) string {
	return s.resolveExchangeModelDisplayName(agentName, model, rootDir)
}

func (s *Service) claudeEnvForExchange(rootDir string) map[string]string {
	if s == nil || s.Registry == nil {
		return nil
	}
	var baseEnv map[string]string
	if pool := s.Registry.GetAgentPool(); pool != nil {
		if def, ok := pool.Config().GetAgent("claude"); ok {
			baseEnv = def.Env
		}
	}
	if strings.TrimSpace(rootDir) == "" {
		if roots := s.Registry.ListRoots(); len(roots) > 0 {
			if dir, err := roots[0].RootDir(); err == nil {
				rootDir = dir
			}
		}
	}
	return claude.ClaudeEffectiveEnv(baseEnv, rootDir)
}

func resolveRuntimeEffort(_ string, current *session.Session, requested string) string {
	if effort := strings.TrimSpace(requested); effort != "" {
		return effort
	}
	if effort := session.InferEffortFromSession(current); effort != "" {
		return effort
	}
	return ""
}

func resolveRuntimeFastService(agentName string, current *session.Session, requested string) string {
	if strings.TrimSpace(agentName) != "codex" {
		return ""
	}
	if value := strings.TrimSpace(requested); value != "" {
		return value
	}
	return inferFastServiceFromSession(current)
}

func inferFastServiceFromSession(current *session.Session) string {
	return session.InferFastServiceFromSession(current)
}

func resolveRuntimeMode(current *session.Session, requested string) string {
	if mode := strings.TrimSpace(requested); mode != "" {
		return mode
	}
	if mode := resolveSessionExchangeMode(current); mode != "" {
		return mode
	}
	return ""
}

func resolveSessionExchangeModel(current *session.Session) string {
	if current == nil || len(current.Exchanges) == 0 {
		return ""
	}
	for i := len(current.Exchanges) - 1; i >= 0; i-- {
		model := strings.TrimSpace(current.Exchanges[i].Model)
		if model != "" {
			return model
		}
	}
	return ""
}

func resolveSessionExchangeMode(current *session.Session) string {
	if current == nil || len(current.Exchanges) == 0 {
		return ""
	}
	for i := len(current.Exchanges) - 1; i >= 0; i-- {
		mode := strings.TrimSpace(current.Exchanges[i].Mode)
		if mode != "" {
			return mode
		}
	}
	return ""
}

// userExchangeRepeatTolerance：同一轮用户条目的两个写入者之间的时间窗。
// 外部转录同步会在回合进行中先导入一条（ts 带 Z、来自 transcript），回合结束时
// SendMessage 再写一条（ts 无 Z、time.Now()），实测差 21ms。落在窗内且 role+内容
// 相同 → 判为同一轮重复，不再落库。
// ponytail: 5s 窗。只挡同一次发言的双写，不吞用户隔久了的真实重发（「继续」「ok」必须保留），
// 也不按内容批量去重存量数据。
const userExchangeRepeatTolerance = 5 * time.Second

// exchangeAgentKey 是「这一行在 agent 侧的身份」（桥梁键，不是权威身份）。
//
// 老代码在这里按 role 分两把键（user 看 native_prompt、agent 看 native_turn），
// 是因为「两套 id 各写一半」的权宜：live 只写 user 行、import 只写 agent 行。
// 现在 agent 侧统一成一个字段 agent_id，role 仍要分开 —— 同一个 id 空间里
// user 与 agent 的消息 id 各自独立，混用会让「agent 的回答」把「用户的提问」
// 判成已写过。
