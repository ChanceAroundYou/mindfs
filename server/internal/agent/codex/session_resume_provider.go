package codex

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"

	codexsdk "github.com/fanwenlin/codex-go-sdk/codex"
)

const (
	// codexResumeProbeTimeout 只用于本地 app-server 的 thread/resume 校验，不走网络。
	codexResumeProbeTimeout = 20 * time.Second
	// codexProviderConfigTimeout 只用于本地 app-server 的 config/read。
	codexProviderConfigTimeout = 20 * time.Second
)

// isCodexProviderMismatch 判定 thread/resume 的失败是否属于「线程记录的供应商
// 与当前配置不一致」。codex app-server 实测三种文案：
//
//	This chat uses probe-provider, but your current provider is openai. Switch to probe-provider to continue.
//	This chat uses model provider "llmux", which is no longer configured. Restore this provider's configuration to continue.
//	Cannot change this thread's model provider from `openai` to `llmux` on resume.
//
// 第三种是 codex 0.170.0 起的文案（2026-11-19 fn 上 CMAI 会话实测）。三种情况下
// thread/resume 都不能把线程换到当前供应商；唯一能让历史对话继续的路径是
// thread/fork 到当前供应商。
//
// 这只是兜底：主判定在 codexThreadProviderNeedsFork，直接读线程记录的供应商，
// 不受文案变化影响。
func isCodexProviderMismatch(err error) bool {
	if err == nil {
		return false
	}
	value := strings.ToLower(strings.TrimSpace(err.Error()))
	if value == "" {
		return false
	}
	if strings.Contains(value, "no longer configured") {
		return true
	}
	if strings.Contains(value, "cannot change this thread's model provider") {
		return true
	}
	return strings.Contains(value, "current provider") && strings.Contains(value, "switch to")
}

// codexModelProviderFromConfig 取当前生效的 model_provider。app-server 与 SDK 在
// 该键缺失时都回落到 "openai"，这里保持一致。
func codexModelProviderFromConfig(resp *codexsdk.ConfigReadResponse) string {
	if resp == nil || resp.Config.ModelProvider == nil {
		return "openai"
	}
	if provider := strings.TrimSpace(*resp.Config.ModelProvider); provider != "" {
		return provider
	}
	return "openai"
}

// resolveCodexModelProvider 读一次 config/read 拿当前生效供应商。读取失败返回空串，
// 调用方据此跳过探测而不是带着空供应商去 resume。
func resolveCodexModelProvider(ctx context.Context, client *codexsdk.Codex, cwd string) string {
	if client == nil {
		return ""
	}
	if ctx == nil {
		ctx = context.Background()
	}
	readCtx, cancel := context.WithTimeout(ctx, codexProviderConfigTimeout)
	defer cancel()
	resp, err := client.ReadConfig(readCtx, codexsdk.ConfigReadParams{
		IncludeLayers: false,
		Cwd:           strings.TrimSpace(cwd),
	})
	if err != nil {
		log.Printf("[codex] provider.resolve.error cwd=%q err=%v", cwd, err)
		return ""
	}
	return codexModelProviderFromConfig(resp)
}

// codexThreadProvider 读线程元数据里记录的供应商。thread/read 不加载线程、
// 不碰 writer 锁，返回体只有元数据（实测 1.7 KB），所以能在 resume 之前安全调用；
// 2026-11-19 实测「先 read 再 resume」与「直接 resume」结果一致，read 没有副作用。
// 读不到（线程不存在、旧版 app-server 不认这个 RPC）时第二返回值为 false，
// 调用方退回按错误文案判定。
func codexThreadProvider(ctx context.Context, client *codexsdk.Codex, threadID string) (string, bool) {
	if client == nil || strings.TrimSpace(threadID) == "" {
		return "", false
	}
	var response struct {
		Thread struct {
			ModelProvider string `json:"modelProvider"`
		} `json:"thread"`
	}
	params := map[string]any{"threadId": strings.TrimSpace(threadID)}
	if err := client.AppServerRPCTyped(ctx, "thread/read", params, &response); err != nil {
		return "", false
	}
	return strings.TrimSpace(response.Thread.ModelProvider), true
}

// codexThreadProviderNeedsForkFrom 是主判定的纯逻辑：线程记录的供应商与当前
// 配置不一致时必须 fork。它不依赖 app-server 的错误文案 —— 2026-11-19 codex
// 0.170.0 换了文案，靠字符串匹配的旧判定当场失效，整条会话报
// `Cannot change this thread's model provider ... on resume`。
//
// ok=false 表示元数据没读到（线程不存在、旧版 app-server 不认 thread/read），
// 调用方退回探针路径，保持原有「不在 open 阶段报错」的惰性语义。
func codexThreadProviderNeedsForkFrom(persisted string, ok bool, provider string) (needsFork bool, byMetadata bool) {
	if !ok || strings.TrimSpace(provider) == "" {
		return false, false
	}
	return persisted != "" && !strings.EqualFold(persisted, provider), true
}

