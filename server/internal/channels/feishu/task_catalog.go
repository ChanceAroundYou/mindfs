package feishu

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

// TaskRef 是某台机器上一个任务的最小投影：确认卡要显示「#42 修登录」，
// 并把它作为用户可改写的默认值。NodeID 为空表示本机。
type TaskRef struct {
	NodeID     string
	RootID     string
	TaskID     string
	TaskNumber int
	Title      string
	Status     string
}

// TaskFinder 列出 (节点, 项目) 下的任务。实现由 app 层提供：本机读库，
// 远程节点走主控中继。keyword 仅供能服务端过滤的实现使用，可以忽略。
type TaskFinder interface {
	List(ctx context.Context, nodeID, rootID, keyword string) ([]TaskRef, error)
}

// TaskCatalog 把任务列表按 (节点, 项目, 关键词) 缓存并挑出候选。
type TaskCatalog interface {
	Match(ctx context.Context, nodeID, rootID, keyword string) ([]TaskRef, error)
}

const (
	taskCatalogTTL = 60 * time.Second
	// taskCatalogLimit 是确认卡上最多列出的候选数：再多就不是「确认」而是列表了。
	taskCatalogLimit = 5
)

type taskCacheEntry struct {
	refs      []TaskRef
	fetchedAt time.Time
}

type taskCatalog struct {
	finder TaskFinder
	ttl    time.Duration
	now    func() time.Time

	mu    sync.Mutex
	cache map[string]taskCacheEntry
}

// NewTaskCatalog 建一个按 TTL 缓存的任务目录。finder 为 nil 时 Match 恒空 ——
// 没接任务源的节点仍然能用「新建」，只是没有可预填的候选。
func NewTaskCatalog(finder TaskFinder, ttl time.Duration, now func() time.Time) TaskCatalog {
	if ttl <= 0 {
		ttl = taskCatalogTTL
	}
	if now == nil {
		now = time.Now
	}
	return &taskCatalog{finder: finder, ttl: ttl, now: now, cache: map[string]taskCacheEntry{}}
}

func (c *taskCatalog) Match(ctx context.Context, nodeID, rootID, keyword string) ([]TaskRef, error) {
	nodeID = strings.TrimSpace(nodeID)
	rootID = strings.TrimSpace(rootID)
	keyword = strings.TrimSpace(keyword)
	if rootID == "" {
		return nil, errors.New("feishu task catalog: project required")
	}
	if c.finder == nil {
		return nil, nil
	}
	key := nodeID + "\x00" + rootID + "\x00" + strings.ToLower(keyword)
	c.mu.Lock()
	if entry, ok := c.cache[key]; ok && c.now().Sub(entry.fetchedAt) < c.ttl {
		out := append([]TaskRef(nil), entry.refs...)
		c.mu.Unlock()
		return out, nil
	}
	c.mu.Unlock()

	list, err := c.finder.List(ctx, nodeID, rootID, keyword)
	if err != nil {
		return nil, err
	}
	refs := matchTaskRefs(list, keyword, taskCatalogLimit)
	c.mu.Lock()
	c.cache[key] = taskCacheEntry{refs: append([]TaskRef(nil), refs...), fetchedAt: c.now()}
	c.mu.Unlock()
	return refs, nil
}

// matchTaskRefs 是纯函数：编号精确命中排前面，其次是标题/编号的子串命中
// （大小写不敏感），最多 limit 条。空关键词表示「没有指定」，按输入顺序取前
// limit 条 —— 调用方用它给「最近的任务」做候选。
func matchTaskRefs(list []TaskRef, keyword string, limit int) []TaskRef {
	keyword = strings.TrimSpace(keyword)
	wantNumber, hasNumber := parseTaskNumber(keyword)
	needle := strings.ToLower(strings.TrimPrefix(keyword, "#"))
	var exact, loose []TaskRef
	for _, ref := range list {
		if ref.TaskID == "" {
			continue
		}
		if keyword == "" {
			loose = append(loose, ref)
			continue
		}
		if hasNumber && ref.TaskNumber == wantNumber {
			exact = append(exact, ref)
			continue
		}
		if strings.Contains(strings.ToLower(ref.Title), needle) {
			loose = append(loose, ref)
		}
	}
	out := append(exact, loose...)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// parseTaskNumber 认 "#42" 与 "42" 两种写法；其它输入不算编号。
func parseTaskNumber(keyword string) (int, bool) {
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
