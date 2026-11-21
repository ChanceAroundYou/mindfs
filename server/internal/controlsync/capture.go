package controlsync

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// FallbackInterval 是兜底全量扫描的默认周期。HTTP 中间件只能看见走 HTTP 的写入；
// 启动迁移、定时任务这类直接改 store 的路径只能靠定时比对补上。
const FallbackInterval = 15 * time.Second

// recordState 是采集器对「索引库里这条记录现在是什么」的记忆。
// 删除的墓碑保留最后一次内容 hash，所以状态必须是 (hash, deleted) 二元组而不是单个 hash —
// 只看 hash 会把「内容 A 删掉」和「存在内容 A」当成同一件事。
type recordState struct {
	hash    string
	deleted bool
}

// Capturer 把权威存储的变化采集进同步索引库（records + changes）。
//
// 它不改任何业务数据、也不调 Apply：只做 Snapshot → 与上次基线比对 → 落 change。
// 远端记录的应用走 Domain.Apply，并且必须包在 Suppress 里 —— 否则采集器分不清
// 「远端写进来的」和「本地人改的」，边缘会把主节点的改动原样回传，通道自激。
type Capturer struct {
	store    *Store
	registry *Registry
	origin   string
	now      func() time.Time

	// OnCaptureError 接调用方的日志。采集失败不改写 HTTP 响应（请求已经成功），
	// 下一次兜底扫描会把这次变化补上。
	OnCaptureError func(error)

	mu         sync.Mutex
	suppressed int
	last       map[string]map[string]recordState
}

// NewCapturer 组装采集器。origin 写入每条 change 的 Origin（节点名），方便排查
// 「这条记录是谁写的」；空 origin 允许，只是诊断信息少一截。
func NewCapturer(store *Store, registry *Registry, origin string) (*Capturer, error) {
	if store == nil {
		return nil, errors.New("controlsync: nil store")
	}
	if registry == nil {
		return nil, errors.New("controlsync: nil registry")
	}
	return &Capturer{
		store:    store,
		registry: registry,
		origin:   strings.TrimSpace(origin),
		now:      time.Now,
		last:     make(map[string]map[string]recordState),
	}, nil
}

