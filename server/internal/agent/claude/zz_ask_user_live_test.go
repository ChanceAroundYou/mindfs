package claude

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	types "mindfs/server/internal/agent/types"
)

// 真实 CLI 的 AskUserQuestion 端到端（默认跳过：要真 CLI + 网络，不入 CI）。
//
//	MIND_FS_ASK_USER_LIVE=1 go test ./internal/agent/claude/ -run TestLiveAskUserQuestion -v -timeout 300s
//
// 它回答的是一个被记错过的结论：「真实 CLI 发出了 AskUserQuestion，但适配层没把它
// 送进 MindFS（没有 tool_call / 没有 session.done），卡在 agent 边界」。
// 本测试在**适配层**（不经过 HTTP/WS/前端）断言四件事：
//  1. can_use_tool 到达后适配层确实发射了 kind=ask_user 的 tool_call（前端据此渲染卡片）；
//  2. 回答能被 AnswerQuestion 接住并回给 CLI（不报 "question is not pending"）；
//  3. 该轮正常收到 message_done（不会卡在 agent 边界）；
//  4. 模型后续输出里带上了我们选的那个选项 —— 证明答案真的进了对话，而不是被丢弃。
//
// 只在隔离目录里起会话（新建 slug），不碰任何既有会话。
func TestLiveAskUserQuestion(t *testing.T) {
	if os.Getenv("MIND_FS_ASK_USER_LIVE") != "1" {
		t.Skip("set MIND_FS_ASK_USER_LIVE=1（需要真实 claude CLI 与网络）")
	}
	rootPath := os.Getenv("MIND_FS_ASK_USER_LIVE_DIR")
	if rootPath == "" {
		rootPath = "/tmp/mindfs-askuser-live"
	}
	if err := os.MkdirAll(rootPath, 0o755); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	runtime := NewRuntime()
	sess, err := runtime.OpenSession(ctx, OpenOptions{
		AgentName:  "claude",
		SessionKey: "live-ask-user",
		RootPath:   rootPath,
		Command:    strings.TrimSpace(os.Getenv("MIND_FS_CLAUDE_BIN")),
	})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	defer sess.Close()

	var mu sync.Mutex
	var chunks []string
	var askCards []types.ToolCall
	askSeen := make(chan types.ToolCall, 4)
	doneSeen := make(chan struct{}, 4)
	sess.OnUpdate(func(event types.Event) {
		mu.Lock()
		defer mu.Unlock()
		switch event.Type {
		case types.EventTypeMessageChunk:
			if chunk, ok := event.Data.(types.MessageChunk); ok {
				chunks = append(chunks, chunk.Content)
			}
		case types.EventTypeToolCall:
			if call, ok := event.Data.(types.ToolCall); ok && call.Kind == types.ToolKindAskUser {
				askCards = append(askCards, call)
				select {
				case askSeen <- call:
				default:
				}
			}
		case types.EventTypeMessageDone:
			select {
			case doneSeen <- struct{}{}:
			default:
			}
		}
	})

	prompt := "Use the AskUserQuestion tool exactly once to ask me which color I prefer, " +
		"with two options: 'crimson' and 'azure'. After I answer, reply with exactly the " +
		"chosen label and nothing else. Do not use any other tool."
	sendErr := make(chan error, 1)
	go func() { sendErr <- sess.SendMessage(ctx, prompt) }()

	var card types.ToolCall
	select {
	case card = <-askSeen:
	case <-time.After(150 * time.Second):
		t.Fatalf("150s 内没等到 kind=ask_user 的 tool_call —— 适配层没把提问送出来（cards=%d, done=%d）",
			len(askCards), len(doneSeen))
	}
	questions := liveAskUserQuestions(card)
	if len(questions) == 0 {
		t.Fatalf("卡片没有 questions，前端渲染不出可回答的提问：meta=%v", card.Meta)
	}
	answers := map[string]string{}
	for _, question := range questions {
		if len(question.Options) == 0 {
			t.Fatalf("问题没有选项：%+v", question)
		}
		answers[question.Question] = question.Options[0].Label
	}
	log.Printf("[live-test] 收到提问卡 callID=%s answers=%v", card.CallID, answers)

	// 卡片一可见就立刻作答（不重试）：真机上应答通道由 can_use_tool 后登记约 74ms，
	// 这段时间里的答案曾经会被拒 —— 拒掉即永久卡死，所以这里不许重试。
	start := time.Now()
	if err := sess.AnswerQuestion(ctx, types.AskUserAnswer{ToolUseID: card.CallID, Answers: answers}); err != nil {
		t.Fatalf("卡片可见后立刻作答被拒（真机上就是永久卡死）：%v", err)
	}
	log.Printf("[live-test] 卡片可见 → 答案被受理：%v", time.Since(start))

	select {
	case err := <-sendErr:
		if err != nil {
			t.Fatalf("该轮没有正常结束：%v", err)
		}
	case <-time.After(150 * time.Second):
		t.Fatalf("回答后 150s 该轮仍未结束 —— 卡在 agent 边界")
	}

	select {
	case <-doneSeen:
	default:
		t.Fatalf("该轮没有 message_done")
	}

	mu.Lock()
	text := strings.Join(chunks, "")
	cardCount := len(askCards)
	mu.Unlock()
	if cardCount != 1 {
		t.Fatalf("ask_user 卡应恰好发一次（两条路径去重），实际 %d 次", cardCount)
	}
	if !strings.Contains(strings.ToLower(text), "crimson") {
		t.Fatalf("模型输出里没有我们选的选项，答案可能没送达：%q", text)
	}
	t.Logf("该轮输出：%q", strings.TrimSpace(text))
}

// liveAskUserQuestions 与前端 getAskUserQuestions 同口径：先看 meta.questions，
// 再退回 meta.input 里的 JSON。两条发射路径的 meta 形状不同（CanUseTool 那条带
// questions，AssistantMessage 那条只带 input+questionCount），前端两种都能渲染，
// 这里必须同样两种都认，否则会把「卡片其实可回答」误判成缺字段。
func liveAskUserQuestions(call types.ToolCall) []types.AskUserQuestionItem {
	if raw, ok := call.Meta["questions"].([]types.AskUserQuestionItem); ok && len(raw) > 0 {
		return raw
	}
	input, _ := call.Meta["input"].(string)
	if strings.TrimSpace(input) == "" {
		return nil
	}
	var parsed struct {
		Questions []types.AskUserQuestionItem `json:"questions"`
	}
	if err := json.Unmarshal([]byte(input), &parsed); err != nil {
		return nil
	}
	return parsed.Questions
}
