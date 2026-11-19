package codex

import (
	"context"
	"os"
	"testing"
	"time"

	codexsdk "github.com/fanwenlin/codex-go-sdk/codex"
)

// 端到端验证「元数据判定 → fork → 新线程可 resume」这条链路真的能跑通。
//
// Opt-in：需要一个**记录的供应商与当前配置不同**的历史线程 id：
//
//	MINDFS_TEST_CODEX_PROVIDER_MISMATCH_THREAD=<thread id> \
//	MINDFS_TEST_CODEX_PROVIDER_MISMATCH_PROVIDER=llmux \
//	go test ./server/internal/agent/codex/ -run TestRethreadCodexResumeOnProviderMismatchLive -v
//
// 它会 fork 出一个新线程，结束时归档，不改动源线程。
func TestRethreadCodexResumeOnProviderMismatchLive(t *testing.T) {
	threadID := os.Getenv("MINDFS_TEST_CODEX_PROVIDER_MISMATCH_THREAD")
	if threadID == "" {
		t.Skip("set MINDFS_TEST_CODEX_PROVIDER_MISMATCH_THREAD to test against the installed app server")
	}
	wantProvider := os.Getenv("MINDFS_TEST_CODEX_PROVIDER_MISMATCH_PROVIDER")
	if wantProvider == "" {
		wantProvider = "llmux"
	}
	rootPath, _ := os.Getwd()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	client := newClient(OpenOptions{Command: "codex"})
	defer client.Close()

	provider := resolveCodexModelProvider(ctx, client, rootPath)
	if provider == "" {
		t.Fatalf("config/read 没返回供应商，无法验证")
	}
	if provider != wantProvider {
		t.Skipf("当前配置供应商是 %q，不是 %q；这条用例要求线程记录的供应商与之不同", provider, wantProvider)
	}

	persisted, ok := codexThreadProvider(ctx, client, threadID)
	if !ok {
		t.Fatalf("thread/read 读不到 %s 的元数据", threadID)
	}
	if persisted == provider {
		t.Skipf("源线程记录的供应商已是 %q，没有不一致可验证", provider)
	}
	t.Logf("源线程记录 provider=%s，当前配置 provider=%s", persisted, provider)

	threadOptions := codexsdk.ThreadOptions{WorkingDirectory: rootPath}
	forked, did := rethreadCodexResumeOnProviderMismatch(ctx, client, threadID, threadOptions, rootPath)
	if !did || forked == "" {
		t.Fatalf("元数据不一致却没有 fork（返回 %q/%v）", forked, did)
	}
	defer func() {
		if _, err := client.AppServerRPC(context.Background(), "thread/archive", map[string]any{"threadId": forked}); err != nil {
			t.Errorf("archive test fork: %v", err)
		}
	}()

	got, ok := codexThreadProvider(ctx, client, forked)
	if !ok {
		t.Fatalf("读不到 fork 后线程 %s 的元数据", forked)
	}
	if got != provider {
		t.Fatalf("fork 出来的线程 provider=%q，want %q", got, provider)
	}

	// 关键回归点：fork 出的线程必须真的能按当前供应商 resume —— 这正是修复前
	// 整条会话报 `Cannot change this thread's model provider ... on resume` 的地方。
	result, err := client.AppServerRPC(ctx, "thread/resume", codexResumeProbeParams(forked, rootPath, provider))
	if err != nil {
		t.Fatalf("fork 后 resume 失败: %v", err)
	}
	if codexResumeNeedsFork(result, provider, nil) {
		t.Fatalf("fork 后 resume 仍报供应商不一致: %s", string(result))
	}
}
