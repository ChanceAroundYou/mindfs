package codex

import (
	"context"
	"log"
	"strings"
	"time"

	codexsdk "github.com/fanwenlin/codex-go-sdk/codex"
)

// codexResumeProbeTimeout 只用于本地 app-server 的 thread/resume 校验，不走网络。
const codexResumeProbeTimeout = 20 * time.Second

// isCodexProviderMismatch 判定 thread/resume 的失败是否属于「线程记录的供应商
// 与当前配置不一致」。codex app-server 实测两种文案：
//
//	This chat uses probe-provider, but your current provider is openai. Switch to probe-provider to continue.
//	This chat uses model provider "llmux", which is no longer configured. Restore this provider's configuration to continue.
//
// 实测 thread/resume 的 modelProvider 覆盖参数对这两种情况都无效（2026-11-09 隔离
// CODEX_HOME 验证），唯一能让历史对话继续的路径是 thread/fork 到当前供应商。
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

// rethreadCodexResumeOnProviderMismatch 在恢复线程前先让 app-server 校验一次。
// 供应商已切换、线程仍记着旧供应商时，把线程 fork 到当前供应商，返回新的线程 id。
//
// fork 会复制完整历史，所以切换供应商后旧会话仍可续聊；新 id 由上层
// ensureAgentSession 立刻写回绑定，避免重复 fork。非供应商原因的失败保持原有
// 惰性语义（不在 open 阶段报错），交由发消息时暴露。
func rethreadCodexResumeOnProviderMismatch(ctx context.Context, client *codexsdk.Codex, threadID string, threadOptions codexsdk.ThreadOptions, rootPath string) (string, bool) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return "", false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	probeCtx, cancel := context.WithTimeout(ctx, codexResumeProbeTimeout)
	defer cancel()
	params := map[string]any{"threadId": threadID}
	if cwd := strings.TrimSpace(rootPath); cwd != "" {
		params["cwd"] = cwd
	}
	_, err := client.AppServerRPC(probeCtx, "thread/resume", params)
	if err == nil || !isCodexProviderMismatch(err) {
		return "", false
	}
	forked, forkErr := forkCodexThread(probeCtx, client, threadOptions, threadID, nil)
	if forkErr != nil {
		log.Printf("[codex] resume.provider_mismatch.fork_error thread=%s err=%v", threadID, forkErr)
		return "", false
	}
	log.Printf("[codex] resume.provider_mismatch.rethreaded source=%s forked=%s", threadID, forked)
	return forked, true
}
