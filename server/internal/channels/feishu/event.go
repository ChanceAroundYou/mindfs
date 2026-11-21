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
		return act, true
	}
	if id, ok := strings.CutPrefix(e.ActionName, noticeFormPrefix); ok {
		act.CardID, act.Text, act.Choice = id, formNote(e.FormValue), 0
		return act, true
	}

	value := anyValue(e.ActionValue)
	if value == nil {
		return Action{}, false
	}
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
