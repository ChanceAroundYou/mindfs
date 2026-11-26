package usecase

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"mindfs/server/internal/agent"
	"mindfs/server/internal/session"
	"mindfs/server/internal/sessionmaintenance"
)

// sessionMaintenanceEntryOf 把 MindFS 的会话元数据翻译成 harness 维护所需的身份。
func sessionMaintenanceEntryOf(ctx context.Context, manager *session.Manager, item *session.Session) (sessionmaintenance.Entry, error) {
	agent := session.InferAgentFromSession(item)
	entry := sessionmaintenance.Entry{
		SessionKey:  item.Key,
		Agent:       agent,
		RuntimePath: session.RuntimeRootPath(item),
	}
	// 推断不出 agent 的会话没有可维护的 harness，也没有绑定可查（历史 command 会话
	// 就是这种形态）。把它当错误会让整批诊断与锁清理一起失败，所以按「无 harness」返回。
	if strings.TrimSpace(agent) == "" {
		return entry, nil
	}
	binding, err := manager.FindAgentBinding(ctx, item.Key, agent)
	if err != nil {
		return sessionmaintenance.Entry{}, fmt.Errorf("find agent binding for %s: %w", item.Key, err)
	}
	if binding != nil {
		entry.AgentSessionID = binding.AgentSessionID
	}
	return entry, nil
}