// CaptureDomain 采集一个域，返回写入的 change 条数。没有变化就不写。
//
// 基线优先取内存；进程刚起时从索引库的 records 表加载 —— 重启不该把整库当成新数据
// 重新发一遍。
func (c *Capturer) CaptureDomain(ctx context.Context, domain string) (int, error) {
	if c == nil {
		return 0, errors.New("controlsync: nil capturer")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.captureDomainLocked(ctx, domain)
}

// CaptureAll 采集全部已注册的域。某个域失败不阻断其他域（它的变化留给下一次兜底扫描），
// 错误合并返回。
func (c *Capturer) CaptureAll(ctx context.Context) (int, error) {
	if c == nil {
		return 0, errors.New("controlsync: nil capturer")
	}
	var total int
	var errs error
	for _, domain := range c.registry.Names() {
		n, err := c.CaptureDomain(ctx, domain)
		total += n
		if err != nil {
			errs = errors.Join(errs, err)
		}
	}
	return total, errs
}

// CapturePaths 把一批请求路径映射成域并采集。同一条请求链上重复命中同一个域只采集一次。
// HTTP 中间件（WriteCapture）与需要手动补采的调用方共用这条路径映射。
func (c *Capturer) CapturePaths(ctx context.Context, paths ...string) (int, error) {
	seen := make(map[string]bool, len(paths))
	var domains []string
	for _, path := range paths {
		domain, ok := DomainForPath(path)
		if !ok || seen[domain] {
			continue
		}
		seen[domain] = true
		domains = append(domains, domain)
	}
	var total int
	var errs error
	for _, domain := range domains {
		n, err := c.CaptureDomain(ctx, domain)
		total += n
		if err != nil {
			errs = errors.Join(errs, err)
		}
	}
	return total, errs
}

// Suppress 在 fn 执行期间暂停采集；fn 成功后把给定域的当前状态记为基线。
//
// 为什么成功后要「采纳基线」而不只是「跳过采集」：fn 通常就是 Domain.Apply，它改的
// 是同一份权威存储，采集器在快照上看不出这次变化来自远端。只跳过当下的采集，下一次
// 兜底扫描仍会把它当成本地改动发出去。
//
// fn 失败时不采纳基线：远端记录可能只落了一半，下一次采集会把残留当成一次本地改动
// 回传；同样的内容 hash 到主节点是幂等的，宁可多一次回声，也不能把真实的本地改动一起吞掉。
func (c *Capturer) Suppress(ctx context.Context, domains []string, fn func() error) error {
	if c == nil {
		return errors.New("controlsync: nil capturer")
	}
	if fn == nil {
		return errors.New("controlsync: nil apply func")
	}
	if len(domains) == 0 {
		domains = c.registry.Names()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.suppressed++
	err := func() error {
		defer func() { c.suppressed-- }()
		return fn()
	}()
	if err != nil {
		return err
	}
	var errs error
	for _, domain := range domains {
		if err := c.adoptBaselineLocked(ctx, domain); err != nil {
			errs = errors.Join(errs, err)
		}
	}
	return errs
}

// WriteCapture 是挂在控制面路由前的 HTTP 中间件：非 GET 请求成功（2xx）后采集对应域。
//
// 只挂在「剥掉部署前缀之后」的 mux 上，路径语义与 nodeinfo.IsControlPlane 一致。
// 采集失败不改写响应 —— 请求本身已经成功，失败只记日志，变化由兜底扫描补。
func (c *Capturer) WriteCapture(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		domain, ok := DomainForPath(r.URL.Path)
		if !ok || isReadMethod(r.Method) {
			next.ServeHTTP(w, r)
			return
		}
		rec := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if rec.status < 200 || rec.status > 299 {
			return
		}
		// 客户端断开不该让采集半途而废：请求已经写进权威存储，这条变化必须落账。
		if _, err := c.CaptureDomain(context.WithoutCancel(r.Context()), domain); err != nil {
			c.reportError(err)
		}
	})
}

// Run 每 interval 兜底全量采集一次，直到 ctx 取消。interval ≤ 0 用 FallbackInterval。
func (c *Capturer) Run(ctx context.Context, interval time.Duration) {
	if c == nil {
		return
	}
	if interval <= 0 {
		interval = FallbackInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := c.CaptureAll(ctx); err != nil {
				c.reportError(err)
			}
		}
	}
}

func (c *Capturer) reportError(err error) {
	if err != nil && c.OnCaptureError != nil {
		c.OnCaptureError(err)
	}
}

func (c *Capturer) captureDomainLocked(ctx context.Context, domain string) (int, error) {
	if c.suppressed > 0 {
		return 0, nil
	}
	d, err := c.registry.Get(domain)
	if err != nil {
		return 0, err
	}
	current, err := d.Snapshot(ctx)
	if err != nil {
		return 0, fmt.Errorf("controlsync: snapshot %s: %w", domain, err)
	}
	prev, err := c.baselineLocked(ctx, domain)
	if err != nil {
		return 0, err
	}
	present := make(map[string]bool, len(current))
	var changed []Record
	for _, rec := range current {
		present[rec.Key] = true
		if old, ok := prev[rec.Key]; ok && !old.deleted && old.hash == rec.Hash {
			continue
		}
		rec.Origin = c.origin
		changed = append(changed, rec)
	}
	for key, old := range prev {
		if old.deleted || present[key] {
			continue
		}
		changed = append(changed, Record{
			Domain:  domain,
			Key:     key,
			Hash:    old.hash,
			Deleted: true,
			Origin:  c.origin,
		})
	}
	if len(changed) == 0 {
		return 0, nil
	}
	// 同一次采集里的顺序固定下来，日志与测试才好读；rev 由事务按序分配。
	sort.Slice(changed, func(i, j int) bool { return changed[i].Key < changed[j].Key })
	if _, err := c.store.WriteRecords(ctx, changed); err != nil {
		return 0, err
	}
	next := make(map[string]recordState, len(current))
	for _, rec := range current {
		next[rec.Key] = recordState{hash: rec.Hash}
	}
	// 权威存储里已经没有的 key：基线记成删除态（保留最后一次 hash），
	// 否则下一次采集会为同一个删除再发一条墓碑。
	for key, old := range prev {
		if _, ok := next[key]; !ok {
			next[key] = recordState{hash: old.hash, deleted: true}
		}
	}
	c.last[domain] = next
	return len(changed), nil
}

