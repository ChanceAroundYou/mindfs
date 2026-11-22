package feishu

import (
	"encoding/json"
	"strings"
	"testing"
)

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func renderText(t *testing.T, c Card) string {
	t.Helper()
	raw, err := RenderJSON(c)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestRenderGateOpenHasTwoCallbackChoices(t *testing.T) {
	card := Card{ID: "gate-nr-1", Kind: KindGate, State: StateOpen, Prompt: "确认继续",
		Choices: []Choice{{Label: "批准", Value: "approve"}, {Label: "驳回", Value: "reject"}}}
	var out map[string]any
	if err := json.Unmarshal([]byte(renderText(t, card)), &out); err != nil {
		t.Fatal(err)
	}
	if out["schema"] != "2.0" {
		t.Fatalf("schema = %v", out["schema"])
	}
	body := string(mustJSON(t, out["body"]))
	if !strings.Contains(body, `"card":"gate-nr-1"`) || !strings.Contains(body, `"choice":1`) {
		t.Fatalf("body = %s", body)
	}
	if !strings.Contains(body, "批准") || !strings.Contains(body, "驳回") {
		t.Fatalf("body lost labels: %s", body)
	}
	if strings.Contains(body, `"input"`) {
		t.Fatalf("gate card must not render a text input: %s", body)
	}
}

func TestRenderAskAndNoticeHaveTextForms(t *testing.T) {
	ask := renderText(t, Card{ID: "ask-1", Kind: KindAsk, State: StateOpen, Prompt: "选哪个？",
		Choices: []Choice{{Label: "A", Value: "option-0"}}})
	if !strings.Contains(ask, `"form"`) || !strings.Contains(ask, "提交文字") || !strings.Contains(ask, "ask_text_submit_ask-1") {
		t.Fatalf("ask = %s", ask)
	}
	notice := renderText(t, Card{ID: "notice-1", Kind: KindNotice, State: StateOpen, Prompt: "任务完成"})
	if !strings.Contains(notice, "记录并结案") || !strings.Contains(notice, "handling_note_submit_notice-1") {
		t.Fatalf("notice = %s", notice)
	}
}

func TestRenderPickerCardHasProjectChoicesAndNoOtherKinds(t *testing.T) {
	card := Card{ID: "pick-nr-1", Kind: KindPicker, State: StateOpen,
		SourceMessageID: "om_im_1", IntakeText: "修一下",
		Projects: []PickerProject{
			{NodeID: "fn", RootID: "docs", Label: "docs · fn"},
			{NodeID: "pc", RootID: "docs", Label: "docs · pc"},
			{NodeID: "home", RootID: "proj", Label: "proj"},
		}}
	raw := renderText(t, card)
	var out struct {
		Schema string `json:"schema"`
		Header struct {
			Title struct {
				Content string `json:"content"`
			} `json:"title"`
		} `json:"header"`
		Body struct {
			Elements []json.RawMessage `json:"elements"`
		} `json:"body"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	if out.Schema != "2.0" {
		t.Fatalf("schema = %q", out.Schema)
	}
	if len(out.Body.Elements) != 3 {
		t.Fatalf("body has %d elements, want exactly three project buttons: %s", len(out.Body.Elements), raw)
	}
	if !strings.Contains(out.Header.Title.Content, "修一下") {
		t.Fatalf("title lost the original question: %q", out.Header.Title.Content)
	}
	for _, label := range []string{"docs · fn", "docs · pc", "proj"} {
		if !strings.Contains(raw, label) {
			t.Fatalf("picker lost label %q: %s", label, raw)
		}
	}
	if !strings.Contains(raw, `"choice":2`) || strings.Contains(raw, `"choice":3`) {
		t.Fatalf("picker callbacks do not match the three projects: %s", raw)
	}
	if strings.Contains(raw, `"form"`) || strings.Contains(raw, `"input"`) {
		t.Fatalf("picker must not render a free-text form: %s", raw)
	}
}

func TestRenderSettledCardHidesButtonsAndShowsOutcome(t *testing.T) {
	raw := renderText(t, Card{ID: "gate-1", Kind: KindGate, State: StateDone, Selected: 0,
		Choices: []Choice{{Label: "批准", Value: "approve"}}, Summary: "已批准"})
	if strings.Contains(raw, `"callback"`) {
		t.Fatalf("settled card still has buttons: %s", raw)
	}
	if !strings.Contains(raw, "已批准") {
		t.Fatalf("settled card lost outcome: %s", raw)
	}
}

func TestRenderApplyingAndExpiredAreDistinct(t *testing.T) {
	applying := renderText(t, Card{ID: "gate-2", Kind: KindGate, State: StateApplying,
		Choices: []Choice{{Label: "批准", Value: "approve"}}, Selected: 0})
	if !strings.Contains(applying, "已收到你的点击") {
		t.Fatalf("applying = %s", applying)
	}
	expired := renderText(t, Card{ID: "gate-3", Kind: KindGate, State: StateExpired,
		Choices: []Choice{{Label: "批准", Value: "approve"}}})
	if !strings.Contains(expired, "已过期") || strings.Contains(expired, `"callback"`) {
		t.Fatalf("expired = %s", expired)
	}
}
