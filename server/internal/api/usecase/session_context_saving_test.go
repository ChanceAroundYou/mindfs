package usecase

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"mindfs/server/internal/agent"
	"mindfs/server/internal/agent/proxy"
	agenttypes "mindfs/server/internal/agent/types"
	rootfs "mindfs/server/internal/fs"
	"mindfs/server/internal/preferences"
	"mindfs/server/internal/session"
)

// contextSavingTestRegistry 只实现这条用例要用的三个方法：会话管理器、池、
// 以及一个永远成功的 GetRoot。其余方法返回零值即可 —— 这条链路不该碰它们。
type contextSavingTestRegistry struct {
	root       rootfs.RootInfo
	manager    *session.Manager
	pool       *agent.Pool
	archiveDir string
}

func (r *contextSavingTestRegistry) GetRoot(rootID string) (rootfs.RootInfo, error) {
	if rootID != r.root.ID {
		return rootfs.RootInfo{}, errors.New("root not found")
	}
	return r.root, nil
}

func (r *contextSavingTestRegistry) GetSessionManager(string) (*session.Manager, error) {
	return r.manager, nil
}

func (r *contextSavingTestRegistry) UpsertRoot(string) (rootfs.RootInfo, error) {
	return rootfs.RootInfo{}, errors.New("not implemented")
}

func (r *contextSavingTestRegistry) RemoveRoot(string) (rootfs.RootInfo, error) {
	return rootfs.RootInfo{}, errors.New("not implemented")
}

func (r *contextSavingTestRegistry) RenameRoot(string, string, string) (rootfs.RootInfo, error) {
	return rootfs.RootInfo{}, errors.New("not implemented")
}

func (r *contextSavingTestRegistry) UpdateDisplayName(string, string) (rootfs.RootInfo, error) {
	return rootfs.RootInfo{}, errors.New("not implemented")
}

func (r *contextSavingTestRegistry) ListRoots() []rootfs.RootInfo {
	return []rootfs.RootInfo{r.root}
}

func (r *contextSavingTestRegistry) GetAgentPool() *agent.Pool { return r.pool }

func (r *contextSavingTestRegistry) GetPreferences() *preferences.Store { return nil }

func (r *contextSavingTestRegistry) GetExternalSessionImporter(string) (agenttypes.ExternalSessionImporter, error) {
	return nil, errors.New("not implemented")
}

func (r *contextSavingTestRegistry) GetProber() *agent.Prober { return nil }

func (r *contextSavingTestRegistry) GetCandidateRegistry() *CandidateRegistry {
	return NewCandidateRegistry()
}

func (r *contextSavingTestRegistry) GetFileWatcher(string, *session.Manager) (*rootfs.SharedFileWatcher, error) {
	return nil, nil
}

func (r *contextSavingTestRegistry) ReleaseFileWatcher(string, string) {}

// 会话绑定的 agent 决定池级 key。账本只写在 `claude-<key>` 上，再拿**裸 key 的会话**
// 查询：读得到说明用例做了 agent 解析，读不到就是回归（症状是胶囊永远不出现，
// 不报错，所以只能靠测试）。
func TestGetSessionContextSavingResolvesPoolSessionKey(t *testing.T) {
	registry, service, current, rootID := newContextSavingFixture(t)
	recordFoldInLedger(t, registry.archiveDir, "claude-"+current.Key)

	saving, err := service.GetSessionContextSaving(context.Background(), GetSessionContextSavingInput{
		RootID: rootID, Key: current.Key,
	})
	if err != nil {
		t.Fatalf("usecase error: %v", err)
	}
	if saving.Requests != 1 || saving.Latest.SavedBytes <= 0 {
		t.Fatalf("usecase did not resolve the pool key: %+v", saving)
	}
}

// 同一份账本放在裸 key 上时，带 agent 的会话**不该**读到它 —— 防止用例偷偷用裸 key
// 兜底而让上一条恰好通过。
func TestGetSessionContextSavingDoesNotReadBareLedger(t *testing.T) {
	registry, service, current, rootID := newContextSavingFixture(t)
	recordFoldInLedger(t, registry.archiveDir, current.Key)

	saving, err := service.GetSessionContextSaving(context.Background(), GetSessionContextSavingInput{
		RootID: rootID, Key: current.Key,
	})
	if err != nil {
		t.Fatalf("usecase error: %v", err)
	}
	if saving.Requests != 0 {
		t.Fatalf("read the bare-key ledger for an agent-bound session: %+v", saving)
	}
}

