package claude

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"mindfs/server/internal/agent/logs"
	types "mindfs/server/internal/agent/types"

	claudeagent "github.com/roasbeef/claude-agent-sdk-go"
)

type OpenOptions struct {
	AgentName             string
	SessionKey            string
	Model                 string
	Effort                string
	PlanMode              bool
	RootPath              string
	Command               string
	Args                  []string
	Env                   map[string]string
	DeveloperInstructions string
	ResumeSessionID       string
	ForkSessionID         string
	ResumeMessageID       string
}

type Runtime struct{}

func NewRuntime() *Runtime {
	return &Runtime{}
}

func (r *Runtime) OpenSession(ctx context.Context, opts OpenOptions) (types.Session, error) {
	if opts.SessionKey == "" {
		return nil, errors.New("session key required")
	}

	s := &session{
		sessionKey:      opts.SessionKey,
		model:           canonicalClaudeModel(opts.Model),
		planMode:        opts.PlanMode,
		agentDebugLog:   logs.NewAgentLogger(opts.RootPath, opts.SessionKey, opts.AgentName),
		questionWaits:   make(map[string]chan askUserAnswerResult),
		emittedAskUsers: make(map[string]struct{}),
		rootPath:        opts.RootPath,
		baseEnv:         cloneEnv(opts.Env),
	}

	optionList := []claudeagent.Option{
		claudeagent.WithCwd(opts.RootPath),
		claudeagent.WithEnv(opts.Env),
		claudeagent.WithVerbose(true),
		claudeagent.WithIncludePartialMessages(true),
		claudeagent.WithAgentProgressSummaries(true),
		claudeagent.WithForwardSubagentText(true),
		claudeagent.WithCanUseTool(s.handleCanUseTool),
	}
	optionList = appendClaudeDeveloperInstructions(optionList, opts.DeveloperInstructions)
	if strings.TrimSpace(opts.Command) != "" {
		optionList = append(optionList, claudeagent.WithCLIPath(opts.Command))
	}
	forkSessionID := strings.TrimSpace(opts.ForkSessionID)
	resumeMessageID := strings.TrimSpace(opts.ResumeMessageID)
	explicitSessionID := ""
	if forkSessionID != "" && resumeMessageID != "" {
		forked, err := claudeagent.ForkSession(forkSessionID, &claudeagent.ForkSessionOptions{
			UpToMessageID: resumeMessageID,
		})
		if err != nil {
			return nil, fmt.Errorf("fork claude session at message: %w", err)
		}
		if forked == nil || strings.TrimSpace(forked.SessionID) == "" {
			return nil, errors.New("fork claude session did not return session id")
		}
		explicitSessionID = strings.TrimSpace(forked.SessionID)
		optionList = append(optionList, claudeagent.WithResume(explicitSessionID))
	} else if forkSessionID != "" {
		optionList = append(optionList, claudeagent.WithForkSession(forkSessionID))
	} else if strings.TrimSpace(opts.ResumeSessionID) != "" {
		optionList = append(optionList, claudeagent.WithResume(strings.TrimSpace(opts.ResumeSessionID)))
	}
	if resumeMessageID != "" && forkSessionID == "" {
		optionList = append(optionList, claudeagent.WithResumeSessionAt(resumeMessageID))
	}
	if strings.TrimSpace(opts.Model) != "" {
		optionList = append(optionList, claudeagent.WithModel(s.resolveClaudeModelArg(opts.Model)))
	}
	if strings.TrimSpace(opts.Effort) != "" {
		optionList = append(optionList, claudeagent.WithEffort(claudeagent.EffortLevel(strings.TrimSpace(opts.Effort))))
	}
	if opts.PlanMode {
		optionList = append(optionList, claudeagent.WithPermissionMode(claudeagent.PermissionModePlan))
	}

	client, err := claudeagent.NewClient(optionList...)
	if err != nil {
		return nil, err
	}
	stream, err := client.Stream(ctx)
	if err != nil {
		client.Close()
		return nil, err
	}

	selectedModel := canonicalClaudeModel(opts.Model)
	if selectedModel == "" && opts.ResumeSessionID == "" {
		if candidate, ok := claudeFirstAvailableModel(client); ok {
			selectedModel = canonicalClaudeModel(candidate)
		}
	}
	// WithModel already selects an explicitly requested model at CLI startup.
	// A control-plane SetModel here would repeat the same switch immediately and
	// force an unnecessary provider confirmation. Keep SetModel only for the
	// first-available fallback, where no model was passed to the CLI.
	if selectedModel != "" && strings.TrimSpace(opts.Model) == "" {
		if err := stream.SetModel(ctx, s.resolveClaudeModelArg(selectedModel)); err != nil {
			client.Close()
			return nil, err
		}
	}
	if opts.PlanMode {
		if err := stream.SetPermissionMode(ctx, claudeagent.PermissionModePlan); err != nil {
			client.Close()
			return nil, err
		}
	}

	s.client = client
	s.stream = stream
	s.sessionID = stream.SessionID()
	if strings.TrimSpace(s.sessionID) == "" {
		s.sessionID = explicitSessionID
	}
	s.model = selectedModel
	go s.consumeMessages()
	return s, nil
}

func appendClaudeDeveloperInstructions(options []claudeagent.Option, developerInstructions string) []claudeagent.Option {
	if instructions := strings.TrimSpace(developerInstructions); instructions != "" {
		return append(options, claudeagent.WithExtraArgs(map[string]*string{
			"append-system-prompt": &instructions,
		}))
	}
	return options
}

func (r *Runtime) CloseAll() {}

func claudeFirstAvailableModel(client *claudeagent.Client) (string, bool) {
	if client == nil {
		return "", false
	}
	for _, item := range client.SupportedModelsFromInit() {
		candidate := strings.TrimSpace(item.Value)
		if candidate == "" || strings.EqualFold(candidate, "default") {
			continue
		}
		return candidate, true
	}
	return "", false
}