// codexResumeProbeParams 组装探测用的 thread/resume 参数。modelProvider 必须始终
// 带上：不带它时 app-server 会把线程按 rollout 里记录的旧供应商加载，之后 SDK 带着
// 正确供应商的 resume 就成了空操作 —— 2026-11-11 冰晶智能会话正是这样继续走 ChatGPT，
// 上下文超窗口后触发远端压缩，报 `workspace routing discovery failed`。
func codexResumeProbeParams(threadID, cwd, provider string) map[string]any {
	params := map[string]any{
		"threadId":      threadID,
		"modelProvider": provider,
	}
	if cwd = strings.TrimSpace(cwd); cwd != "" {
		params["cwd"] = cwd
	}
	return params
}

// codexResumeNeedsFork 判定 resume 的**结果**是否说明线程已不能按当前供应商继续：
// 显式的供应商不一致错误，或 resume 成功但回报的生效供应商不是当前配置（线程
// 已被别处按旧供应商加载，此时带供应商的 resume 不会改写它）。
func codexResumeNeedsFork(result json.RawMessage, provider string, err error) bool {
	if err != nil {
		return isCodexProviderMismatch(err)
	}
	var resp struct {
		ModelProvider string `json:"modelProvider"`
	}
	if len(result) == 0 || json.Unmarshal(result, &resp) != nil {
		return false
	}
	got := strings.TrimSpace(resp.ModelProvider)
	return got != "" && !strings.EqualFold(got, provider)
}

// codexResumeProbeResult 决定一次 resume 前检查是否要 fork，并给出判定依据，
// 便于单测与日志区分「元数据不一致」与「探针失败」两条路径。
func codexResumeProbeResult(
	persistedProvider string,
	persistedOK bool,
	provider string,
	result json.RawMessage,
	probeErr error,
) (needsFork bool, byMetadata bool) {
	if persistedOK {
		// 元数据能读到且与当前配置一致：仍要看探针，因为线程可能已被别处按旧
		// 供应商加载，那种情况下 resume 返回的生效供应商会暴露出来。
		if persistedProvider != "" && !strings.EqualFold(persistedProvider, provider) {
			return true, true
		}
		return codexResumeNeedsFork(result, provider, probeErr), false
	}
	// 读不到元数据（线程不存在 / 旧版 app-server）：退回按错误文案判定。
	return codexResumeNeedsFork(result, provider, probeErr), false
}

// rethreadCodexResumeOnProviderMismatch 在恢复线程前先让 app-server 校验一次。
// 供应商已切换、线程仍记着旧供应商时，把线程 fork 到当前供应商，返回新的线程 id。
//
// fork 会复制完整历史，所以切换供应商后旧会话仍可续聊；新 id 由上层
// ensureAgentSession 立刻写回绑定，避免重复 fork。非供应商原因的失败保持原有
// 惰性语义（不在 open 阶段报错），交由发消息时暴露。
func rethreadCodexResumeOnProviderMismatch(ctx context.Context, client *codexsdk.Codex, threadID string, threadOptions codexsdk.ThreadOptions, rootPath string) (string, bool) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" || client == nil {
		return "", false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	probeCtx, cancel := context.WithTimeout(ctx, codexResumeProbeTimeout)
	defer cancel()
	provider := resolveCodexModelProvider(probeCtx, client, rootPath)
	if provider == "" {
		log.Printf("[codex] resume.provider_probe.skipped thread=%s reason=config_unavailable", threadID)
		return "", false
	}
	// 先读线程自己记录的供应商：这一步与 app-server 的报错文案无关，是主判定。
	persisted, readOK := codexThreadProvider(probeCtx, client, threadID)
	mismatch, byMetadata := codexThreadProviderNeedsForkFrom(persisted, readOK, provider)
	var probeErr error
	if !mismatch {
		// 未命中主判定时仍探一次：线程可能已被别处按旧供应商加载（此时带供应商
		// 的 resume 不会改写它），这种情况只有返回值看得到。
		var result json.RawMessage
		result, probeErr = client.AppServerRPC(probeCtx, "thread/resume", codexResumeProbeParams(threadID, rootPath, provider))
		mismatch, _ = codexResumeProbeResult(persisted, readOK, provider, result, probeErr)
	}
	if !mismatch {
		if probeErr != nil {
			log.Printf("[codex] resume.provider_probe.error thread=%s provider=%s err=%v", threadID, provider, probeErr)
		}
		return "", false
	}
	if byMetadata {
		log.Printf("[codex] resume.provider_mismatch.metadata thread=%s persisted=%s current=%s", threadID, persisted, provider)
	}
	forkOptions := threadOptions
	forkOptions.ModelProvider = provider
	forked, forkErr := forkCodexThread(probeCtx, client, forkOptions, threadID, nil)
	if forkErr != nil {
		log.Printf("[codex] resume.provider_mismatch.fork_error thread=%s provider=%s err=%v", threadID, provider, forkErr)
		return "", false
	}
	log.Printf("[codex] resume.provider_mismatch.rethreaded source=%s provider=%s forked=%s", threadID, provider, forked)
	return forked, true
}
