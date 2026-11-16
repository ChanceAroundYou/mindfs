package usecase

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"mindfs/server/internal/agent"
	agenttypes "mindfs/server/internal/agent/types"
)

// RunWorkflowAuthoringPrompt is a one-shot, unpersisted Agent turn used by the
// workflow authoring gateway. It reads the caller-supplied prompt and returns
// raw text; schema validation and dry-run happen outside this function. The
// session is closed immediately and never becomes a user-visible session.
func RunWorkflowAuthoringPrompt(ctx context.Context, pool *agent.Pool, agentName, model, prompt string) (string, error) {
	return RunOneShotPrompt(ctx, pool, OneShotPromptInput{
		AgentName: agentName,
		Model:     model,
		Prompt:    prompt,
		RootPath:  "",
	})
}

// OneShotPromptInput 是一次性 agent 回合的完整输入。ConfigDir/Tools/ToolPolicy
// 供 Router / 推荐器这类“不能读用户 settings 和 hooks”的场景使用；普通作者模式
// 留空即可保持原行为。
type OneShotPromptInput struct {
	AgentName             string
	Model                 string
	Prompt                string
	RootPath              string
	ConfigDir             string
	Effort                string
	DeveloperInstructions string
	Tools                 []agenttypes.ToolSpec
	ToolPolicy            *agenttypes.ToolPolicy
}

func RunOneShotPrompt(ctx context.Context, pool *agent.Pool, in OneShotPromptInput) (string, error) {
	agentName := strings.TrimSpace(in.AgentName)
	if agentName == "" {
		return "", errors.New("agent required")
	}
	if pool == nil {
		return "", errors.New("agent pool unavailable")
	}
	if strings.TrimSpace(in.Prompt) == "" {
		return "", errors.New("prompt required")
	}
	rootPath := strings.TrimSpace(in.RootPath)
	if rootPath == "" {
		var err error
		rootPath, err = agent.EnsureStableWorkDir("workflow-author", agentName)
		if err != nil {
			return "", err
		}
	}
	sessionKey := agentPoolSessionKey(fmt.Sprintf("one-shot-%d", time.Now().UnixNano()), agentName)
	sess, err := pool.GetOrCreate(ctx, agenttypes.OpenSessionInput{
		SessionKey:            sessionKey,
		AgentName:             agentName,
		Model:                 strings.TrimSpace(in.Model),
		Effort:                strings.TrimSpace(in.Effort),
		RootPath:              rootPath,
		ConfigDir:             strings.TrimSpace(in.ConfigDir),
		DeveloperInstructions: strings.TrimSpace(in.DeveloperInstructions),
		Tools:                 in.Tools,
		ToolPolicy:            in.ToolPolicy,
	})
	if err != nil {
		return "", err
	}
	defer pool.Close(sessionKey)

	var response strings.Builder
	sess.OnUpdate(func(update agenttypes.Event) {
		if update.Type != agenttypes.EventTypeMessageChunk {
			return
		}
		chunk, ok := update.Data.(agenttypes.MessageChunk)
		if !ok {
			return
		}
		response.WriteString(chunk.Content)
	})

	if err := sess.SendMessage(ctx, in.Prompt); err != nil {
		return "", err
	}
	return response.String(), nil
}
