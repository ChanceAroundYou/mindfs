package usecase

import (
	"context"
	"log"
	"strings"

	"mindfs/server/internal/agent"
	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/session"
)

func agentPoolSessionKey(sessionKey, agentName string) string {
	trimmedSessionKey := strings.TrimSpace(sessionKey)
	if trimmedSessionKey == "" {
		return ""
	}
	trimmedAgent := strings.TrimSpace(agentName)
	if trimmedAgent == "" {
		return trimmedSessionKey
	}
	return strings.ToLower(trimmedAgent) + "-" + trimmedSessionKey
}

func isNonRecoverableAgentError(err error) bool {
	if err == nil {
		return false
	}
	value := strings.ToLower(strings.TrimSpace(err.Error()))
	if value == "" {
		return false
	}
	needles := []string{
		"429",
		"too many requests",
		"exceeded retry limit",
		"rate limit",
		"ratelimit",
		"usage limit",
		"usagelimitexceeded",
		"remote compaction failed",
		"compact_remote",
		"responsetoomanyfailedattempts",
	}
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func cancelRuntimeAfterNonRecoverableError(sess agenttypes.Session, pool *agent.Pool, agentName string, cause error) {
	agentName = strings.TrimSpace(agentName)
	if sess != nil {
		if err := sess.CancelCurrentTurn(); err != nil {
			log.Printf("[session] turn.cancel_after_non_recoverable.error agent=%s cause=%v err=%v", agentName, cause, err)
		}
	}
	if pool != nil && agentName != "" {
		if _, ok := pool.KillAgentProcess(agentName, 0); ok {
			log.Printf("[session] runtime.kill_after_non_recoverable.done agent=%s cause=%v", agentName, cause)
			return
		}
	}
	if sess != nil {
		if err := sess.Close(); err != nil {
			log.Printf("[session] runtime.close_after_non_recoverable.error agent=%s cause=%v err=%v", agentName, cause, err)
		}
	}
}

// persistOpenedAgentSessionBinding 在「首次打开」后立刻写回转录 id。
//
// 原先转录 id 只在回合收尾的 UpdateAgentState 里落库（session_send.go）。如果进程死在
// **第一轮中途**，绑定里始终是空的 —— 重启后既无法 resume，也无从找到转录（同步与重启
// 兜底都要求 agent_session_id），那一轮内容于是永久留在转录里、用户界面看不到。
//
// 对 codex 而言打开那一刻 thread id 就已存在，这里能直接落库；但 claude 的 CLI session id
// 要等到第一条流消息才由 SDK 带上来（setSessionID），打开时 SessionID() 仍是空的 ——
// 所以本函数对 claude 恒为 no-op，首轮被掐断时绑定仍然空着。claude 那条缺口由回合进行中
// 的 persistLearnedAgentSessionID 兜住（见下）。
//
// 只补空绑定：非空的绑定属于「resume 到已经存在的转录」，写错就是抹掉历史（那条路径由
// 下面的 rebind 逻辑负责，它只在 id 真的变了时写）。
func persistOpenedAgentSessionBinding(
	ctx context.Context,
	manager *session.Manager,
	current *session.Session,
	agentName string,
	binding *session.AgentBinding,
	sess agenttypes.Session,
) {
	if manager == nil || current == nil || binding == nil || sess == nil {
		return
	}
	if strings.TrimSpace(binding.AgentSessionID) != "" {
		return
	}
	agentSessionID := strings.TrimSpace(sess.SessionID())
	if agentSessionID == "" {
		return
	}
	if err := manager.UpdateAgentState(ctx, current, agentName, binding.AgentCtxSeq, agentSessionID); err != nil {
		log.Printf("[session/model] bind.error session=%s agent=%s agent_session_id=%s err=%v", current.Key, agentName, agentSessionID, err)
		return
	}
	log.Printf("[session/model] bind.done session=%s agent=%s agent_session_id=%s", current.Key, agentName, agentSessionID)
}

// persistLearnedAgentSessionID 在回合**进行中**反复尝试把 agent 报出的转录 id 补进空绑定。
//
// 为什么需要它：claude 的 CLI session id 要等第一条流消息才由 SDK 带上来（见
// claude/session.setSessionID），打开那一刻 sess.SessionID() 还是空的 ——
// persistOpenedAgentSessionBinding 对 claude 恒为 no-op。于是「首轮跑到一半进程被重启」
// 时绑定里一直是空的：重启后既 resume 不了，也找不到转录，那一轮内容永久留在转录里、
// 界面看不到（这正是 2026-11-17 codex 同形事故的 claude 版本，也是「重启丢记录且同步不
// 回来」的根因之一）。这里在每条流消息到达时试一次，id 一出现就落库。
//
// 返回 true 表示「已确定、无需再试」（已写入，或绑定本来就有 id）；调用方把它记成一次性
// 状态，避免每条流消息都查一次库。空 id / 写库失败返回 false，下一条消息再试。
func persistLearnedAgentSessionID(
	ctx context.Context,
	manager *session.Manager,
	current *session.Session,
	agentName string,
	sess agenttypes.Session,
) bool {
	if manager == nil || current == nil || sess == nil {
		return true
	}
	agentSessionID := strings.TrimSpace(sess.SessionID())
	if agentSessionID == "" {
		return false
	}
	binding, err := manager.FindAgentBinding(ctx, current.Key, agentName)
	if err != nil {
		log.Printf("[session/model] bind.learn.error session=%s agent=%s err=%v", current.Key, agentName, err)
		return false
	}
	// 非空绑定属于 resume/已写，交给 rebind 逻辑；这里绝不覆盖。
	if binding != nil && strings.TrimSpace(binding.AgentSessionID) != "" {
		return true
	}
	ctxSeq := 0
	if binding != nil {
		ctxSeq = binding.AgentCtxSeq
	}
	if err := manager.UpdateAgentState(ctx, current, agentName, ctxSeq, agentSessionID); err != nil {
		log.Printf("[session/model] bind.learn.error session=%s agent=%s agent_session_id=%s err=%v", current.Key, agentName, agentSessionID, err)
		return false
	}
	log.Printf("[session/model] bind.learned session=%s agent=%s agent_session_id=%s", current.Key, agentName, agentSessionID)
	return true
}

func (s *Service) ensureAgentSession(
	ctx context.Context,
	pool *agent.Pool,
	manager *session.Manager,
	current *session.Session,
	agentName string,
	model string,
	mode string,
	effort string,
	fastService string,
	rootAbs string,
	developerInstructions string,
	configDir string,
	tools []agenttypes.ToolSpec,
	toolPolicy *agenttypes.ToolPolicy,
) (agenttypes.Session, *int, error) {
	poolSessionKey := agentPoolSessionKey(current.Key, agentName)
	nextModel := resolveRuntimeModel(current, nil, model)
	nextMode := resolveRuntimeMode(current, mode)
	nextEffort := resolveRuntimeEffort(agentName, current, effort)
	nextFastService := resolveRuntimeFastService(agentName, current, fastService)
	nextPlanMode := current != nil && current.PlanMode
	var binding *session.AgentBinding
	if manager != nil {
		var err error
		binding, err = manager.FindAgentBinding(ctx, current.Key, agentName)
		if err != nil {
			return nil, nil, err
		}
	}
	currentMode := ""
	currentEffort := ""
	currentFastService := ""
	currentPlanMode := false
	if current != nil {
		currentMode = resolveSessionExchangeMode(current)
		currentEffort = session.InferEffortFromSession(current)
		currentFastService = inferFastServiceFromSession(current)
		currentPlanMode = current.PlanMode
	}
	if existing, ok := pool.Get(poolSessionKey); ok {
		currentModel := currentModelForRuntime(current, existing)
		reopen := decideSessionReopen(
			currentModel, nextModel,
			shouldReopenSessionForSetting(pool, agentName, currentEffort, nextEffort) || currentFastService != nextFastService,
		)
		if reopen == sessionReuse {
			if current != nil && currentModel != nextModel {
				log.Printf("[session/model] switch.detected session=%s agent=%s from=%q to=%q action=set_runtime_model", current.Key, agentName, currentModel, nextModel)
				if err := existing.SetModel(ctx, nextModel); err != nil {
					if prober := s.Registry.GetProber(); prober != nil {
						prober.ReportRuntimeFailure(agentName, err)
					}
					log.Printf("[session/model] switch.error session=%s agent=%s model=%q pool_session=%s err=%v", current.Key, agentName, nextModel, poolSessionKey, err)
					return nil, nil, err
				}
				log.Printf("[session/model] switch.done session=%s agent=%s model=%q pool_session=%s", current.Key, agentName, nextModel, poolSessionKey)
			}
			if current != nil && currentMode != nextMode {
				log.Printf("[session/mode] switch.detected session=%s agent=%s from=%q to=%q action=set_runtime_mode", current.Key, agentName, currentMode, nextMode)
				if err := existing.SetMode(ctx, nextMode); err != nil {
					if prober := s.Registry.GetProber(); prober != nil {
						prober.ReportRuntimeFailure(agentName, err)
					}
					log.Printf("[session/mode] switch.error session=%s agent=%s mode=%q pool_session=%s err=%v", current.Key, agentName, nextMode, poolSessionKey, err)
					return nil, nil, err
				}
				log.Printf("[session/mode] switch.done session=%s agent=%s mode=%q pool_session=%s", current.Key, agentName, nextMode, poolSessionKey)
			}
			if current != nil && currentPlanMode != nextPlanMode {
				log.Printf("[session/plan] switch.detected session=%s agent=%s from=%t to=%t action=set_plan_mode", current.Key, agentName, currentPlanMode, nextPlanMode)
				if err := existing.SetPlanMode(ctx, nextPlanMode); err != nil {
					if prober := s.Registry.GetProber(); prober != nil {
						prober.ReportRuntimeFailure(agentName, err)
					}
					log.Printf("[session/plan] switch.error session=%s agent=%s plan_mode=%t pool_session=%s err=%v", current.Key, agentName, nextPlanMode, poolSessionKey, err)
					return nil, nil, err
				}
				log.Printf("[session/plan] switch.done session=%s agent=%s plan_mode=%t pool_session=%s", current.Key, agentName, nextPlanMode, poolSessionKey)
			}
			var currentSeq *int
			if current != nil {
				last := current.AgentCtxSeq[agentName]
				currentSeq = &last
			}
			return existing, currentSeq, nil
		}
		if reopen == sessionReopenLongContext {
			log.Printf("[session/model] reopen.detected session=%s agent=%s from=%q to=%q reason=long_context action=resume_runtime_session", current.Key, agentName, currentModel, nextModel)
		} else {
			log.Printf("[session/settings] reopen.detected session=%s agent=%s effort_from=%q effort_to=%q fast_service_from=%q fast_service_to=%q action=resume_runtime_session", current.Key, agentName, currentEffort, nextEffort, currentFastService, nextFastService)
		}
		pool.Close(poolSessionKey)
	}

	openCtx := pool.Context()
	if openCtx == nil {
		openCtx = ctx
	}

	openInput := agenttypes.OpenSessionInput{
		SessionKey:            poolSessionKey,
		AgentName:             agentName,
		Model:                 nextModel,
		Mode:                  nextMode,
		Effort:                nextEffort,
		FastService:           nextFastService,
		PlanMode:              nextPlanMode,
		RootPath:              rootAbs,
		DeveloperInstructions: developerInstructions,
		ConfigDir:             configDir,
		Tools:                 tools,
		ToolPolicy:            toolPolicy,
		AgentSessionID: strings.TrimSpace(func() string {
			if binding == nil {
				return ""
			}
			return binding.AgentSessionID
		}()),
		AgentCtxSeq: func() int {
			if binding == nil {
				return 0
			}
			return binding.AgentCtxSeq
		}(),
	}
	if openInput.AgentSessionID != "" {
		log.Printf("[session/model] open session=%s agent=%s model=%q mode=%q effort=%q fast_service=%q pool_session=%s action=resume_runtime_session agent_session_id=%s agent_ctx_seq=%d", current.Key, agentName, nextModel, nextMode, nextEffort, nextFastService, poolSessionKey, openInput.AgentSessionID, openInput.AgentCtxSeq)
	} else {
		log.Printf("[session/model] open session=%s agent=%s model=%q mode=%q effort=%q fast_service=%q pool_session=%s action=open_new_runtime_session", current.Key, agentName, nextModel, nextMode, nextEffort, nextFastService, poolSessionKey)
	}
	sess, err := pool.GetOrCreate(openCtx, openInput)
	var ctxSeqOverride *int
	if err != nil {
		if openInput.AgentSessionID != "" {
			log.Printf("[session/model] resume.error session=%s agent=%s model=%q mode=%q effort=%q fast_service=%q pool_session=%s agent_session_id=%s err=%v fallback=open_new_runtime_session", current.Key, agentName, nextModel, nextMode, nextEffort, nextFastService, poolSessionKey, openInput.AgentSessionID, err)
			openInput.AgentSessionID = ""
			openInput.AgentCtxSeq = 0
			sess, err = pool.GetOrCreate(openCtx, openInput)
			if err == nil {
				zero := 0
				ctxSeqOverride = &zero
			}
		}
	}
	if err != nil {
		if prober := s.Registry.GetProber(); prober != nil {
			prober.ReportRuntimeFailure(agentName, err)
		}
		log.Printf("[session/model] open.error session=%s agent=%s model=%q mode=%q effort=%q fast_service=%q pool_session=%s err=%v", current.Key, agentName, nextModel, nextMode, nextEffort, nextFastService, poolSessionKey, err)
		return nil, nil, err
	}
	if ctxSeqOverride == nil && binding != nil && openInput.AgentSessionID != "" {
		last := binding.AgentCtxSeq
		ctxSeqOverride = &last
	}
	persistOpenedAgentSessionBinding(ctx, manager, current, agentName, binding, sess)
	// 供应商切换后 codex 会把线程 fork 到当前供应商，线程 id 随之变化。
	// 立刻写回绑定，避免用户「只打开不发送」时下次再 fork 出一份重复历史。
	if manager != nil && sess != nil && openInput.AgentSessionID != "" {
		if nextID := strings.TrimSpace(sess.SessionID()); nextID != "" && nextID != openInput.AgentSessionID {
			seq := openInput.AgentCtxSeq
			if ctxSeqOverride != nil {
				seq = *ctxSeqOverride
			}
			if err := manager.UpdateAgentState(ctx, current, agentName, seq, nextID); err != nil {
				log.Printf("[session/model] rebind.error session=%s agent=%s from=%s to=%s err=%v", current.Key, agentName, openInput.AgentSessionID, nextID, err)
			} else {
				log.Printf("[session/model] rebind.done session=%s agent=%s from=%s to=%s", current.Key, agentName, openInput.AgentSessionID, nextID)
			}
		}
	}
	log.Printf("[session/model] open.done session=%s agent=%s model=%q mode=%q effort=%q fast_service=%q pool_session=%s", current.Key, agentName, nextModel, nextMode, nextEffort, nextFastService, poolSessionKey)
	return sess, ctxSeqOverride, nil
}
