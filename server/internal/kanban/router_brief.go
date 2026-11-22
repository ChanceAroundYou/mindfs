package kanban

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Situation Brief 是 Router 一轮能看到的世界摘要。它是**快照**：shadow 模式必须
// 在旧路径分发之前取，否则比较的是「旧路径已经改变了世界之后」的状态。
//
// 信任边界：只含本账户任务数据与 workflow 元数据；不含文件内容、shell 输出、
// agent 转录、环境变量或其它任务/账户的数据。workflow 的 name/description 按
// 不可信数据处理（提示词要求把它们当数据，不当指令）。

const (
	// 2：LatestRun 增逐节点卡片（Nodes / NodeCardsTruncated）。
	routerBriefVersion         = 2
	routerBriefMaxBytes        = 24 * 1024
	routerBriefMaxActivities   = 10
	routerBriefActivityExcerpt = 500
	// routerBriefTriggerBodyLimit 单独给「触发这一轮的留言」：它是 Router 唯一的
	// 决策输入，500 字节的摘录上限会把需求砍成半句话（实测 574 字节的需求被截在
	// 第 189 个字符，Router 转头去问用户第 5 条要什么）。仍设上限，避免一条超大
	// 粘贴把 brief 预算撑破；整体 24KB 的硬上限由 capSituationBrief 兜底。
	routerBriefTriggerBodyLimit = 8000
	routerBriefMaxWorkflows     = 20
	// routerBriefNodeExcerpt 是单张节点卡片的产出摘要上限：卡片是「一眼看出这个
	// 节点上次干了什么」，要细节时 Router 用 inspect_node 取。
	routerBriefNodeExcerpt = 240
	// routerBriefNodeShortExcerpt / routerBriefNodeTrimmedLimit 是 brief 超限时的
	// 两档退让：先缩摘要，再缩条数。
	routerBriefNodeShortExcerpt = 120
	routerBriefNodeTrimmedLimit = 4
)

type SituationBrief struct {
	Version          int                   `json:"version"`
	Task             RouterBriefTask       `json:"task"`
	Trigger          RouterBriefTrigger    `json:"trigger"`
	RecentActivities []RouterBriefActivity `json:"recent_activities,omitempty"`
	ActiveExecution  *RouterBriefExecution `json:"active_execution,omitempty"`
	LatestRun        *RouterBriefRun       `json:"latest_run,omitempty"`
	Workflows        []RouterBriefWorkflow `json:"workflows,omitempty"`
	Limits           RouterBriefLimits     `json:"limits"`
	PolicyHints      []string              `json:"policy_hints,omitempty"`
	// 运行期事件回合的三段证据（Plan 2026-11-18，R1 T8）。用户留言回合保持 nil。
	Event    *RouterBriefEvent    `json:"event,omitempty"`
	Evidence *RouterBriefEvidence `json:"evidence,omitempty"`
	Budget   *RouterBriefBudget   `json:"budget,omitempty"`
	// Supervision 是主动监管的水位快照（事件生产时写入 payload，事件回合
	// 原样挂回 brief）。nil = 这个回合没有监管能力，工具也不会暴露。
	Supervision *SupervisionObservation `json:"supervision,omitempty"`
	// Completion 是上一次证据门判定的结果，只在 completion_blocked 事件回合
	// 挂上：模型据此决定补做哪一项、还是交还用户。
	Completion *RouterBriefCompletion `json:"completion,omitempty"`
}

type RouterBriefTask struct {
	ID                 string `json:"id"`
	RootID             string `json:"root_id"`
	Name               string `json:"name"`
	Status             string `json:"status"`
	CreateWorktree     bool   `json:"create_worktree"`
	WorktreePresent    bool   `json:"worktree_present"`
	MainSessionPresent bool   `json:"main_session_present"`
}

type RouterBriefTrigger struct {
	Kind        RouterTriggerKind       `json:"kind"`
	Ref         string                  `json:"ref,omitempty"`
	UserMessage *RouterBriefUserMessage `json:"user_message,omitempty"`
}

type RouterBriefUserMessage struct {
	ID             string `json:"id"`
	Body           string `json:"body"`
	WorkflowChoice string `json:"workflow_choice"`
	At             string `json:"at"`
}

type RouterBriefActivity struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Role        string `json:"role"`
	Purpose     string `json:"purpose,omitempty"`
	Status      string `json:"status,omitempty"`
	BodyExcerpt string `json:"body_excerpt,omitempty"`
	At          string `json:"at"`
}

type RouterBriefExecution struct {
	Kind         string            `json:"kind"` // workflow|direct|none
	RunID        string            `json:"run_id,omitempty"`
	ActivityID   string            `json:"activity_id,omitempty"`
	Status       string            `json:"status,omitempty"`
	StartedAt    string            `json:"started_at,omitempty"`
	CurrentNodes []RouterBriefNode `json:"current_nodes,omitempty"`
}