func (s *Service) sessionMaintenanceEntries(ctx context.Context, rootID string) ([]sessionmaintenance.Entry, error) {
	if err := s.ensureRegistry(); err != nil {
		return nil, err
	}
	rootIDs := []string{rootID}
	if rootID == "" {
		rootIDs = make([]string, 0)
		for _, root := range s.Registry.ListRoots() {
			rootIDs = append(rootIDs, root.ID)
		}
	}
	var entries []sessionmaintenance.Entry
	for _, id := range rootIDs {
		manager, err := s.Registry.GetSessionManager(id)
		if err != nil {
			return nil, fmt.Errorf("session manager %s: %w", id, err)
		}
		items, err := manager.ListMetas(ctx)
		if err != nil {
			return nil, fmt.Errorf("list sessions %s: %w", id, err)
		}
		for _, item := range items {
			entry, err := sessionMaintenanceEntryOf(ctx, manager, item)
			if err != nil {
				// 单条会话取身份失败不该让整批作废：根级诊断与死 PID 锁清理都不依赖
				// 每个会话都能维护，记录一条跳过即可。
				log.Printf("[session-maintenance] skip session root=%s session=%s err=%v", id, item.Key, err)
				continue
			}
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

// SessionMaintenanceDiagnose 只读诊断；rootID 为空表示本机所有项目。
func (s *Service) SessionMaintenanceDiagnose(ctx context.Context, rootID string) (sessionmaintenance.Report, error) {
	entries, err := s.sessionMaintenanceEntries(ctx, rootID)
	if err != nil {
		return sessionmaintenance.Report{}, err
	}
	return sessionmaintenance.Diagnose(entries, sessionmaintenance.Options{}), nil
}

// sessionMaintenanceAdapterFor 定位会话并取 harness 适配器，错误一律上报。
func (s *Service) sessionMaintenanceAdapterFor(ctx context.Context, rootID, sessionKey string) (sessionmaintenance.Entry, sessionmaintenance.Adapter, error) {
	entries, err := s.sessionMaintenanceEntries(ctx, rootID)
	if err != nil {
		return sessionmaintenance.Entry{}, nil, err
	}
	for _, entry := range entries {
		if entry.SessionKey != sessionKey {
			continue
		}
		adapter := sessionmaintenance.ForHarness(entry.Agent)
		if adapter == nil {
			return entry, nil, fmt.Errorf("harness %q 不支持会话维护", entry.Agent)
		}
		return entry, adapter, nil
	}
	return sessionmaintenance.Entry{}, nil, fmt.Errorf("session %s not found", sessionKey)
}

// SessionMaintenancePaths 返回某会话的原生转录路径与 harness 能力。
func (s *Service) SessionMaintenancePaths(ctx context.Context, rootID, sessionKey string) (sessionmaintenance.Entry, []string, bool, error) {
	entry, adapter, err := s.sessionMaintenanceAdapterFor(ctx, rootID, sessionKey)
	if err != nil {
		return entry, nil, false, err
	}
	paths, err := adapter.TranscriptPaths(entry, sessionmaintenance.Options{})
	return entry, paths, adapter.SupportsImages(), err
}

// SessionMaintenanceCleanImages 只改原生转录里的图片块，拒绝在会话回复中执行。
func (s *Service) SessionMaintenanceCleanImages(ctx context.Context, rootID, sessionKey string) ([]sessionmaintenance.CleanResult, error) {
	entry, paths, supported, err := s.SessionMaintenancePaths(ctx, rootID, sessionKey)
	if err != nil {
		return nil, err
	}
	if !supported {
		return nil, errors.New("当前 harness 不支持安全改写图片块")
	}
	if err := s.ensureSessionIdle(entry.Agent, sessionKey); err != nil {
		return nil, err
	}
	opts := maintenanceOptionsFor(s.Registry)
	results := make([]sessionmaintenance.CleanResult, 0, len(paths))
	for _, path := range paths {
		result, err := sessionmaintenance.CleanImagesWithOptions(path, opts)
		if err != nil {
			return results, fmt.Errorf("clean images %s: %w", path, err)
		}
		results = append(results, result)
	}
	if len(results) == 0 {
		return nil, errors.New("没有找到该会话的原生转录")
	}
	return results, nil
}

// SessionMaintenanceCleanLocks 清 PID 已死的原生会话锁；与具体项目无关。
func (s *Service) SessionMaintenanceCleanLocks(ctx context.Context) (sessionmaintenance.LockResult, error) {
	entries, err := s.sessionMaintenanceEntries(ctx, "")
	if err != nil {
		return sessionmaintenance.LockResult{}, err
	}
	for _, entry := range entries {
		if adapter := sessionmaintenance.ForHarness(entry.Agent); adapter != nil {
			if dir := adapter.LocksDir(sessionmaintenance.Options{}); dir != "" {
				return sessionmaintenance.CleanLocks(dir), nil
			}
		}
	}
	return sessionmaintenance.LockResult{}, nil
}

// SessionMaintenancePurgeEmpty 只在 confirm 为真时删除空会话转录。
func (s *Service) SessionMaintenancePurgeEmpty(ctx context.Context, rootID, sessionKey string, confirm bool) ([]sessionmaintenance.CleanResult, error) {
	if !confirm {
		return nil, errors.New("purge_empty 必须显式 confirm:true")
	}
	entry, adapter, err := s.sessionMaintenanceAdapterFor(ctx, rootID, sessionKey)
	if err != nil {
		return nil, err
	}
	if !adapter.SupportsPurge() {
		return nil, fmt.Errorf("harness %q 不支持安全判定空会话，拒绝 purge", entry.Agent)
	}
	paths, err := adapter.TranscriptPaths(entry, sessionmaintenance.Options{})
	if err != nil {
		return nil, err
	}
	if err := s.ensureSessionIdle(entry.Agent, sessionKey); err != nil {
		return nil, err
	}
	results := make([]sessionmaintenance.CleanResult, 0, len(paths))
	for _, path := range paths {
		result, err := sessionmaintenance.PurgeEmpty(path)
		if err != nil {
			return results, fmt.Errorf("purge empty %s: %w", path, err)
		}
		results = append(results, result)
	}
	return results, nil
}

func (s *Service) ensureSessionIdle(agent, sessionKey string) error {
	pool := s.Registry.GetAgentPool()
	if pool == nil {
		return nil
	}
	if sessionMaintenanceBusy(pool, agent, sessionKey) {
		return errors.New("会话正在回复，不能维护")
	}
	return nil
}

// sessionMaintenanceBusy 是所有无损维护共用的活跃判据：真实 pool 键必须优先，
// 裸键只作历史兼容。巡检与单会话维护不能各写一份，否则一边放行一边拒绝。
func sessionMaintenanceBusy(pool *agent.Pool, agentName, sessionKey string) bool {
	for _, key := range sessionMaintenancePoolKeys(agentName, sessionKey) {
		if pool.HasActiveUse(key) {
			return true
		}
	}
	return false
}

// sessionMaintenancePoolKeys 给出维护守卫需要检查的 pool 键顺序。
// 第一个必须是真实 pool 键；裸键只是兼容兜底，不能反过来。
func sessionMaintenancePoolKeys(agent, sessionKey string) []string {
	keys := []string{agentPoolSessionKey(sessionKey, agent)}
	if bare := strings.TrimSpace(sessionKey); bare != "" && bare != keys[0] {
		keys = append(keys, bare)
	}
	return keys
}
