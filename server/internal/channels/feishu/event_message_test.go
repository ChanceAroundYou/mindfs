package feishu

import (
	"os"
	"strings"
	"testing"
)

func TestParseMessageAcceptsP2PText(t *testing.T) {
	line, err := os.ReadFile("testdata/message_p2p.json")
	if err != nil {
		t.Fatal(err)
	}
	msg, ok := ParseMessage(line)
	if !ok {
		t.Fatal("p2p text message was rejected")
	}
	if msg.EventID != "ei_im_1" || msg.MessageID != "om_im_1" || msg.ChatID != "oc_p2p_1" ||
		msg.ChatType != "p2p" || msg.SenderOpenID != "ou_owner" ||
		msg.Text != "修一下 #proj/main.go" || msg.CreateTime != 1763712000000 {
		t.Fatalf("message = %+v", msg)
	}
}

func TestParseMessageRejectsNonTextAndGroup(t *testing.T) {
	line, err := os.ReadFile("testdata/message_p2p.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, from, to string
	}{
		{"image", `"message_type":"text"`, `"message_type":"image"`},
		{"group", `"chat_type":"p2p"`, `"chat_type":"group"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mutated := strings.Replace(string(line), tc.from, tc.to, 1)
			if mutated == string(line) {
				t.Fatalf("fixture does not contain %s", tc.from)
			}
			if _, ok := ParseMessage([]byte(mutated)); ok {
				t.Fatalf("%s message was accepted", tc.name)
			}
		})
	}
}

func TestParseEventStillIgnoresInboundMessage(t *testing.T) {
	line, err := os.ReadFile("testdata/message_p2p.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ParseEvent(line); ok {
		t.Fatal("inbound message must not be mistaken for a card click")
	}
}
