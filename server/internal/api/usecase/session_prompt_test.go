package usecase

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mindfs/server/internal/agent"
	agenttypes "mindfs/server/internal/agent/types"
	rootfs "mindfs/server/internal/fs"
	"mindfs/server/internal/session"
)

// switchReadHintPath 一律返回绝对路径：提示文案不含基准目录，agent 无从判断相对路径
// 该以哪里为准，猜成 home 就读不到文件。worktree 与非 worktree 必须给出同一个路径。
func TestSwitchReadHintPathIsAbsolute(t *testing.T) {
	rootDir := t.TempDir()
	root := rootfs.NewRootInfo("mindfs", "mindfs", rootDir)
	manager := session.NewManager(root)
	created, err := manager.Create(context.Background(), session.CreateInput{
		Type: session.TypeChat,
		Name: "Task",
	})
	if err != nil {
		t.Fatalf("Create session: %v", err)
	}

	basePath := switchReadHintPath(manager, created.Key)
	if !filepath.IsAbs(basePath) {
		t.Fatalf("base path = %q, want absolute path", basePath)
	}
	if !strings.HasSuffix(basePath, filepath.ToSlash(filepath.Join(".mindfs", "sessions", created.Key+".jsonl"))) {
		t.Fatalf("base path = %q, want it to point at this session's exchange log", basePath)
	}
}

func TestRelatedFileRecordPathUsesRuntimeRoot(t *testing.T) {
	rootDir := filepath.Clean("/project/mindfs")
	worktreeRoot := filepath.Join(rootDir, ".worktree", "task-55")

	got := relatedFileRecordPath(rootDir, worktreeRoot, "test.json")
	want := filepath.Join(worktreeRoot, "test.json")
	if got != want {
		t.Fatalf("relatedFileRecordPath relative = %q, want %q", got, want)
	}

	got = relatedFileRecordPath(rootDir, worktreeRoot, ".worktree/task-55/test.json")
	want = filepath.Join(rootDir, ".worktree", "task-55", "test.json")
	if got != want {
		t.Fatalf("relatedFileRecordPath worktree rel = %q, want %q", got, want)
	}
}

func TestBuildUserPromptSelectionOnly(t *testing.T) {
	got := buildUserPrompt("hello", ClientContext{})
	if strings.Contains(got, "[USER_SELECTION]") {
		t.Fatalf("did not expect user selection block without selection: %q", got)
	}

	got = buildUserPrompt("hello", ClientContext{
		Selection: &Selection{
			FilePath: "design/README.md",
		},
	})
	if !strings.Contains(got, "[USER_SELECTION]\nfile: design/README.md") {
		t.Fatalf("expected file-only user selection block, got %q", got)
	}

	got = buildUserPrompt("hello", ClientContext{
		Selection: &Selection{
			FilePath:  "design/README.md",
			StartLine: 1,
			EndLine:   3,
			Text:      "abc",
		},
	})
	if !strings.Contains(got, "[USER_SELECTION]\nfile: design/README.md") {
		t.Fatalf("expected user selection block, got %q", got)
	}
}

func TestBuildPromptAddsReplyTipsOnlyWhenUserMessageFallbackIsRequested(t *testing.T) {
	service := &Service{}

	initial := service.BuildPrompt(BuildPromptInput{
		Message:                       "hello",
		IsInitial:                     true,
		IncludeReplyTipsInUserMessage: true,
	})
	if initial != "hello\n\n"+replyTips {
		t.Fatalf("initial prompt does not put the user message before reply tips: %q", initial)
	}

	followup := service.BuildPrompt(BuildPromptInput{
		Message: "hello again",
	})
	if strings.Contains(followup, "[REPLY_TIPS]") {
		t.Fatalf("follow-up prompt unexpectedly contains reply tips: %q", followup)
	}

	native := service.BuildPrompt(BuildPromptInput{
		Message:   "hello",
		IsInitial: true,
	})
	if native != "hello" {
		t.Fatalf("native-instruction prompt changed the user message: %q", native)
	}

	plugin := service.BuildPrompt(BuildPromptInput{
		Message:   "build a plugin",
		IsInitial: true,
		ClientContext: ClientContext{
			PluginCatalog: "plugin catalog",
		},
	})
	if strings.Contains(plugin, "[REPLY_TIPS]") {
		t.Fatalf("plugin prompt unexpectedly contains reply tips: %q", plugin)
	}
}

