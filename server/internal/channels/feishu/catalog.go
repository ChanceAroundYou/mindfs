package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"mindfs/server/internal/nodes"
)

// Binding 是 chat_id → {node_id, root_id} 的显式入口绑定。app 包持有等价结构，
// 在构造通道时显式映射到这里；feishu 包不依赖 app。
type Binding struct {
	ChatID string
	NodeID string
	RootID string
}

// ProjectRef 是飞书入口能选中的一台机器上的一个项目目录。
// NodeID 为空表示本机（不需要跨节点中继）。
type ProjectRef struct {
	NodeID string
	RootID string
	Name   string
}

// ProjectCatalog 给项目选择卡与文字入口提供候选项目。
type ProjectCatalog interface {
	List(ctx context.Context) ([]ProjectRef, error)
}

const (
	catalogTTL          = 60 * time.Second
	catalogFetchTimeout = 5 * time.Second
)

type projectCatalog struct {
	nodeLister interface{ List() []nodes.NodeConnection }
	local      func() []ProjectRef
	ttl        time.Duration
	now        func() time.Time
	fetch      func(context.Context, string) ([]byte, error)

	mu        sync.Mutex
	cached    []ProjectRef
	fetchedAt time.Time
	loaded    bool
}

// NewProjectCatalog 建一个按 TTL 缓存的项目目录。本地项目由 local 提供；
// 其余节点通过 GET <node.URL>/api/dirs 发现。单个节点失败只记日志并跳过，
// 全都失败且本地也没有项目时才返回 error。
func NewProjectCatalog(
	nodeLister interface{ List() []nodes.NodeConnection },
	local func() []ProjectRef,
	ttl time.Duration,
	now func() time.Time,
	fetch func(context.Context, string) ([]byte, error),
) ProjectCatalog {
	if ttl <= 0 {
		ttl = catalogTTL
	}
	if now == nil {
		now = time.Now
	}
	return &projectCatalog{
		nodeLister: nodeLister,
		local:      local,
		ttl:        ttl,
		now:        now,
		fetch:      fetch,
	}
}

func (c *projectCatalog) List(ctx context.Context) ([]ProjectRef, error) {
	// 锁覆盖整次刷新：低 QPS 的入口消息不该让并发请求重复打遍所有节点。
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.loaded && c.now().Sub(c.fetchedAt) < c.ttl {
		return append([]ProjectRef(nil), c.cached...), nil
	}

	refs := make([]ProjectRef, 0)
	seen := make(map[string]bool)
	add := func(ref ProjectRef) {
		ref.NodeID = strings.TrimSpace(ref.NodeID)
		ref.RootID = strings.TrimSpace(ref.RootID)
		ref.Name = strings.TrimSpace(ref.Name)
		if ref.RootID == "" || ref.Name == "" {
			return
		}
		key := ref.NodeID + "\x00" + ref.RootID
		if seen[key] {
			return
		}
		seen[key] = true
		refs = append(refs, ref)
	}
	if c.local != nil {
		for _, ref := range c.local() {
			add(ref)
		}
	}

	attempts, reached := 0, 0
	if c.nodeLister != nil && c.fetch != nil {
		for _, node := range c.nodeLister.List() {
			url := strings.TrimRight(strings.TrimSpace(node.URL), "/") + "/api/dirs"
			if strings.TrimSpace(node.URL) == "" {
				continue
			}
			attempts++
			body, err := c.fetchRemote(ctx, url)
			if err != nil {
				log.Printf("[feishu/catalog] node=%s dirs failed: %v", node.ID, err)
				continue
			}
			var entries []struct {
				ID          string `json:"id"`
				DisplayName string `json:"display_name"`
			}
			if err := json.Unmarshal(body, &entries); err != nil {
				log.Printf("[feishu/catalog] node=%s dirs decode failed: %v", node.ID, err)
				continue
			}
			reached++
			labelNode := strings.TrimSpace(node.Name)
			if labelNode == "" {
				labelNode = node.ID
			}
			for _, entry := range entries {
				name := strings.TrimSpace(entry.DisplayName)
				if strings.TrimSpace(entry.ID) == "" || name == "" {
					continue
				}
				add(ProjectRef{NodeID: node.ID, RootID: entry.ID, Name: name + " · " + labelNode})
			}
		}
	}
	if attempts > 0 && reached == 0 && len(refs) == 0 {
		return nil, errors.New("feishu project catalog: no node reachable")
	}

	c.cached = append([]ProjectRef(nil), refs...)
	c.fetchedAt = c.now()
	c.loaded = true
	return append([]ProjectRef(nil), c.cached...), nil
}

func (c *projectCatalog) fetchRemote(ctx context.Context, url string) ([]byte, error) {
	fetchCtx, cancel := context.WithTimeout(ctx, catalogFetchTimeout)
	defer cancel()
	return c.fetch(fetchCtx, url)
}
