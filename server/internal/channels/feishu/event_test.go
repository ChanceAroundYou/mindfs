package feishu

import "testing"

func TestParseEventReadsButtonCallbackWithStringActionValue(t *testing.T) {
	line := []byte(`{"type":"card.action.trigger","event_id":"ev_1","operator_id":"ou_owner","message_id":"om_1","chat_id":"oc_1","action_tag":"button","action_value":"{\"card\":\"gate-nr-1\",\"choice\":1}"}`)
	ev, ok := ParseEvent(line)
	if !ok {
		t.Fatal("event not parsed")
	}
	got, ok := ev.ParseAction()
	if !ok || got.CardID != "gate-nr-1" || got.Choice != 1 || got.MessageID != "om_1" || got.ChatID != "oc_1" {
		t.Fatalf("action = %+v ok=%v", got, ok)
	}
	if got.Text != "" {
		t.Fatalf("button click carried text %q", got.Text)
	}
}

func TestParseEventAcceptsObjectActionValue(t *testing.T) {
	ev, ok := ParseEvent([]byte(`{"type":"card.action.trigger","operator_id":"ou_owner","action_value":{"card":"ask-9","choice":0}}`))
	if !ok {
		t.Fatal("event not parsed")
	}
	got, ok := ev.ParseAction()
	if !ok || got.CardID != "ask-9" || got.Choice != 0 {
		t.Fatalf("action = %+v ok=%v", got, ok)
	}
}

func TestParseActionReadsAskTextForm(t *testing.T) {
	ev, _ := ParseEvent([]byte(`{"type":"card.action.trigger","operator_id":"ou_owner","message_id":"om_2","action_name":"ask_text_submit_ask-1","form_value":"{\"note\":\"先做 A\"}"}`))
	got, ok := ev.ParseAction()
	if !ok || got.CardID != "ask-1" || got.Text != "先做 A" {
		t.Fatalf("action = %+v ok=%v", got, ok)
	}
}

func TestParseActionReadsNoticeFormAsAcknowledgement(t *testing.T) {
	ev, _ := ParseEvent([]byte(`{"type":"card.action.trigger","operator_id":"ou_owner","action_name":"handling_note_submit_notice-1","form_value":"{\"note\":\"已回复\"}"}`))
	got, ok := ev.ParseAction()
	if !ok || got.CardID != "notice-1" || got.Choice != 0 || got.Text != "已回复" {
		t.Fatalf("action = %+v ok=%v", got, ok)
	}
}

func TestParseActionRejectsEmptyTextAndUnknownShapes(t *testing.T) {
	ev, _ := ParseEvent([]byte(`{"type":"card.action.trigger","action_name":"ask_text_submit_ask-1","form_value":"{\"note\":\"   \"}"}`))
	if _, ok := ev.ParseAction(); ok {
		t.Fatal("empty text accepted as an answer")
	}
	ev, _ = ParseEvent([]byte(`{"type":"card.action.trigger","action_value":"not-json"}`))
	if _, ok := ev.ParseAction(); ok {
		t.Fatal("unparseable action accepted")
	}
}

func TestParseEventIgnoresOtherTypesAndMalformedJSON(t *testing.T) {
	if _, ok := ParseEvent([]byte(`{"type":"im.message.receive_v1"}`)); ok {
		t.Fatal("wrong type accepted")
	}
	if _, ok := ParseEvent([]byte(`{`)); ok {
		t.Fatal("malformed accepted")
	}
	if _, ok := ParseEvent([]byte(``)); ok {
		t.Fatal("empty line accepted")
	}
}