func TestSessionNameScore(t *testing.T) {
	testCases := []struct {
		name    string
		message string
		want    int
	}{
		{name: "empty", message: "", want: 0},
		{name: "chinese", message: "请帮我排查会话列表刷新问题", want: 13},
		{name: "english token counts once", message: "abcdefghijkl", want: 1},
		{name: "mixed", message: "修复 session list refresh", want: 5},
		{name: "punctuation ignored", message: "fix, bug!", want: 2},
		{name: "digits join token", message: "fix v2 api", want: 3},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sessionNameScore(tc.message); got != tc.want {
				t.Fatalf("sessionNameScore(%q) = %d, want %d", tc.message, got, tc.want)
			}
		})
	}
}

func TestNormalizeSessionNameCandidateOnlyCleans(t *testing.T) {
	input := "  \"这是 一个 很长 的 标题 candidate with trailing punctuation!!!\"  "
	want := "这是 一个 很长 的 标题 candidate with trailing punctuation"
	if got := normalizeSessionNameCandidate(input); got != want {
		t.Fatalf("normalizeSessionNameCandidate(%q) = %q, want %q", input, got, want)
	}
}

func TestSessionNameRunnerRealAgent(t *testing.T) {
	if os.Getenv("MINDFS_RUN_REAL_AGENT") != "1" {
		t.Skip("set MINDFS_RUN_REAL_AGENT=1 to run real agent interaction test")
	}

	cfg, err := agent.LoadConfig("")
	if err != nil {
		t.Skipf("LoadConfig failed: %v", err)
	}

	agentName, ok := selectRunnableAgent(cfg)
	if !ok {
		t.Skip("no runnable configured agent found (set MINDFS_IT_AGENT_NAME)")
	}

	pool := agent.NewPool(cfg)
	defer pool.CloseAll()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	got, err := sessionNameRunner(ctx, pool, t.TempDir(), SuggestSessionNameInput{
		SessionKey:   "real-it-" + time.Now().UTC().Format("20060102-150405"),
		Agent:        agentName,
		FirstMessage: "Please help me investigate why the session list does not refresh immediately after a new session is created.",
	})
	if err != nil {
		t.Fatalf("sessionNameRunner returned error: %v", err)
	}
	if strings.TrimSpace(got) == "" {
		t.Fatal("sessionNameRunner returned empty title")
	}
	if strings.Contains(got, "\n") {
		t.Fatalf("sessionNameRunner returned multi-line title: %q", got)
	}
}

func TestSessionNameRunnerSkipsWithoutAgentOrPool(t *testing.T) {
	testCases := []struct {
		name  string
		input SuggestSessionNameInput
	}{
		{
			name: "missing agent",
			input: SuggestSessionNameInput{
				SessionKey:   "s-1",
				FirstMessage: "hello world session title",
			},
		},
		{
			name: "missing pool",
			input: SuggestSessionNameInput{
				SessionKey:   "s-1",
				Agent:        "codex",
				FirstMessage: "hello world session title",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := sessionNameRunner(context.Background(), nil, "/tmp/root", tc.input)
			if err != nil || got != "" {
				t.Fatalf("sessionNameRunner = (%q, %v), want empty nil", got, err)
			}
		})
	}
}

