package kanban

import (
	"context"
	"strings"
	"time"
)

// Task Router 的只读查询面。HTTP 层只经这里访问 Router 台账，避免把
// RouterStore 的内部结构（与 db 句柄）暴露到 api 包。

// RouterStatus 是诊断快照：模式、预算、活跃 lane 与最近一次错误。
type RouterStatus struct {
	Enabled     bool          `json:"enabled"`
	Mode        RouterMode    `json:"mode"`
	Agent       string        `json:"agent"`
	Model       string        `json:"model"`
	Effort      string        `json:"effort"`
	TurnTimeout time.Duration `json:"turn_timeout"`
	IdleClose   time.Duration `json:"idle_close"`
	MaxSay      int           `json:"max_say"`
	MaxActions  int           `json:"max_actions"`
	PromptHash  string        `json:"prompt_hash"`
	ActiveLanes int           `json:"active_lanes"`
	LastError   string        `json:"last_error,omitempty"`
	LastErrorAt time.Time     `json:"last_error_at,omitempty"`
	// 配置里的时长对前端不友好，另给毫秒字段；保留 Duration 供 Go 调用方。
	TurnTimeoutMS int64 `json:"turn_timeout_ms"`
	IdleCloseMS   int64 `json:"idle_close_ms"`
}

// RouterStatusForRoot 汇总当前节点配置与运行态。mode=off 时仍返回配置，
// Enabled=false 供前端隐藏入口。
func (s *Service) RouterStatusForRoot(rootID string) RouterStatus {
	if s == nil {
		return RouterStatus{Mode: RouterModeOff}
	}
	config := s.RouterConfig()
	mode := config.ModeForRoot(rootID)
	status := RouterStatus{
		Enabled:       mode != RouterModeOff,
		Mode:          mode,
		Agent:         config.Agent,
		Model:         config.Model,
		Effort:        config.Effort,
		TurnTimeout:   config.TurnTimeout,
		IdleClose:     config.IdleClose,
		MaxSay:        config.MaxSay,
		MaxActions:    config.MaxActions,
		PromptHash:    RouterPromptHash(),
		TurnTimeoutMS: config.TurnTimeout.Milliseconds(),
		IdleCloseMS:   config.IdleClose.Milliseconds(),
	}
	s.mu.Lock()
	router := s.routers[strings.TrimSpace(rootID)]
	s.mu.Unlock()
	if router != nil {
		status.ActiveLanes = router.activeLanes()
	}
	if store, err := s.routerStore(rootID); err == nil {
		if decision, err := store.LatestProblemDecision(context.Background(), rootID); err == nil && decision != nil {
			status.LastError = decision.Error
			status.LastErrorAt = decision.CreatedAt
		}
	}
	return status
}

func (s *Service) routerStore(rootID string) (*RouterStore, error) {
	store, err := s.taskStore(rootID)
	if err != nil {
		return nil, err
	}
	return NewRouterStore(store.db), nil
}

// ListRouterDecisions 返回某任务的决策列表（时间倒序）。任务必须属于 root。
func (s *Service) ListRouterDecisions(ctx context.Context, rootID, taskID string, limit int, before time.Time) ([]*RouterDecision, error) {
	store, err := s.taskStore(rootID)
	if err != nil {
		return nil, err
	}
	if _, err := store.GetTask(ctx, taskID); err != nil {
		return nil, err
	}
	return NewRouterStore(store.db).ListDecisions(ctx, taskID, limit, before)
}

// GetRouterDecision 按 id 取决策，并校验它属于该任务。
func (s *Service) GetRouterDecision(ctx context.Context, rootID, taskID, decisionID string) (*RouterDecision, error) {
	store, err := s.taskStore(rootID)
	if err != nil {
		return nil, err
	}
	if _, err := store.GetTask(ctx, taskID); err != nil {
		return nil, err
	}
	decision, err := NewRouterStore(store.db).GetDecision(ctx, decisionID)
	if err != nil {
		return nil, err
	}
	if decision == nil || decision.TaskID != strings.TrimSpace(taskID) {
		return nil, ErrRouterDecisionNotFound
	}
	return decision, nil
}

// RouterSessionForTask 返回该任务的 Router 会话元数据；没有则 nil。
func (s *Service) RouterSessionForTask(ctx context.Context, rootID, taskID string) (*RouterSessionRecord, error) {
	store, err := s.taskStore(rootID)
	if err != nil {
		return nil, err
	}
	if _, err := store.GetTask(ctx, taskID); err != nil {
		return nil, err
	}
	return NewRouterStore(store.db).Session(ctx, RouterScopeTask, taskID)
}

// RouterSessionKeyForTask 暴露给 HTTP transcript 端点；key 由任务 id 派生，
// 不读库也能定位（库里的记录只是诊断元数据）。
func RouterSessionKeyForTask(taskID string) string {
	return "router-" + strings.TrimSpace(taskID)
}