// RouterBriefNode 是「一个节点此刻长什么样」的唯一卡片类型：执行中的
// current_nodes 与整轮的逐节点卡片共用它，免得两处各说一遍。
type RouterBriefNode struct {
	NodeID         string   `json:"node_id"`
	Name           string   `json:"name,omitempty"`
	Kind           string   `json:"kind,omitempty"`
	Status         string   `json:"status"`
	SessionPresent bool     `json:"session_present,omitempty"`
	Attempt        int      `json:"attempt,omitempty"`
	Model          string   `json:"model,omitempty"`
	Summary        string   `json:"summary,omitempty"`
	Actions        []string `json:"actions,omitempty"`
}

type RouterBriefRun struct {
	ID               string         `json:"id"`
	Status           string         `json:"status,omitempty"`
	Version          string         `json:"version,omitempty"`
	NodeSummary      map[string]int `json:"node_summary,omitempty"`
	WaitingUserNodes []string       `json:"waiting_user_nodes,omitempty"`
	ErrorExcerpt     string         `json:"error_excerpt,omitempty"`
	// Nodes 是逐节点卡片（W2 T1）：状态、attempt、上一次产出摘要与当前可用的
	// 动作。动作表由状态唯一决定，Router 照卡片动手，不用再猜哪个动作合法。
	Nodes              []RouterBriefNode `json:"nodes,omitempty"`
	NodeCardsTruncated int               `json:"node_cards_truncated,omitempty"`
}

// briefNodeCardLimit 是单次 brief 携带的节点卡片上限；超出的只报总数，免得
// 大图把 Router 的上下文挤爆。
const briefNodeCardLimit = 16

type RouterBriefWorkflow struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Description   string   `json:"description,omitempty"`
	UseCases      []string `json:"use_cases,omitempty"`
	Tags          []string `json:"tags,omitempty"`
	LatestVersion string   `json:"latest_version,omitempty"`
}

type RouterBriefLimits struct {
	MaxSay     int `json:"max_say"`
	MaxActions int `json:"max_actions"`
	DeadlineMS int `json:"deadline_ms"`
}

func (b *SituationBrief) Hash() string {
	if b == nil {
		return ""
	}
	raw, err := json.Marshal(b)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:6])
}

// turnBrief preserves the pre-dispatch shadow snapshot. Route decisions must observe
// the current execution and the original message when the task lane reaches that turn.
func (rs *RouterService) turnBrief(ctx context.Context, taskID string, trigger RouterTrigger, config RouterConfig) (*SituationBrief, error) {
	if trigger.Mode != RouterModeRoute && trigger.Brief != nil {
		return trigger.Brief, nil
	}
	store, err := rs.service.taskStore(rs.rootID)
	if err != nil {
		return nil, fmt.Errorf("load task store: %w", err)
	}
	task, err := store.GetTask(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("load task: %w", err)
	}
	brief, err := buildSituationBrief(ctx, store, rs.service.Workflows, config, task, trigger.Kind, trigger.UserActivityID)
	if err != nil {
		return nil, fmt.Errorf("build brief: %w", err)
	}
	// 运行期事件回合补证据三段；事件语义与用户留言不同，必须单独挂载后再按
	// 事件顺序重新裁剪（buildSituationBrief 只认用户回合的裁剪顺序）。
	if trigger.Event != nil {
		if err := attachRouterEventBrief(ctx, store, brief, *trigger.Event); err != nil {
			return nil, fmt.Errorf("attach event brief: %w", err)
		}
		// 主动监管：决策时重算水位，而不是复用事件生产时写下的快照 —— 预算
		// （每节点已用重试、最近一小时动作数）必须是最新的，否则同一轮里连发的
		// 两个事件会各自看到「还有余额」。
		if config.Supervision.Normalized().Mode != RouterSupervisionOff {
			switch {
			case trigger.Event.Kind == RouterEventDirectStalled:
				// 直连事件没有 Run：水位按卡住的那条回复行重算。
				if task, taskErr := store.GetTask(ctx, trigger.Event.TaskID); taskErr == nil {
					activityID := routerEventPayloadString(trigger.Event.PayloadJSON, "activity_id")
					if reply, replyErr := NewActivityStore(store.db).Get(ctx, activityID); replyErr == nil {
						brief.Supervision = rs.service.supervisionDirectObservation(ctx, store, task, reply, time.Now().UTC())
					}
				}
			case routerEventHealthKind(trigger.Event.Kind):
				// 巡检与重启恢复事件没有 Run：现场全在载荷与回复行里，观测按它们重算
				// （turn_stalled 的水位要取实时内存值，不能信事件里的快照）。
				if task, taskErr := store.GetTask(ctx, trigger.Event.TaskID); taskErr == nil {
					brief.Supervision = rs.service.healthEventSupervision(ctx, store, task, *trigger.Event, time.Now().UTC())
				}
			case strings.TrimSpace(trigger.Event.RunID) != "":
				if run, runErr := store.WorkflowRunByID(ctx, trigger.Event.RunID); runErr == nil && run != nil {
					if task, taskErr := store.GetTask(ctx, trigger.Event.TaskID); taskErr == nil {
						brief.Supervision = rs.service.supervisionObservation(ctx, store, task, run, time.Now().UTC())
					}
				}
			}
		}
		if trigger.Event.Kind == RouterEventCompletionBlocked {
			rs.attachCompletionBrief(ctx, store, brief, *trigger.Event)
		}
		brief = capSituationBrief(brief)
	}
	return brief, nil
}

