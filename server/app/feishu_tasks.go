package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"mindfs/server/internal/api"
	"mindfs/server/internal/channels/feishu"
	"mindfs/server/internal/kanban"
	"mindfs/server/internal/nodes"
)

// feishuTaskClient is the app-layer data-plane bridge for explicit Feishu
// target commands: append to an existing task and look tasks up by number.
// Local projects go straight to the workspace store; remote nodes use the same
// node-table URL resolution as feishuTaskCreator. A URL from a chat message is
// never trusted, so a forged text cannot redirect a write to another host.
type feishuTaskClient struct {
	nodes      *nodes.Store
	workspaces *workspaceManager
	localNode  string
	client     *http.Client
}

func newFeishuTaskClient(nodesStore *nodes.Store, workspaces *workspaceManager, localNode string) *feishuTaskClient {
	return &feishuTaskClient{nodes: nodesStore, workspaces: workspaces, localNode: localNode}
}

// Append adds one user message to an existing task. A missing task (404 or a
// "no rows" error) is wrapped with feishu.ErrTargetStale so the channel can
// forget the dead active target; everything else is returned as-is (transient).
func (c *feishuTaskClient) Append(ctx context.Context, target feishu.TargetRef, input string) error {
	if strings.TrimSpace(target.TaskID) == "" {
		return errors.New("feishu intake: append needs a task id")
	}
	if target.NodeID == "" || target.NodeID == c.localNode {
		return c.appendLocal(ctx, target, input)
	}
	base, err := c.remoteBase(target.NodeID)
	if err != nil {
		return err
	}
	blank := struct {
		RootID         string `json:"root_id"`
		Body           string `json:"body"`
		WorkflowChoice string `json:"workflow_choice"`
	}{RootID: target.RootID, Body: input, WorkflowChoice: "empty"}
	body, err := json.Marshal(blank)
	if err != nil {
		return err
	}
	endpoint := base + "/api/tasks/" + url.PathEscape(target.TaskID) + "/activities"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusNotFound || taskGoneBody(raw) {
		return fmt.Errorf("%w: task %s on %s", feishu.ErrTargetStale, target.TaskID, target.NodeID)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("feishu intake: node %s append to %s: HTTP %d", target.NodeID, target.TaskID, resp.StatusCode)
	}
	return nil
}

func (c *feishuTaskClient) appendLocal(ctx context.Context, target feishu.TargetRef, input string) error {
	workspace, err := c.local()
	if err != nil {
		return err
	}
	_, _, err = workspace.Kanban.CreateUserActivity(ctx, target.RootID, target.TaskID, input, kanban.CommentWorkflowChoiceEmpty, nil)
	if err != nil {
		if taskGone(err) {
			return fmt.Errorf("%w: %v", feishu.ErrTargetStale, err)
		}
		return err
	}
	return nil
}

// List returns a project's tasks for the confirmation card / exact `#N` lookup.
// For a numeric keyword a remote node is asked for that exact number; a
// non-numeric keyword filters the fetched list by case-insensitive name match.
func (c *feishuTaskClient) List(ctx context.Context, nodeID, rootID, keyword string) ([]feishu.TaskRef, error) {
	if strings.TrimSpace(rootID) == "" {
		return nil, errors.New("feishu task finder: project required")
	}
	if nodeID == "" || nodeID == c.localNode {
		return c.listLocal(ctx, nodeID, rootID, keyword)
	}
	return c.listRemote(ctx, nodeID, rootID, keyword)
}

func (c *feishuTaskClient) listLocal(ctx context.Context, nodeID, rootID, keyword string) ([]feishu.TaskRef, error) {
	workspace, err := c.local()
	if err != nil {
		return nil, err
	}
	opts := kanban.ListTasksOptions{Limit: 50}
	if n, ok := feishuTaskNumber(keyword); ok {
		opts.TaskNumber = n
	}
	tasks, err := workspace.Kanban.ListTasks(ctx, rootID, opts)
	if err != nil {
		return nil, err
	}
	needle := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(keyword), "#"))
	refs := make([]feishu.TaskRef, 0, len(tasks))
	for _, task := range tasks {
		if _, exact := feishuTaskNumber(keyword); !exact && needle != "" &&
			!strings.Contains(strings.ToLower(task.Name), needle) {
			continue
		}
		refs = append(refs, feishu.TaskRef{
			NodeID: nodeID, RootID: task.RootID, TaskID: task.ID,
			TaskNumber: task.TaskNumber, Title: task.Name, Status: task.Status,
		})
	}
	return capTaskRefs(refs, feishuTaskRefLimit), nil
}