func TestGetSessionContextSavingRejectsBlankKey(t *testing.T) {
	_, service, _, rootID := newContextSavingFixture(t)
	if _, err := service.GetSessionContextSaving(context.Background(), GetSessionContextSavingInput{
		RootID: rootID, Key: "   ",
	}); err == nil {
		t.Fatal("blank key accepted")
	}
}

func TestGetSessionContextSavingWithoutPoolReturnsEmpty(t *testing.T) {
	registry, _, current, rootID := newContextSavingFixture(t)
	registry.pool = nil
	saving, err := (&Service{Registry: registry}).GetSessionContextSaving(context.Background(), GetSessionContextSavingInput{
		RootID: rootID, Key: current.Key,
	})
	if err != nil {
		t.Fatalf("a missing pool is not an error: %v", err)
	}
	if saving.Requests != 0 || saving.Latest.SavedBytes != 0 {
		t.Fatalf("summary without a pool = %+v", saving)
	}
}

// newContextSavingFixture 建一个真实会话（聊天类型、绑定 claude）与一个真实池。
func newContextSavingFixture(t *testing.T) (*contextSavingTestRegistry, *Service, *session.Session, string) {
	t.Helper()
	root, err := rootfs.NewRegistry(filepath.Join(t.TempDir(), "registry.json")).Upsert(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager := session.NewManager(root)
	current, err := manager.Create(context.Background(), session.CreateInput{Type: session.TypeChat, Agent: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	// CreateInput 的 agent 只是一个初始 exchange；InferAgentFromSession 读的是绑定表。
	if err := manager.UpdateAgentState(context.Background(), current, "claude", 1, "agent-session-1"); err != nil {
		t.Fatal(err)
	}
	archiveDir := filepath.Join(t.TempDir(), "context")
	pool := agent.NewPoolWithOptions(agent.Config{}, agent.PoolOptions{
		LiveCompactMode:       proxy.LiveCompactOn,
		LiveCompactArchiveDir: archiveDir,
	})
	t.Cleanup(pool.CloseAll)
	registry := &contextSavingTestRegistry{root: root, manager: manager, pool: pool, archiveDir: archiveDir}
	return registry, &Service{Registry: registry}, current, root.ID
}

// recordFoldInLedger 用**另一个代理实例**指向同一存档目录，发一个真实可折叠请求，
// 让账本按生产路径写进磁盘。不直接写文件：那条前提（代理真的在记账）本身也要被测。
func recordFoldInLedger(t *testing.T, archiveDir, sessionKey string) {
	t.Helper()
	ps, err := proxy.NewServerWithLiveCompact(proxy.LiveCompactConfig{
		// RailTokens: -1 disables the rail gate: this helper only needs one
		// real fold on disk, not the fold policy (design §12.6).
		Mode: proxy.LiveCompactOn, RailTokens: -1, ArchiveDir: archiveDir, ArchiveEnabled: true,
	})
	if err != nil {
		t.Fatalf("proxy: %v", err)
	}
	t.Cleanup(func() { _ = ps.Close() })
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data\n"))
	}))
	t.Cleanup(upstream.Close)
	token := "fixture-token"
	ps.RegisterSessionWithKey(token, upstream.URL, sessionKey)
	url := fmt.Sprintf("http://127.0.0.1:%d/p/%s/v1/messages", ps.Port(), token)
	resp, err := http.Post(url, "application/json", strings.NewReader(contextSavingFoldBody()))
	if err != nil {
		t.Fatalf("fold request: %v", err)
	}
	_ = resp.Body.Close()
}

// contextSavingFoldBody 是代理测试里同形的最小可折叠请求（4 轮、每轮 8 KB 工具输出）。
func contextSavingFoldBody() string {
	var builder strings.Builder
	builder.WriteString(`{"model":"test","messages":[{"role":"user","content":[{"type":"text","text":"start"}]}`)
	for index := 0; index < 4; index++ {
		id := fmt.Sprintf("toolu_%d", index)
		builder.WriteString(fmt.Sprintf(`,{"role":"assistant","content":[{"type":"tool_use","id":%q,"name":"Read","input":{"file_path":"/x"}}]}`, id))
		builder.WriteString(fmt.Sprintf(`,{"role":"user","content":[{"type":"tool_result","tool_use_id":%q,"content":%q,"is_error":false}]}`, id, strings.Repeat("a", 8001)))
	}
	builder.WriteString("]}")
	return builder.String()
}
