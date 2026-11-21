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

// AttachFeishuIntake builds and installs the inbound side of the channel. It
// is deliberately a separate helper so T7/T8 only need one call after the
// card Service, workspaces and node table exist.
func AttachFeishuIntake(svc *feishu.Service, opts FeishuOptions, deps FeishuIntakeDeps) error {
	if svc == nil {
		return errors.New("feishu intake: service is nil")
	}
	if deps.ConfigDir == "" || deps.NodeID == "" {
		return errors.New("feishu intake: config dir and node id are required")
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
	body, err := json.Marshal(map[string]any{
		"root_id": rootID, "input": input, "name": feishuTaskName(input),
	})
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
