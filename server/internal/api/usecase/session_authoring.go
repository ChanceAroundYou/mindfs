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
	agentName = strings.TrimSpace(agentName)
	if agentName == "" {
		return "", errors.New("agent required")
	}
	if pool == nil {
		return "", errors.New("agent pool unavailable")
	}
	if strings.TrimSpace(prompt) == "" {
		return "", errors.New("prompt required")
	}

	tmpRoot, err := agent.EnsureStableWorkDir("workflow-author", agentName)
	if err != nil {
		return "", err
	}
	sessionKey := agentPoolSessionKey(fmt.Sprintf("workflow-author-%d", time.Now().UnixNano()), agentName)
	sess, err := pool.GetOrCreate(ctx, agenttypes.OpenSessionInput{
		SessionKey: sessionKey,
		AgentName:  agentName,
		Model:      strings.TrimSpace(model),
		RootPath:   tmpRoot,
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

	if err := sess.SendMessage(ctx, prompt); err != nil {
		return "", err
	}
	return response.String(), nil
}
