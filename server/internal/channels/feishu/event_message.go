package feishu

import (
	"encoding/json"
	"strconv"
	"strings"
)

// InboundMessage is one owner chat message, reduced to what intake needs.
// Feishu sends a schema 2.0 envelope around the actual event; only the fields
// below are decoded so upstream additions cannot break the listener.
type InboundMessage struct {
	EventID      string
	MessageID    string
	ChatID       string
	ChatType     string
	SenderOpenID string
	Text         string
	CreateTime   int64
}

type inboundEnvelope struct {
	Header struct {
		EventID   string `json:"event_id"`
		EventType string `json:"event_type"`
	} `json:"header"`
	Event struct {
		Sender struct {
			SenderID struct {
				OpenID string `json:"open_id"`
			} `json:"sender_id"`
		} `json:"sender"`
		Message struct {
			MessageID   string          `json:"message_id"`
			ChatID      string          `json:"chat_id"`
			ChatType    string          `json:"chat_type"`
			MessageType string          `json:"message_type"`
			Content     string          `json:"content"`
			CreateTime  json.RawMessage `json:"create_time"`
		} `json:"message"`
	} `json:"event"`
}

// ParseMessage accepts a text message from a p2p chat. Owner, dedupe and
// project routing are policy decisions made by the intake service, not here.
func ParseMessage(line []byte) (InboundMessage, bool) {
	if len(strings.TrimSpace(string(line))) == 0 {
		return InboundMessage{}, false
	}
	var env inboundEnvelope
	if err := json.Unmarshal(line, &env); err != nil {
		return InboundMessage{}, false
	}
	m := env.Event.Message
	if env.Header.EventType != "im.message.receive_v1" ||
		// 群聊不在产品范围内；在解析口就挡掉，避免下游误把群消息建成任务。
		m.ChatType != "p2p" || m.MessageType != "text" ||
		m.MessageID == "" || m.ChatID == "" || env.Event.Sender.SenderID.OpenID == "" {
		return InboundMessage{}, false
	}
	var content struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(m.Content), &content); err != nil {
		return InboundMessage{}, false
	}
	text := strings.TrimSpace(content.Text)
	if text == "" {
		return InboundMessage{}, false
	}
	return InboundMessage{
		EventID:      env.Header.EventID,
		MessageID:    m.MessageID,
		ChatID:       m.ChatID,
		ChatType:     m.ChatType,
		SenderOpenID: env.Event.Sender.SenderID.OpenID,
		Text:         text,
		CreateTime:   parseInboundTime(m.CreateTime),
	}, true
}

// Feishu timestamps are milliseconds, sent as a JSON string or a number
// depending on the producer. Both shapes must give the same value.
func parseInboundTime(raw json.RawMessage) int64 {
	if len(raw) == 0 {
		return 0
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		n, err := strconv.ParseInt(asString, 10, 64)
		if err != nil {
			return 0
		}
		return n
	}
	var asNumber json.Number
	if err := json.Unmarshal(raw, &asNumber); err != nil {
		return 0
	}
	n, err := asNumber.Int64()
	if err != nil {
		return 0
	}
	return n
}