func TestAppendResponseChunk(t *testing.T) {
	testCases := []struct {
		name     string
		current  string
		lastType string
		chunk    string
		want     string
	}{
		{
			name:     "plain message append",
			current:  "Hello",
			lastType: string(agenttypes.EventTypeMessageChunk),
			chunk:    " world",
			want:     "Hello world",
		},
		{
			name:     "insert separator after thought",
			current:  "First paragraph.",
			lastType: string(agenttypes.EventTypeThoughtChunk),
			chunk:    "Second paragraph.",
			want:     "First paragraph.\n\nSecond paragraph.",
		},
		{
			name:     "insert separator after tool call update",
			current:  "Result summary.",
			lastType: string(agenttypes.EventTypeToolUpdate),
			chunk:    "Follow-up details.",
			want:     "Result summary.\n\nFollow-up details.",
		},
		{
			name:     "keep existing trailing newline",
			current:  "Result summary.\n",
			lastType: string(agenttypes.EventTypeToolCall),
			chunk:    "Follow-up details.",
			want:     "Result summary.\nFollow-up details.",
		},
		{
			name:     "no prefix on empty response",
			current:  "",
			lastType: string(agenttypes.EventTypeToolCall),
			chunk:    "Fresh text.",
			want:     "Fresh text.",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := appendResponseChunk(tc.current, tc.lastType, tc.chunk); got != tc.want {
				t.Fatalf("appendResponseChunk(%q, %q, %q) = %q, want %q", tc.current, tc.lastType, tc.chunk, got, tc.want)
			}
		})
	}
}

// 全新会话的第一轮不该拿到「本会话从别处迁移而来」的提示。
//
// LinesBeforeThisTurn 首轮的真实值恰好是 0。把它当零值哨兵（`> 0` 才采信）会让首轮
// 回退到 len(Exchanges)=1，而 agent 一个字都还没看过 → delta=1>0，提示照发。agent 于是
// 被要求「先读回自己的历史」，读到的是用户刚发的那一条 —— 正是「无意义的 read」。
func TestPrependSwitchHintIsNotInjectedOnFirstTurn(t *testing.T) {
	rootDir := t.TempDir()
	manager := session.NewManager(rootfs.NewRootInfo("mindfs", "mindfs", rootDir))
	created, err := manager.Create(context.Background(), session.CreateInput{
		Type: session.TypeChat,
		Name: "Task",
	})
	if err != nil {
		t.Fatalf("Create session: %v", err)
	}
	// 本轮 user 行在回合开始前已落盘，Exchanges 里已有它。
	created.Exchanges = append(created.Exchanges, session.Exchange{Role: "user", LegacyContent: "hi"})

	service := &Service{}
	linesBefore := 0
	got := service.BuildPrompt(BuildPromptInput{
		Session:             created,
		Manager:             manager,
		Agent:               "claude",
		Message:             "hi",
		LinesBeforeThisTurn: &linesBefore,
	})
	if strings.Contains(got, "This session was migrated from elsewhere.") {
		t.Fatalf("first turn of a new session got the migration hint: %q", got)
	}
	if !strings.Contains(got, "hi") {
		t.Fatalf("user message lost: %q", got)
	}
}

// 回合结束时记 AgentCtxSeq 必须把本轮 user 行算进去。
//
// 时序（SendMessage 内）：linesBeforeThisTurn=N → persistUserTurnExchange 落 N+1
// → AddExchangeForAgent 落 N+2 → UpdateAgentState 记「已落盘总行数」N+2。少记则下一轮
// prependSwitchHint 的 delta 恒 > 0，切 agent 提示在每个正常回合都被重复注入，
// 而不是只在真切换时出现。
func TestPrependSwitchHintIsInjectedOncePerRealSwitch(t *testing.T) {
	rootDir := t.TempDir()
	manager := session.NewManager(rootfs.NewRootInfo("mindfs", "mindfs", rootDir))
	created, err := manager.Create(context.Background(), session.CreateInput{
		Type: session.TypeChat,
		Name: "Task",
	})
	if err != nil {
		t.Fatalf("Create session: %v", err)
	}

	sess := created
	service := &Service{}
	const agentName = "claude"

	// 两轮对话：每轮 user 行 + agent 行各一条。回合结束时 agent 看到的行数按
	// 「user 行之前 +1」记账（对应 UpdateAgentState(linesBeforeThisTurn+1)）。
	total := 0
	agentSeen := 0
	agentInjected := 0
	for turn := 0; turn < 2; turn++ {
		linesBeforeThisTurn := total
		// 本轮 user 行落盘（persistUserTurnExchange）。
		total++

		got := service.BuildPrompt(BuildPromptInput{
			Session:             sess,
			Manager:             manager,
			Agent:               agentName,
			Message:             "hi",
			AgentCtxSeq:         &agentSeen,
			LinesBeforeThisTurn: &linesBeforeThisTurn,
		})
		if strings.Contains(got, "This session was migrated from elsewhere.") {
			agentInjected++
		}
		if !strings.Contains(got, "hi") {
			t.Fatalf("turn %d: user message lost from prompt: %q", turn, got)
		}

		// 助手行落盘（AddExchangeForAgent）。
		total++
		// UpdateAgentState 记 agent 已看到的行数 = 已落盘总行数（user + agent 两行）。
		agentSeen = linesBeforeThisTurn + 2
	}

	if agentInjected != 0 {
		t.Fatalf("switch hint injected %d times across 2 turns of the same agent, want 0", agentInjected)
	}
}

