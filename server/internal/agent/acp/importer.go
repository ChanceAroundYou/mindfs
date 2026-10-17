package acp

import (
	"context"
	"errors"
	"strings"
	"sync"

	acpsdk "github.com/coder/acp-go-sdk"

	agenttypes "mindfs/server/internal/agent/types"
)

type ImporterOptions struct {
	AgentName  string
	Command    string
	Args       []string
	Env        map[string]string
	ResolveCwd func(rootPath string) string
}

type Importer struct {
	agentName  string
	command    string
	args       []string
	env        map[string]string
	resolveCwd func(rootPath string) string
}

type importCollector struct {
	mu        sync.Mutex
	exchanges []agenttypes.ImportedExchange
}

func NewImporter(opts ImporterOptions) *Importer {
	return &Importer{
		agentName:  strings.TrimSpace(opts.AgentName),
		command:    strings.TrimSpace(opts.Command),
		args:       append([]string{}, opts.Args...),
		env:        cloneEnv(opts.Env),
		resolveCwd: opts.ResolveCwd,
	}
}

func (i *Importer) AgentName() string {
	return i.agentName
}

// HasTranscriptTimestamps 恒为 false：ACP 协议的消息更新不带任何时间戳
// （`SessionNotification` 只有 sessionId + update）。所以 ACP 导入行的 Timestamp 恒为
// 合成值（见 ImportedExchange.SynthesizedAt），`TimestampFloor` 对它无效。
func (i *Importer) HasTranscriptTimestamps() bool {
	return false
}

func (i *Importer) ListExternalSessions(ctx context.Context, in agenttypes.ListExternalSessionsInput) (agenttypes.ListExternalSessionsResult, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	items := make([]agenttypes.ExternalSessionSummary, 0, limit)
	err := i.ScanExternalSessions(ctx, in, func(item agenttypes.ExternalSessionSummary) (bool, error) {
		items = append(items, item)
		return len(items) < limit, nil
	})
	if err != nil {
		return agenttypes.ListExternalSessionsResult{}, err
	}
	return agenttypes.ListExternalSessionsResult{Items: items}, nil
}

func (i *Importer) ScanExternalSessions(ctx context.Context, in agenttypes.ListExternalSessionsInput, visit agenttypes.ExternalSessionVisitFunc) error {
	proc, cwd, err := i.openProcess(ctx, in.RootPath)
	if err != nil {
		return err
	}
	defer proc.Close()

	var cursor *string
	for {
		resp, err := proc.conn.ListSessions(ctx, acpsdk.ListSessionsRequest{
			Cursor: cursor,
			Cwd:    &cwd,
		})
		if err != nil {
			if isUnsupportedACPListSessions(err) {
				return nil
			}
			return err
		}
		for _, item := range resp.Sessions {
			if normalizeComparablePath(item.Cwd) != normalizeComparablePath(cwd) {
				continue
			}
			updatedAt := parseTimeStringPtr(item.UpdatedAt)
			if !in.BeforeTime.IsZero() && !updatedAt.Before(in.BeforeTime) {
				continue
			}
			if !in.AfterTime.IsZero() && !updatedAt.After(in.AfterTime) {
				continue
			}
			firstUserText := ""
			if item.Title != nil {
				firstUserText = strings.TrimSpace(*item.Title)
			}
			shouldContinue, err := visit(agenttypes.ExternalSessionSummary{
				Agent:          i.agentName,
				AgentSessionID: string(item.SessionId),
				Cwd:            item.Cwd,
				FirstUserText:  firstUserText,
				UpdatedAt:      updatedAt,
			})
			if err != nil {
				return err
			}
			if !shouldContinue {
				return nil
			}
		}
		if resp.NextCursor == nil || strings.TrimSpace(*resp.NextCursor) == "" {
			break
		}
		next := strings.TrimSpace(*resp.NextCursor)
		cursor = &next
	}
	return nil
}

func isUnsupportedACPListSessions(err error) bool {
	var reqErr *acpsdk.RequestError
	if !errors.As(err, &reqErr) {
		return false
	}
	return reqErr.Code == -32601
}

func (i *Importer) ImportExternalSession(ctx context.Context, in agenttypes.ImportExternalSessionInput) (agenttypes.ImportedExternalSession, error) {
	sessionID := strings.TrimSpace(in.AgentSessionID)
	if sessionID == "" {
		return agenttypes.ImportedExternalSession{}, errors.New("agent session id required")
	}
	// 注意：ACP 的 `SessionUpdate` **不带任何时间戳**（SDK 里只有 NES 的
	// `timestampMs`，与消息无关）。所以这里的时间只能是「本次导入运行的观测时刻」，
	// 它**不是**那一轮真实发生的时间，因此：
	//   ① 打上 SynthesizedAt，让下游能区分「真时间」与「猜的时间」；
	//   ② 不能拿它当过滤判据 —— 重放出来的行每次都会拿到一个更新的时间，
	//      `AfterTimestamp` / `TimestampFloor` 永远挡不住（实测 2026-10-07：某
	//      live-owned 会话 Full 同步把 9 对已经写过的轮次又写了一遍）。ACP 的防重放
	//      依赖 native id 身份判重（S2c）。

	proc, cwd, err := i.openProcess(ctx, in.RootPath)
	if err != nil {
		return agenttypes.ImportedExternalSession{}, err
	}
	defer proc.Close()

	collector := &importCollector{}
	if err := proc.attachImportCollector(sessionID, collector); err != nil {
		return agenttypes.ImportedExternalSession{}, err
	}
	defer proc.detachImportCollector(sessionID)

	_, err = proc.conn.LoadSession(ctx, acpsdk.LoadSessionRequest{
		Cwd:        cwd,
		McpServers: []acpsdk.McpServer{},
		SessionId:  acpsdk.SessionId(sessionID),
	})
	if err != nil {
		return agenttypes.ImportedExternalSession{}, err
	}
	return agenttypes.ImportedExternalSession{
		Agent:          i.agentName,
		AgentSessionID: sessionID,
		Cwd:            cwd,
		Exchanges:      collector.snapshot(),
	}, nil
}

func (i *Importer) openProcess(ctx context.Context, rootPath string) (*Process, string, error) {
	if strings.TrimSpace(i.command) == "" {
		return nil, "", errors.New("agent command required")
	}
	cwd := strings.TrimSpace(rootPath)
	if i.resolveCwd != nil {
		cwd = strings.TrimSpace(i.resolveCwd(rootPath))
	}
	if cwd == "" {
		return nil, "", errors.New("root path required")
	}
	proc, err := Start(ctx, i.agentName, i.command, i.args, cwd, i.env)
	if err != nil {
		return nil, "", err
	}
	if err := proc.Initialize(ctx); err != nil {
		proc.Close()
		return nil, "", err
	}
	return proc, cwd, nil
}

func (p *Process) attachImportCollector(sessionID string, collector *importCollector) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return errors.New("session id required")
	}
	if collector == nil {
		return errors.New("collector required")
	}
	state := &sessionState{
		ID: acpsdk.SessionId(sessionID),
	}
	state.setOnUpdate(func(update SessionUpdate) {
		collector.handle(update)
	})
	p.mu.Lock()
	p.sessionsByID[sessionID] = state
	p.mu.Unlock()
	return nil
}

func (p *Process) detachImportCollector(sessionID string) {
	p.mu.Lock()
	delete(p.sessionsByID, strings.TrimSpace(sessionID))
	p.mu.Unlock()
}
