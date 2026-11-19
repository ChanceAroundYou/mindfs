package api

import (
	"context"
	"strings"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/api/usecase"
	"mindfs/server/internal/kanban"
)

// workflowLLMClient 是 workflow 自动选择与结果转换共用的最小模型客户端。
//
// 它复用一次性的 agent 回合（与工作流作者模式同一个 primitive），不建会话树、
// 不写任务数据；调用方只关心返回的文本。模型/agent 来自 Router 配置，所以
// “自动选择”“结果转换”和 Router 使用同一个（可以很便宜的）模型。
type workflowLLMClient struct {
	app   *AppContext
	agent string
	model string
}

// NewWorkflowLLMClient 构造推荐器/结果转换器客户端。agent 为空时返回 nil，
// 调用方保持「第一个可用 workflow」或「原文即 summary」的兼容回退；model 为空
// 时使用 agent 的默认模型。
func NewWorkflowLLMClient(app *AppContext, agentName, model string) kanban.LLMClient {
	agentName = strings.TrimSpace(agentName)
	if app == nil || agentName == "" {
		return nil
	}
	return workflowLLMClient{app: app, agent: agentName, model: strings.TrimSpace(model)}
}

// NewWorkflowSummaryCompressor 构造结算结果压缩器（task #87 第 2 条）。与推荐器/
// 结果转换器同一个一次性 agent 回合，只是提示词要自然语言短总结。agent 未配置
// 时返回 nil，结算保持引擎挑出的原文。
func NewWorkflowSummaryCompressor(app *AppContext, agentName, model string) kanban.SummaryCompressor {
	agentName = strings.TrimSpace(agentName)
	if app == nil || agentName == "" {
		return nil
	}
	return workflowLLMClient{app: app, agent: agentName, model: strings.TrimSpace(model)}
}

func (c workflowLLMClient) Call(ctx context.Context, prompt string) (string, error) {
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// 与 Router 相同的隔离配置目录：不读用户 settings/hooks，工具全部拒绝。
	// 推荐器只需要一次文本分类，不能让用户 Stop hook 的反馈混进 JSON。
	return runOneShotText(callCtx, c.app, c.agent, c.model, prompt,
		"You are a deterministic JSON-only assistant. Return only the requested JSON object; never use tools.")
}

// Compress 是结算结果压缩器（task #87 第 2 条）。与 Call 同一套一次性 agent
// 回合，只是提示词不同：它要一段自然语言短总结，不是 JSON。
func (c workflowLLMClient) Compress(ctx context.Context, prompt string) (string, error) {
	// 上层已经包了超时；这里只兜底，避免调用方漏包时无限等。
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}
	return runOneShotText(ctx, c.app, c.agent, c.model, prompt,
		"You compress finished work results for the user. Return only the summary sentence; never use tools.")
}

func runOneShotText(ctx context.Context, app *AppContext, agentName, model, prompt, developer string) (string, error) {
	configDir, workDir, err := app.routerDirs("workflow-recommender")
	if err != nil {
		return "", err
	}
	return usecase.RunOneShotPrompt(ctx, app.GetAgentPool(), usecase.OneShotPromptInput{
		AgentName:             agentName,
		Model:                 model,
		Prompt:                prompt,
		RootPath:              workDir,
		ConfigDir:             configDir,
		Effort:                "low",
		DeveloperInstructions: developer,
		ToolPolicy:            &agenttypes.ToolPolicy{DenyByDefault: true},
	})
}