// 真切换时提示仍要出现，且带绝对路径 —— 这是本次修复要保住的行为。
func TestPrependSwitchHintOnAgentSwitchUsesAbsolutePath(t *testing.T) {
	rootDir := t.TempDir()
	manager := session.NewManager(rootfs.NewRootInfo("mindfs", "mindfs", rootDir))
	created, err := manager.Create(context.Background(), session.CreateInput{
		Type: session.TypeChat,
		Name: "Task",
	})
	if err != nil {
		t.Fatalf("Create session: %v", err)
	}

	service := &Service{}
	// 切到 codex：claude 只看过 1 行，会话已有 4 行 → delta=3，提示该出现。
	claudeSeen := 1
	linesBefore := 4
	got := service.BuildPrompt(BuildPromptInput{
		Session:             created,
		Manager:             manager,
		Agent:               "codex",
		Message:             "hi",
		AgentCtxSeq:         &claudeSeen,
		LinesBeforeThisTurn: &linesBefore,
	})
	if !strings.Contains(got, "This session was migrated from elsewhere.") {
		t.Fatalf("switch hint missing on real agent switch: %q", got)
	}
	wantPath := filepath.ToSlash(filepath.Join(rootDir, ".mindfs", "sessions", created.Key+".jsonl"))
	if !strings.Contains(got, wantPath) {
		t.Fatalf("switch hint does not carry absolute path %q: %q", wantPath, got)
	}
}

// 真切换时提示要指向块流，而不是 content 恒空的主日志。
//
// S2d-2 起主日志的 content 一律为空，正文只在 <key>.blocks.jsonl 里。提示若仍
// 指向主日志，agent 读完拿到一串空 content，等于没恢复上下文（甚至误判历史丢失）。
// 读取量必须换算成块行数：1 条交换行 ≈ 多个块，主日志口径的「2 行」在块流里不够。
func TestPrependSwitchHintPointsAtBlocksWhenAvailable(t *testing.T) {
	rootDir := t.TempDir()
	manager := session.NewManager(rootfs.NewRootInfo("mindfs", "mindfs", rootDir))
	created, err := manager.Create(context.Background(), session.CreateInput{
		Type: session.TypeChat,
		Name: "Task",
	})
	if err != nil {
		t.Fatalf("Create session: %v", err)
	}

	blocks := []session.Block{
		{Seq: 1, Pos: 0, Type: session.BlockTypeText, Text: "seen"},
		{Seq: 2, Pos: 0, Type: session.BlockTypeText, Text: "unseen-a"},
		{Seq: 2, Pos: 1, Type: session.BlockTypeText, Text: "unseen-b"},
		{Seq: 3, Pos: 0, Type: session.BlockTypeText, Text: "unseen-c"},
	}
	if err := manager.AppendBlocks(context.Background(), created.Key, blocks); err != nil {
		t.Fatalf("AppendBlocks: %v", err)
	}

	sess := created
	sess.Exchanges = []session.Exchange{
		{Seq: 1, Role: "user"},
		{Seq: 2, Role: "agent"},
		{Seq: 3, Role: "user"},
	}
	service := &Service{}
	claudeSeen := 1 // claude 看过 seq 1 为止
	linesBefore := 3
	got := service.BuildPrompt(BuildPromptInput{
		Session:             sess,
		Manager:             manager,
		Agent:               "codex",
		Message:             "hi",
		AgentCtxSeq:         &claudeSeen,
		LinesBeforeThisTurn: &linesBefore,
	})

	wantPath := filepath.ToSlash(filepath.Join(rootDir, ".mindfs", "sessions", created.Key+".blocks.jsonl"))
	wantRead := "read the last 3 lines from " + wantPath
	if !strings.Contains(got, wantRead) {
		t.Fatalf("switch hint does not ask for the unseen block lines %q: %q", wantRead, got)
	}
}