// buildSituationBrief 只读任务库与 workflow 目录，不写任何状态。
func buildSituationBrief(
	ctx context.Context,
	store *TaskStore,
	workflows *WorkflowStore,
	config RouterConfig,
	task Task,
	kind RouterTriggerKind,
	userActivityID string,
) (*SituationBrief, error) {
	if store == nil {
		return nil, errors.New("task store unavailable")
	}
	brief := &SituationBrief{
		Version: routerBriefVersion,
		Task: RouterBriefTask{
			ID:                 task.ID,
			RootID:             task.RootID,
			Name:               strings.TrimSpace(task.Name),
			Status:             task.Status,
			CreateWorktree:     task.CreateWorktree,
			WorktreePresent:    strings.TrimSpace(task.WorktreePath) != "",
			MainSessionPresent: strings.TrimSpace(task.MainSessionKey) != "",
		},
		Trigger: RouterBriefTrigger{Kind: kind, Ref: userActivityID},
		Limits: RouterBriefLimits{
			MaxSay:     config.MaxSay,
			MaxActions: config.MaxActions,
			DeadlineMS: int(config.TurnTimeout.Milliseconds()),
		},
		PolicyHints: []string{
			"有活动执行时不要新开 Run 或 direct",
			"不确定时用 router_ask_user，不要猜",
			"不能批准 HumanGate，不能 merge worktree",
			"latest_run.node_summary.unknown 表示进程重启时那一轮结果不明：节点已不在跑，可以用 router_read_session 读它的会话说清结果，再决定是否新开一轮",
			"router_read_session 默认给最后一段文本，用 offset 往前翻；只能读本任务的会话",
			ReviewCategoriesHint(),
		},
	}
	activities := NewActivityStore(store.db)
	recent, err := activities.List(ctx, task.ID)
	if err == nil {
		brief.RecentActivities = briefActivities(recent, userActivityID)
		if userActivityID != "" {
			if item, err := activities.Get(ctx, userActivityID); err == nil && item != nil {
				brief.Trigger.UserMessage = &RouterBriefUserMessage{
					ID:             item.ID,
					Body:           truncateRouterText(item.Body, routerBriefTriggerBodyLimit),
					WorkflowChoice: item.WorkflowChoice,
					At:             item.CreatedAt.UTC().Format(rfc3339),
				}
			}
		}
	}
	if run, err := store.WorkflowRunForTask(ctx, task.ID); err == nil && run != nil {
		// NodeRun 行是活动执行的另一半证据：投影只在节点终态写，重启把 running
		// 标成 unknown 时投影还是空的（#86）。一次取出，闸门与摘要共用。
		nodeRuns, _ := store.ListWorkflowNodeRuns(ctx, run.ID)
		brief.LatestRun = briefRun(ctx, store, run, task, nodeRuns)
		// 闸门按「Run 投影后的状态」判，而不是可能滞后的任务行：任务行写着
		// waiting_user、节点其实还在跑时，Router 会被告知「没有活动执行」，于是
		// 把一条本该转交的留言判成新开一轮。
		//
		// 但「活动」必须还有可变化的节点：整轮 success 的 Run 投影到 waiting_user
		// 只是为了让用户来审核，它已经没有节点会自己再动一下。若仍报成活动执行，
		// 校验层会永久拒绝 run_workflow / message_agent，Router 只能反复劝用户
		// 「先结算 Run」——而界面上根本没有这个入口（#88 现场）。
		// 暂停是操作员状态，不是「这一轮跑完了」：暂停任务照旧挡新执行。
		status := briefTaskStatus(run, task)
		if isActiveTaskStatus(status) && (status == StatusPaused || workflowRunHasOpenNodes(run, nodeRuns)) {
			brief.ActiveExecution = briefActiveExecution(ctx, store, run, task)
		}
	}
	brief.Workflows = briefWorkflows(ctx, workflows)
	return capSituationBrief(brief), nil
}
