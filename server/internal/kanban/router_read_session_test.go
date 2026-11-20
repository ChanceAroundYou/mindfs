package kanban

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// 用户裁定（2026-11-19，#86 复盘）：Router 要有读会话的权力 —— 一个专用工具，
// 默认只给最后一段文本，用 offset 往前翻。这样节点结果「投影没写、行是 unknown」
// 时，Router 能自己去转录里确认这一轮到底做完没有，而不是反复问用户
// 「请先结算 Run」。

type fakeTranscriptReader struct {
	blocks map[string][]RouterTranscriptBlock
	err    error
	seen   []string
}

func (f *fakeTranscriptReader) ReadSessionBlocks(_ context.Context, rootID, sessionKey string) ([]RouterTranscriptBlock, error) {
	f.seen = append(f.seen, rootID+"/"+sessionKey)
	if f.err != nil {
		return nil, f.err
	}
	return f.blocks[sessionKey], nil
}

type readSessionPayload struct {
	SessionKey string `json:"session_key"`
	TextBlocks int    `json:"text_blocks"`
	Blocks     []struct {
		Offset int    `json:"offset"`
		Text   string `json:"text"`
	} `json:"blocks"`
}

func readSessionEnv(t *testing.T) (*TaskStore, *routerToolEnv, *fakeTranscriptReader) {
	t.Helper()
	store, _, run, _ := newWorkflowCommentFixture(t, "task-read", "run-read", WorkflowDefinition{
		Nodes: []WorkflowNode{{ID: "execute", Kind: NodeKindAgent, Params: map[string]any{"prompt": "归档会话"}}},
	})
	startWorkflowAgentNode(t, store, run.ID, "execute", "sess-node")
	task, err := store.GetTask(context.Background(), "task-read")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	reader := &fakeTranscriptReader{blocks: map[string][]RouterTranscriptBlock{
		"sess-node": {
			{Type: "text", Text: "第一块：先看现状"},
			{Type: "toolcall", Text: "Bash(git log)"},
			{Type: "reasoning", Text: "内部推理"},
			{Type: "text", Text: "第二块：已提交 07424b66，侧栏同步摘除"},
		},
	}}
	brief := briefFor(t, store, task.ID)
	return store, &routerToolEnv{store: store, brief: brief, transcripts: reader}, reader
}

func callReadSession(t *testing.T, env *routerToolEnv, args map[string]any) (readSessionPayload, error) {
	t.Helper()
	var spec *RouterToolSpec
	for _, candidate := range env.tools() {
		if candidate.Name == "router_read_session" {
			spec = &candidate
		}
	}
	if spec == nil {
		t.Fatal("router_read_session is not exposed")
	}
	if spec.Class != RouterToolRead {
		t.Fatalf("router_read_session class = %v, want read（读工具必须立即执行）", spec.Class)
	}
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	out, err := spec.Handler(context.Background(), raw)
	if err != nil {
		return readSessionPayload{}, err
	}
	var payload readSessionPayload
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal payload %q: %v", out, err)
	}
	return payload, nil
}

func TestRouterReadSessionDefaultsToLatestTextBlock(t *testing.T) {
	_, env, reader := readSessionEnv(t)
	payload, err := callReadSession(t, env, map[string]any{})
	if err != nil {
		t.Fatalf("read session: %v", err)
	}
	if payload.SessionKey != "sess-node" {
		t.Fatalf("default session = %q, want the node run session", payload.SessionKey)
	}
	if payload.TextBlocks != 2 {
		t.Fatalf("text_blocks = %d, want 2（只数文本块）", payload.TextBlocks)
	}
	if len(payload.Blocks) != 1 || !strings.Contains(payload.Blocks[0].Text, "07424b66") {
		t.Fatalf("默认必须只给最后一段文本，got %+v", payload.Blocks)
	}
	if len(reader.seen) != 1 || reader.seen[0] != "wf-comment-root/sess-node" {
		t.Fatalf("reader calls = %v, want one call on the task session", reader.seen)
	}
}

