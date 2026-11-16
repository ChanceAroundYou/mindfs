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

func (c workflowLLMClient) Call(ctx context.Context, prompt string) (string, error) {
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// 与 Router 相同的隔离配置目录：不读用户 settings/hooks，工具全部拒绝。
	// 推荐器只需要一次文本分类，不能让用户 Stop hook 的反馈混进 JSON。
	configDir, workDir, err := c.app.routerDirs("workflow-recommender")
	if err != nil {
		return "", err
	}
	return usecase.RunOneShotPrompt(callCtx, c.app.GetAgentPool(), usecase.OneShotPromptInput{
		AgentName:             c.agent,
		Model:                 c.model,
		Prompt:                prompt,
		RootPath:              workDir,
		ConfigDir:             configDir,
		Effort:                "low",
		DeveloperInstructions: "You are a deterministic JSON-only assistant. Return only the requested JSON object; never use tools.",
		ToolPolicy:            &agenttypes.ToolPolicy{DenyByDefault: true},
	})
}
