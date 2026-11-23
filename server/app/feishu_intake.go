package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"mindfs/server/internal/channels/feishu"
	"mindfs/server/internal/kanban"
	"mindfs/server/internal/nodes"
)

// FeishuIntakeDeps is the production wiring T7/T8 must supply. Catalog and
// Creator are optional overrides for tests; in production the defaults build
// a catalog from Workspaces + Nodes and a creator that writes locally or
// POSTs to the target worker's /api/tasks.
type FeishuIntakeDeps struct {
	ConfigDir string
	// NodeID is the root-level feishu_node_id, used both as the label of local
	// projects and to reject a click whose card belongs to another node.
	NodeID    string
	Nodes     *nodes.Store
	Workspace *workspaceManager
	Catalog   feishu.ProjectCatalog
	Creator   feishu.TaskCreator
	Now       func() time.Time
}

// prepareFeishuChannel is the single assembly seam used by server.Run and by
// the end-to-end tests. Keeping it separate from AttachFeishuIntake means a
// test can pin exactly what production installs, instead of silently wiring
// the intake only inside the test.
func prepareFeishuChannel(svc *feishu.Service, opts FeishuOptions, deps FeishuIntakeDeps) error {
	return AttachFeishuIntake(svc, opts, deps)
}

// AttachFeishuIntake builds and installs the inbound side of the channel. It
// is deliberately a separate helper so T7/T8 only need one call after the
// card Service, workspaces and node table exist.
func AttachFeishuIntake(svc *feishu.Service, opts FeishuOptions, deps FeishuIntakeDeps) error {
	if svc == nil {
		return errors.New("feishu intake: service is nil")
	}
	// 控制节点可以不配 feishu_node_id：空值在目录、通知与任务创建里一律按
	// 「本机」解释，不再要求每台机器都填一个节点名。
	if deps.ConfigDir == "" {
		return errors.New("feishu intake: config dir is required")
	}
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	ledger := filepath.Join(deps.ConfigDir, "feishu", "intake.json")
	store, err := feishu.NewIntakeStore(ledger, now)
	if err != nil {
		return err
	}
	catalog := deps.Catalog
	if catalog == nil {
		if deps.Nodes == nil || deps.Workspace == nil {
			return errors.New("feishu intake: catalog needs nodes and workspaces")
		}
		catalog = feishu.NewProjectCatalog(deps.Nodes, func() []feishu.ProjectRef {
			return localFeishuProjects(deps.Workspace, deps.NodeID)
		}, 0, now, nil)
	}
	creator := deps.Creator
	if creator == nil {
		if deps.Nodes == nil || deps.Workspace == nil {
			return errors.New("feishu intake: creator needs nodes and workspaces")
		}
		creator = &feishuTaskCreator{nodes: deps.Nodes, workspaces: deps.Workspace, localNode: deps.NodeID}
	}
	// 项目选择卡的点击发生在另一条 goroutine：它必须用同一个去重账本和
	// 同一个 creator，否则线上点击只会报「选择卡缺少建任务依赖」。
	svc.SetPickerTaskDeps(store, creator)
	cfg := feishu.Config{
		OwnerOpenID: opts.OwnerOpenID, NodeID: deps.NodeID, RelayToken: opts.RelayToken,
		Bindings: feishuBindings(opts.Bindings), DefaultRootID: opts.DefaultRootID,
		PickerTTL: opts.PickerTTL, PickerMax: opts.PickerMax,
	}
	limiter := feishu.NewInboundLimiter(opts.InboundRatePerMinute, opts.InboundBurst, now)
	intake := feishu.NewIntakeService(cfg, catalog, store, creator, svc.Notify, now, limiter)
	intake.SetPickerFinder(func(chatID string, _ time.Time) (feishu.Card, bool) {
		return svc.Store().FindOpenPickerByChat(chatID)
	})
	// 目标确认卡与文本命令（#N / 切换 / 新任务）需要追加与任务目录；确认卡的
	// 点击发生在另一条 goroutine，所以 Service 侧也装同一对依赖。审核分类由
	// 这里注入，feishu 包不 import kanban。
	taskClient := newFeishuTaskClient(deps.Nodes, deps.Workspace, deps.NodeID)
	taskCatalog := feishu.NewTaskCatalog(taskClient, 0, now)
	intake.SetTargetDeps(taskClient, taskCatalog)
	intake.SetTargetFinder(func(chatID string, _ time.Time) (feishu.Card, bool) {
		return svc.Store().FindOpenTargetByChat(chatID)
	})
	intake.SetReviewClassifier(feishuReviewClassifier)
	svc.SetTargetTaskDeps(taskClient, taskCatalog)
	svc.SetIntake(intake)
	return nil
}

func feishuBindings(in []FeishuBinding) []feishu.Binding {
	out := make([]feishu.Binding, 0, len(in))
	for _, b := range in {
		out = append(out, feishu.Binding{ChatID: b.ChatID, NodeID: b.NodeID, RootID: b.RootID})
	}
	return out
}

func localFeishuProjects(workspaces *workspaceManager, nodeID string) []feishu.ProjectRef {
	workspace, err := workspaces.Workspace("")
	if err != nil {
		return nil
	}
	refs := make([]feishu.ProjectRef, 0)
	for _, root := range workspace.ListRoots() {
		if root.ID == "" {
			continue
		}
		refs = append(refs, feishu.ProjectRef{NodeID: nodeID, RootID: root.ID, Name: root.Name})
	}
	return refs
}

// feishuTaskCreator resolves a node id to a URL only from nodes.json. URLs in
// a Feishu message or card are never accepted, so a forged chat message
// cannot redirect a task to an arbitrary host.
type feishuTaskCreator struct {
	nodes      *nodes.Store
	workspaces *workspaceManager
	localNode  string
	client     *http.Client
}

func (c *feishuTaskCreator) Create(ctx context.Context, nodeID, rootID, input string) (string, error) {
	if nodeID == "" || nodeID == c.localNode {
		workspace, err := c.workspaces.Workspace("")
		if err != nil {
			return "", err
		}
		detail, err := workspace.Kanban.CreateTask(ctx, kanban.CreateTaskInput{
			RootID: rootID, Input: input, Name: feishuTaskName(input),
		})
		if err != nil {
			return "", err
		}
		return detail.Task.ID, nil
	}
	var target string
	for _, node := range c.nodes.List() {
		if node.ID == nodeID {
			target = strings.TrimRight(strings.TrimSpace(node.URL), "/")
			break
		}
	}
	if target == "" {
		return "", fmt.Errorf("feishu intake: unknown node %q", nodeID)
	}
	body, err := feishu.RemoteTaskBody(rootID, input, feishuTaskName(input))
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target+"/api/tasks", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	client := c.client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("feishu intake: node %s create task: HTTP %d", nodeID, resp.StatusCode)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &created); err != nil || created.ID == "" {
		return "", fmt.Errorf("feishu intake: node %s create task: bad response", nodeID)
	}
	return created.ID, nil
}

// feishuTaskName caps the task title at 200 runes without splitting a rune.
func feishuTaskName(input string) string {
	line := ""
	for _, candidate := range strings.Split(input, "\n") {
		if line = strings.TrimSpace(candidate); line != "" {
			break
		}
	}
	if line == "" {
		return "飞书任务"
	}
	if utf8.RuneCountInString(line) <= 200 {
		return line
	}
	return string([]rune(line)[:200])
}
