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
// 与当前配置不一致」。codex app-server 实测两种文案：
//
//	This chat uses probe-provider, but your current provider is openai. Switch to probe-provider to continue.
//	This chat uses model provider "llmux", which is no longer configured. Restore this provider's configuration to continue.
//
// 这两种情况下 thread/resume 不带 modelProvider 会失败；带上当前供应商仍失败时，
// 唯一能让历史对话继续的路径是 thread/fork 到当前供应商。
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

// codexResumeNeedsFork 判定探测结果是否必须 fork 到当前供应商：显式的供应商
// 不一致错误，或 resume 成功但回报的生效供应商不是当前配置（线程已被别处按旧
// 供应商加载，此时带供应商的 resume 不会改写它）。
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
	result, err := client.AppServerRPC(probeCtx, "thread/resume", codexResumeProbeParams(threadID, rootPath, provider))
	if !codexResumeNeedsFork(result, provider, err) {
		if err != nil {
			log.Printf("[codex] resume.provider_probe.error thread=%s provider=%s err=%v", threadID, provider, err)
		}
		return "", false
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