// 日志文件不可读时不要注入迁移提示。
//
// 提示文案最后一句是「Only if reading fails, output a brief error and stop」——
// 路径解析错位（2026-11-12 fn 实测）时，这一句直接让 agent 每轮停摆。少一份上下文
// 可以接受，整轮无法继续不行。
func TestPrependSwitchHintSkippedWhenLogMissing(t *testing.T) {
	rootDir := t.TempDir()
	manager := session.NewManager(rootfs.NewRootInfo("mindfs", "mindfs", rootDir))
	created, err := manager.Create(context.Background(), session.CreateInput{
		Type: session.TypeChat,
		Name: "Task",
	})
	if err != nil {
		t.Fatalf("Create session: %v", err)
	}
	if err := os.Remove(manager.ExchangeLogAbsolutePath(created.Key)); err != nil {
		t.Fatalf("remove exchange log: %v", err)
	}

	service := &Service{}
	codexSeen := 1
	linesBefore := 4
	got := service.BuildPrompt(BuildPromptInput{
		Session:             created,
		Manager:             manager,
		Agent:               "codex",
		Message:             "hi",
		AgentCtxSeq:         &codexSeen,
		LinesBeforeThisTurn: &linesBefore,
	})
	if strings.Contains(got, "This session was migrated from elsewhere.") {
		t.Fatalf("missing exchange log still got the migration hint: %q", got)
	}
	if !strings.Contains(got, "hi") {
		t.Fatalf("user message lost: %q", got)
	}
}

// ---- 以下为上游 v0.5.5 独有、与任务组无关的回归测试（合并时保留） ----

func TestAssistantAuxLineBeforeFollowingText(t *testing.T) {
	for _, tc := range []struct {
		before string
		line   int
	}{
		{"", 0},
		{"\n\n", 0},
		{"说明", 1},
		{"说明\n", 1},
		{"说明\n\n", 1},
		{"第一行\n第二行\n", 2},
		{"第一行\n\n第二行\n\n", 3},
	} {
		before := tc.before
		t.Run(fmt.Sprintf("prefix_%q", before), func(t *testing.T) {
			line := currentAssistantLine(before)
			if line != tc.line {
				t.Fatalf("aux line = %d, want %d", line, tc.line)
			}
			content := appendResponseChunk(before, string(agenttypes.EventTypeToolCall), "工具后的回复\n后续内容")
			if before == "" || strings.HasSuffix(before, "\n") {
				if content != before+"工具后的回复\n后续内容" {
					t.Fatalf("response whitespace changed: %q", content)
				}
			}
			// Match the history viewer's line-based split around an auxiliary event.
			lines := strings.Split(content, "\n")
			preceding := strings.Join(lines[:line], "\n")
			following := strings.Join(lines[line:], "\n")
			if strings.TrimRight(preceding, "\n") != strings.TrimRight(before, "\n") {
				t.Fatalf("text before tool = %q, want %q", preceding, before)
			}
			if strings.TrimLeft(following, "\n") != "工具后的回复\n后续内容" {
				t.Fatalf("text after tool = %q", following)
			}
		})
	}
}