// baselineLocked 返回某域的基线（必须有 c.mu）。
func (c *Capturer) baselineLocked(ctx context.Context, domain string) (map[string]recordState, error) {
	if states, ok := c.last[domain]; ok {
		return states, nil
	}
	stored, err := c.store.RecordStates(ctx, domain)
	if err != nil {
		return nil, err
	}
	states := make(map[string]recordState, len(stored))
	for key, state := range stored {
		states[key] = recordState{hash: state.Hash, deleted: state.Deleted}
	}
	c.last[domain] = states
	return states, nil
}

// adoptBaselineLocked 把某域的当前快照记为基线，不写 change（必须有 c.mu）。
// 快照失败时丢掉内存基线并报错：留着旧基线会让下一次采集把所有远端改动重新发一遍，
// 而丢掉基线后至少会重新从索引库加载，退回「多一次回声」而不是「批量回传」。
func (c *Capturer) adoptBaselineLocked(ctx context.Context, domain string) error {
	d, err := c.registry.Get(domain)
	if err != nil {
		return err
	}
	current, err := d.Snapshot(ctx)
	if err != nil {
		delete(c.last, domain)
		return fmt.Errorf("controlsync: adopt baseline %s: %w", domain, err)
	}
	prev := c.last[domain]
	next := make(map[string]recordState, len(current))
	for _, rec := range current {
		next[rec.Key] = recordState{hash: rec.Hash}
	}
	for key, old := range prev {
		if _, ok := next[key]; !ok {
			next[key] = recordState{hash: old.hash, deleted: true}
		}
	}
	c.last[domain] = next
	return nil
}

// DomainForPath 把（已剥掉部署前缀的）请求路径映射到同步域。
//
// 只映射真正会改控制面数据的路径：/api/auth 登录会动 users.json 的登录时间，但它不是
// 「账户变更」，把它算进 users 域会让每次登录都产生一条同步记录。web-push、节点自述、
// 应用更新这些属于本机状态，不参与同步。
func DomainForPath(path string) (string, bool) {
	path = strings.TrimRight(path, "/")
	if path == "" {
		return "", false
	}
	for _, m := range capturePathTable {
		if path == m.prefix || strings.HasPrefix(path, m.prefix+"/") {
			return m.domain, true
		}
	}
	return "", false
}

var capturePathTable = []struct {
	prefix string
	domain string
}{
	{prefix: "/api/users", domain: DomainUsers},
	{prefix: "/api/preferences", domain: DomainPreferences},
	{prefix: "/api/nodes", domain: DomainNodes},
	{prefix: "/api/prompts", domain: DomainPrompts},
	{prefix: "/api/pins", domain: DomainPins},
	{prefix: "/api/workflows", domain: DomainWorkflows},
}

func isReadMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

// statusWriter 记录响应状态码，其余行为原样透传。
type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.status = code
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(p []byte) (int, error) {
	w.wroteHeader = true
	return w.ResponseWriter.Write(p)
}

// Unwrap 让 http.ResponseController 还能拿到底层 writer（Flush / Hijack 等）。
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
