package api

import (
	"context"
	"reflect"
	"testing"

	"mindfs/server/internal/session"
)

// A+（流式记录）会给每个流式片段写一条 Step=-1 的 provisional 块，正常收尾时
// 再追加正式块；崩溃恢复则追加同 (seq,pos)、revision+1 的封口版本。块流因此
// 同时含有「未收口片段」「正式块」「多个 revision」三种行。
//
// Router 的 router_read_session 读的是用户可见正文：它必须只看到收口后的
// 最终版本，否则 offset=1 会翻到半句片段、同一段回复重复计数。
func TestRouterTranscriptReaderSkipsProvisionalAndStaleRevisions(t *testing.T) {
	app, rootID, manager := newWindowTestApp(t)
	ctx := context.Background()
	s, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Name: "Router transcript"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	appendText := func(seq, pos, revision, step int, text, turnID string) {
		t.Helper()
		err := manager.AppendBlocks(ctx, s.Key, []session.Block{{
			Seq: seq, Pos: pos, Revision: revision, Type: session.BlockTypeText,
			Text: text, Step: step, MindFSID: turnID,
		}})
		if err != nil {
			t.Fatalf("AppendBlocks(seq=%d,pos=%d): %v", seq, pos, err)
		}
	}

	appendText(1, 0, 0, 1, "第一条真实消息", "")
	// 第二条回合：流式片段先落盘（Step=-1），收尾再写正式块。
	appendText(2, 0, 0, -1, "第二", "turn-2")
	appendText(2, 1, 0, -1, "条真实消息", "turn-2")
	appendText(2, 0, 0, 1, "第二条真实消息", "turn-2")
	// 崩溃恢复的封口：同一 (seq,pos) 的更高 revision 才是可见版本。
	appendText(3, 0, 0, 1, "封口前的半句", "turn-3")
	appendText(3, 0, 1, 1, "封口后的整句", "turn-3")

	blocks, err := (routerTranscriptReader{app: app}).ReadSessionBlocks(ctx, rootID, s.Key)
	if err != nil {
		t.Fatalf("ReadSessionBlocks: %v", err)
	}
	texts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block.Type != "text" {
			continue
		}
		texts = append(texts, block.Text)
	}
	want := []string{"第一条真实消息", "第二条真实消息", "封口后的整句"}
	if !reflect.DeepEqual(texts, want) {
		t.Fatalf("Router 转录文本 = %#v, want %#v", texts, want)
	}
}