func (c *feishuTaskClient) listRemote(ctx context.Context, nodeID, rootID, keyword string) ([]feishu.TaskRef, error) {
	base, err := c.remoteBase(nodeID)
	if err != nil {
		return nil, err
	}
	query := url.Values{}
	query.Set("root", rootID)
	query.Set("limit", "50")
	if n, ok := feishuTaskNumber(keyword); ok {
		query.Set("task_number", strconv.Itoa(n))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/tasks?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("feishu intake: node %s list tasks: HTTP %d", nodeID, resp.StatusCode)
	}
	var payload struct {
		Items []struct {
			Task struct {
				ID         string `json:"id"`
				TaskNumber int    `json:"task_number"`
				Name       string `json:"name"`
				Status     string `json:"status"`
				RootID     string `json:"root_id"`
			} `json:"task"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("feishu intake: node %s list tasks: bad response: %w", nodeID, err)
	}
	needle := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(keyword), "#"))
	_, exact := feishuTaskNumber(keyword)
	refs := make([]feishu.TaskRef, 0, len(payload.Items))
	for _, item := range payload.Items {
		task := item.Task
		if task.ID == "" {
			continue
		}
		if !exact && needle != "" && !strings.Contains(strings.ToLower(task.Name), needle) {
			continue
		}
		refs = append(refs, feishu.TaskRef{
			NodeID: nodeID, RootID: task.RootID, TaskID: task.ID,
			TaskNumber: task.TaskNumber, Title: task.Name, Status: task.Status,
		})
	}
	return capTaskRefs(refs, feishuTaskRefLimit), nil
}

// feishuTaskRefLimit bounds what a single list returns; the catalog caps its
// candidates at five anyway, and an exact `#N` lookup returns one.
const feishuTaskRefLimit = 20

func capTaskRefs(refs []feishu.TaskRef, limit int) []feishu.TaskRef {
	if limit > 0 && len(refs) > limit {
		return refs[:limit]
	}
	return refs
}

func (c *feishuTaskClient) local() (*api.AppContext, error) {
	if c.workspaces == nil {
		return nil, errors.New("feishu intake: workspace unavailable")
	}
	return c.workspaces.Workspace("")
}

func (c *feishuTaskClient) remoteBase(nodeID string) (string, error) {
	if c.nodes == nil {
		return "", fmt.Errorf("feishu intake: unknown node %q", nodeID)
	}
	for _, node := range c.nodes.List() {
		if node.ID == nodeID {
			if base := strings.TrimRight(strings.TrimSpace(node.URL), "/"); base != "" {
				return base, nil
			}
		}
	}
	return "", fmt.Errorf("feishu intake: unknown node %q", nodeID)
}

func (c *feishuTaskClient) httpClient() *http.Client {
	if c.client != nil {
		return c.client
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// feishuTaskNumber reads a `#<digits>` (or `<digits>`) keyword. Anything else
// is a name filter.
func feishuTaskNumber(keyword string) (int, bool) {
	digits := strings.TrimPrefix(strings.TrimSpace(keyword), "#")
	if digits == "" || len(digits) > 9 {
		return 0, false
	}
	n := 0
	for _, r := range digits {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return n, n > 0
}

func taskGoneBody(raw []byte) bool {
	lower := strings.ToLower(string(raw))
	return strings.Contains(lower, "no rows") || strings.Contains(lower, "not found")
}

func taskGone(err error) bool {
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, kanban.ErrActivityNotFound) {
		return true
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "no rows") || strings.Contains(lower, "not found")
}

// feishuReviewClassifier matches a message against the kanban default review
// categories. The feishu package cannot import kanban, so the matcher is
// injected here and carried on the confirmation card as a warning.
func feishuReviewClassifier(text string) []string {
	lower := strings.ToLower(text)
	var hits []string
	for _, category := range kanban.ReviewCategories {
		if strings.Contains(lower, strings.ToLower(category)) {
			hits = append(hits, category)
		}
	}
	return hits
}
