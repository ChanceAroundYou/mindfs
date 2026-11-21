package app

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mindfs/server/internal/channels/feishu"
	"mindfs/server/internal/kanban"
)

// FeishuOptions 是飞书通道的启动参数。Enabled=false（缺省）时整个通道不创建，
// 进程里不会多出任何 goroutine 或文件。
type FeishuOptions struct {
	Enabled     bool
	OwnerOpenID string
	LarkCLIPath string
	CardTTL     time.Duration
}

// newFeishuChannel 按配置建通道。缺 owner 时宁可不启动：点击只认 owner，
// 没有 owner 就永远没人能点，起了也只是白跑一个监听进程。
func newFeishuChannel(opts FeishuOptions, configDir string) *feishu.Service {
	if !opts.Enabled {
		return nil
	}
	owner := strings.TrimSpace(opts.OwnerOpenID)
	if owner == "" {
		log.Printf("[feishu] 已启用但没有 owner_open_id，通道不启动")
		return nil
	}
	statePath := filepath.Join(configDir, "feishu", "cards.json")
	store := feishu.NewStore(statePath)
	if err := store.Load(); err != nil {
		// 读不回来就不启动：空仓库会忘掉哪些卡片已经答过，同一张 gate 卡重启后
		// 再点一次会被当成新的点击。文件原样留着，修好再重启即可。
		log.Printf("[feishu] 读取卡片状态失败，通道不启动 path=%s err=%v", statePath, err)
		return nil
	}
	nodeID, _ := os.Hostname()
	svc := feishu.New(feishu.Config{
		OwnerOpenID: owner,
		LarkCLIPath: opts.LarkCLIPath,
		NodeID:      nodeID,
		CardTTL:     opts.CardTTL,
	}, store, &feishu.CLI{Path: opts.LarkCLIPath, Timeout: 30 * time.Second})
	log.Printf("[feishu] 通道已启用 owner=%s node=%s state=%s", shortOpenID(owner), nodeID, statePath)
	return svc
}

// shortOpenID 只留后缀：日志里不该出现完整 open_id。
func shortOpenID(id string) string {
	if len(id) <= 6 {
		return id
	}
	return "…" + id[len(id)-6:]
}

// feishuCardNotifier 把一个账户的任务卡片交给飞书通道。账户是装配时固定的 ——
// 同一个通道服务所有账户的卡片，靠 AccountID 把点击路由回正确的 AppContext。
type feishuCardNotifier struct {
	svc       *feishu.Service
	accountID string
}

func (n feishuCardNotifier) NotifyCard(ctx context.Context, notice kanban.CardNotice) error {
	if n.svc == nil {
		return nil
	}
	return n.svc.Notify(ctx, feishu.Notice{
		CardID:         notice.ID,
		Kind:           feishu.Kind(notice.Kind),
		AccountID:      n.accountID,
		RootID:         notice.RootID,
		TaskID:         notice.TaskID,
		TaskTitle:      notice.TaskTitle,
		TaskNumber:     notice.TaskNumber,
		WorkflowNodeID: notice.WorkflowNodeID,
		NodeName:       notice.NodeName,
		QuestionID:     notice.QuestionID,
		Prompt:         notice.Prompt,
		Status:         notice.Status,
		Summary:        notice.Summary,
		Options:        notice.Options,
	})
}

// ApplyFeishuAnswer 把一次点击送到它所属的账户。账户在卡片里，不在进程里 ——
// 同一张卡片重启后仍要落到原来那个账户的任务库。
func (m *workspaceManager) ApplyFeishuAnswer(ctx context.Context, answer feishu.Answer) error {
	workspace, err := m.Workspace(strings.TrimSpace(answer.AccountID))
	if err != nil {
		return err
	}
	return workspace.ApplyFeishuAnswer(ctx, answer)
}