func TestRouterReadSessionOffsetWalksBack(t *testing.T) {
	_, env, _ := readSessionEnv(t)
	payload, err := callReadSession(t, env, map[string]any{"offset": 1})
	if err != nil {
		t.Fatalf("read session offset=1: %v", err)
	}
	if len(payload.Blocks) != 1 || !strings.Contains(payload.Blocks[0].Text, "第一块") {
		t.Fatalf("offset=1 要给倒数第二段文本，got %+v", payload.Blocks)
	}
	if payload.Blocks[0].Offset != 1 {
		t.Fatalf("block offset = %d, want 1（模型要能据此继续往前翻）", payload.Blocks[0].Offset)
	}
}

func TestRouterReadSessionLimitReturnsNewestFirst(t *testing.T) {
	_, env, _ := readSessionEnv(t)
	payload, err := callReadSession(t, env, map[string]any{"limit": 2})
	if err != nil {
		t.Fatalf("read session limit=2: %v", err)
	}
	if len(payload.Blocks) != 2 || !strings.Contains(payload.Blocks[0].Text, "第二块") || !strings.Contains(payload.Blocks[1].Text, "第一块") {
		t.Fatalf("limit=2 要按从新到旧给两段，got %+v", payload.Blocks)
	}
}

func TestRouterReadSessionReadsExplicitTaskSession(t *testing.T) {
	_, env, reader := readSessionEnv(t)
	reader.blocks["sess-node"] = append(reader.blocks["sess-node"], RouterTranscriptBlock{Type: "text", Text: "第三块：复核通过"})
	payload, err := callReadSession(t, env, map[string]any{"session_key": "sess-node", "offset": 0})
	if err != nil {
		t.Fatalf("read session: %v", err)
	}
	if len(payload.Blocks) != 1 || !strings.Contains(payload.Blocks[0].Text, "第三块") {
		t.Fatalf("显式 session_key 要读指定会话，got %+v", payload.Blocks)
	}
}

// 只能读这个任务的会话：别的任务/别的账户的 key 一律拒绝，即使它读得出来。
func TestRouterReadSessionRejectsForeignSession(t *testing.T) {
	_, env, reader := readSessionEnv(t)
	reader.blocks["sess-foreign"] = []RouterTranscriptBlock{{Type: "text", Text: "别的任务的机密"}}
	_, err := callReadSession(t, env, map[string]any{"session_key": "sess-foreign"})
	if err == nil || !strings.Contains(err.Error(), "not a session of this task") {
		t.Fatalf("foreign session must be rejected, got %v", err)
	}
}

func TestRouterReadSessionWithoutReaderFails(t *testing.T) {
	_, env, _ := readSessionEnv(t)
	env.transcripts = nil
	_, err := callReadSession(t, env, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("无读端口时必须显式报错，got %v", err)
	}
}

func TestRouterReadSessionTruncatesHugeBlock(t *testing.T) {
	_, env, reader := readSessionEnv(t)
	huge := strings.Repeat("归档", 4000) // 24000 字节
	reader.blocks["sess-node"] = append(reader.blocks["sess-node"], RouterTranscriptBlock{Type: "text", Text: huge})
	payload, err := callReadSession(t, env, map[string]any{})
	if err != nil {
		t.Fatalf("read session: %v", err)
	}
	if len(payload.Blocks) != 1 {
		t.Fatalf("blocks = %+v, want 1", payload.Blocks)
	}
	got := payload.Blocks[0].Text
	if len(got) > routerReadSessionBlockMaxBytes+len("…") {
		t.Fatalf("单块必须截断到 %d 字节以内，got %d", routerReadSessionBlockMaxBytes, len(got))
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("截断要留标记，got tail %q", got[max(0, len(got)-8):])
	}
}
