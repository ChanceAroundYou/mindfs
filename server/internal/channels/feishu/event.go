package feishu

import (
	"encoding/json"
	"strconv"
	"strings"
)

// Event is one line from `lark-cli event consume`. Only the fields a click
// needs are read; the rest stays unparsed so a schema change upstream cannot
// break the listener.
type Event struct {
	Type        string          `json:"type"`
	EventID     string          `json:"event_id"`
	OperatorID  string          `json:"operator_id"`
	MessageID   string          `json:"message_id"`
	ChatID      string          `json:"chat_id"`
	ActionTag   string          `json:"action_tag"`
	ActionName  string          `json:"action_name"`
	ActionValue json.RawMessage `json:"action_value"`
	FormValue   json.RawMessage `json:"form_value"`
}

// Action is a click reduced to what the card service acts on.
type Action struct {
	CardID     string
	Choice     int
	Text       string
	MessageID  string
	OperatorID string
	ChatID     string
	// Fields carries the editable values a target card submits with the click
	// (task number, title). Absent or empty is a nil map, never a panic.
	Fields map[string]string
}

const (
	askFormPrefix    = "ask_text_submit_"
	noticeFormPrefix = "handling_note_submit_"
)

// ParseEvent accepts only card clicks; everything else on the bus is
// another feature's traffic.
func ParseEvent(line []byte) (Event, bool) {
	if len(strings.TrimSpace(string(line))) == 0 {
		return Event{}, false
	}
	var ev Event
	if err := json.Unmarshal(line, &ev); err != nil {
		return Event{}, false
	}
	if ev.Type != "card.action.trigger" {
		return Event{}, false
	}
	return ev, true
}

// ParseAction turns an event into an answer. It reports false for anything
// that is not a complete answer: unknown cards, empty text, unparseable
// values. Feishu sends action_value as a JSON string; some clients send an
// object, so both are accepted.
func (e Event) ParseAction() (Action, bool) {
	act := Action{MessageID: e.MessageID, OperatorID: e.OperatorID, ChatID: e.ChatID}

	if id, ok := strings.CutPrefix(e.ActionName, askFormPrefix); ok {
		text := formNote(e.FormValue)
		if strings.TrimSpace(text) == "" {
			return Action{}, false
		}
		act.CardID, act.Text = id, text
		act.Fields = formFields(e.FormValue)
		return act, true
	}
	if id, ok := strings.CutPrefix(e.ActionName, noticeFormPrefix); ok {
		act.CardID, act.Text, act.Choice = id, formNote(e.FormValue), 0
		act.Fields = formFields(e.FormValue)
		return act, true
	}

	value := anyValue(e.ActionValue)
	if value == nil {
		return Action{}, false
	}
	act.Fields = anyStringFields(value["fields"])
	act.CardID = anyString(value["card"])
	if act.CardID == "" {
		return Action{}, false
	}
	choice, ok := anyInt(value["choice"])
	if !ok {
		return Action{}, false
	}
	act.Choice = choice
	return act, true
}

// anyValue decodes a field that Feishu sends as a JSON string, and that some
// clients send as an object. Either way the caller gets a map.
func anyValue(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		if strings.TrimSpace(asString) == "" {
			return nil
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(asString), &out); err != nil {
			return nil
		}
		return out
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

func formNote(raw json.RawMessage) string {
	value := anyValue(raw)
	if value == nil {
		return ""
	}
	text, _ := value["note"].(string)
	return text
}

// formFields keeps the editable values a form submitted besides the answer
// text. Feishu sends each named input at the top level ({"note":"…",
// "task_no":"#42"}); a nested "fields" object is accepted too, because the
// button path carries its values that way.
func formFields(raw json.RawMessage) map[string]string {
	value := anyValue(raw)
	if value == nil {
		return nil
	}
	out := anyStringFields(value["fields"])
	for k, v := range value {
		if k == "note" || k == "fields" {
			continue
		}
		if s := anyString(v); s != "" {
			if out == nil {
				out = map[string]string{}
			}
			out[k] = s
		}
	}
	return out
}

// anyStringFields decodes a fields object into a flat string map. Numbers keep
// their text form so a task number typed into a form round-trips. Missing,
// empty, or non-object input is nil — callers read it like a nil map.
func anyStringFields(v any) map[string]string {
	m, ok := v.(map[string]any)
	if !ok || len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, raw := range m {
		if s := anyString(raw); s != "" {
			out[k] = s
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func anyString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.Itoa(int(x))
	case json.Number:
		return x.String()
	default:
		return ""
	}
}

func anyInt(v any) (int, bool) {
	switch x := v.(type) {
	case float64:
		return int(x), true
	case json.Number:
		n, err := x.Int64()
		return int(n), err == nil
	case string:
		n, err := strconv.Atoi(x)
		return n, err == nil
	default:
		return 0, false
	}
}
