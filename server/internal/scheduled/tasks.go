package scheduled

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/fs"
	"mindfs/server/internal/session"
)

const tasksMetaFile = "scheduled-agent-tasks.json"

type SessionActivityBroadcaster interface {
	BroadcastSessionMetaUpdated(rootID string, sess *session.Session)
	// BroadcastSessionCreated 让「定时任务新建的会话」也能立刻进对话列表。
	// 和看板同一条要求：meta.updated 只更新已缓存条目，不新增列表行。
	BroadcastSessionCreated(rootID string, sess *session.Session)
	// SetSessionPendingReply / BroadcastSessionUserMessageAt 返回该条目的**回合 mindfs_id**，
	// 收尾时必须原样交给 EndSessionTurn —— 否则清理按无身份走「保守清」，身份比对失效。
	SetSessionPendingReply(rootID, sessionKey, sessionTitle, mindfsID string) string
	// 刻意**不暴露**无身份的 `BroadcastSessionUserMessage`（不带 At 的那个）：它内部
	// 拿不到 mindfs_id，收尾就退化成无条件清（守卫见 pending_identity_test.go）。
	// 置 pending 只允许走能拿到 mindfs_id 的这一个入口。
	BroadcastSessionUserMessageAt(rootID, sessionKey, sessionType, sessionName, agentName, model, modelDisplayName, mode, effort, fastService string, planMode bool, content string, timestamp time.Time, userExchangeSeq int, userTurnID string) string
	BroadcastSessionUpdate(rootID, sessionKey string, update agenttypes.Event)
	BroadcastSessionError(rootID, sessionKey, message string)
	// EndSessionTurn 是**带回合身份的终结器**。定时任务路径必须走它，不得用无身份的
	// 广播（无身份会跳过身份比对，把新轮的灯误清）。
	EndSessionTurn(rootID, sessionKey, requestID string, turnID string)
	BroadcastAgentStatusChanged(agentName string)
	BroadcastScheduledTaskDone(rootID, taskID, taskName, sessionKey, summary string)
	BroadcastScheduledTaskFailed(rootID, taskID, taskName, sessionKey, message string)
}

type Task struct {
	ID                 string     `json:"id"`
	RootID             string     `json:"root_id"`
	Name               string     `json:"name"`
	Enabled            bool       `json:"enabled"`
	TaskCron           string     `json:"task_cron"`
	Agent              string     `json:"agent"`
	Model              string     `json:"model,omitempty"`
	Mode               string     `json:"mode,omitempty"`
	Effort             string     `json:"effort,omitempty"`
	FastService        string     `json:"fast_service,omitempty"`
	Prompt             string     `json:"prompt"`
	NewSessionCron     string     `json:"new_session_cron,omitempty"`
	SessionKey         string     `json:"session_key,omitempty"`
	LastRunAt          *time.Time `json:"last_run_at,omitempty"`
	LastSuccessAt      *time.Time `json:"last_success_at,omitempty"`
	LastError          string     `json:"last_error,omitempty"`
	LastSessionResetAt *time.Time `json:"last_session_reset_at,omitempty"`
	NextRunAt          *time.Time `json:"next_run_at,omitempty"`
	NextNewSessionAt   *time.Time `json:"next_new_session_at,omitempty"`
	Running            bool       `json:"running,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

type Store struct {
	root fs.RootInfo
}

func NewStore(root fs.RootInfo) *Store {
	return &Store{root: root}
}

func (s *Store) Save(tasks []Task) error {
	sort.SliceStable(tasks, func(i, j int) bool {
		return tasks[i].CreatedAt.Before(tasks[j].CreatedAt)
	})
	data, err := json.MarshalIndent(tasks, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return s.root.WriteMetaFile(tasksMetaFile, data)
}

func (s *Service) List(ctx context.Context, rootID string) ([]Task, error) {
	store, err := s.store(rootID)
	if err != nil {
		return nil, err
	}
	tasks, err := store.List()
	if err != nil {
		return nil, err
	}
	for i := range tasks {
		s.decorateTask(&tasks[i])
	}
	return tasks, nil
}
