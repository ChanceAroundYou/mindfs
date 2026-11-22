package feishu

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"mindfs/server/internal/nodes"
)

type relayTestNodes []string

func (n relayTestNodes) List() []nodes.NodeConnection {
	out := make([]nodes.NodeConnection, 0, len(n))
	for _, id := range n {
		out = append(out, nodes.NodeConnection{ID: id, Name: id, URL: "https://" + id + ".example"})
	}
	return out
}

func TestValidateRelayToken(t *testing.T) {
	valid := strings.Repeat("t", 32)
	cases := []struct {
		name  string
		token string
		ok    bool
	}{
		{"exact 32 bytes", valid, true},
		{"padded with whitespace", "  " + valid + "\n", true},
		{"31 bytes", valid[:31], false},
		{"empty", "", false},
		{"whitespace only", "   ", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateRelayToken(tc.token)
			if (err == nil) != tc.ok {
				t.Fatalf("ValidateRelayToken(%q) error = %v, ok = %v", tc.token, err, tc.ok)
			}
		})
	}
}

func TestValidateRelayRequestRejectsUnconfiguredNode(t *testing.T) {
	lister := relayTestNodes{"wsl", "fn"}
	valid := func(nodeID string) RelayRequest {
		return RelayRequest{NodeID: nodeID, Notice: Notice{CardID: "card-1", Kind: KindNotice, NodeID: nodeID}}
	}
	cases := []struct {
		name string
		req  RelayRequest
	}{
		{"empty node", valid("")},
		{"local node", valid("local")},
		{"unknown node", valid("attacker")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateRelayRequest(tc.req, lister); err == nil {
				t.Fatalf("ValidateRelayRequest(%+v) accepted an unconfigured node", tc.req)
			}
		})
	}

	mismatch := valid("wsl")
	mismatch.Notice.NodeID = "fn"
	if err := ValidateRelayRequest(mismatch, lister); err == nil {
		t.Fatal("notice node_id differing from envelope node_id must be rejected")
	}

	noCard := valid("wsl")
	noCard.Notice.CardID = ""
	if err := ValidateRelayRequest(noCard, lister); err == nil {
		t.Fatal("notice without a card id must be rejected")
	}
}

func TestValidateRelayRequestAcceptsConfiguredNode(t *testing.T) {
	req := RelayRequest{NodeID: "wsl", Notice: Notice{CardID: "card-1", Kind: KindGate, NodeID: "wsl"}}
	if err := ValidateRelayRequest(req, relayTestNodes{"wsl"}); err != nil {
		t.Fatalf("configured node rejected: %v", err)
	}
}

func TestAddRelayNoticeRejectsBeforeWritingCard(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "cards.json"))
	svc := New(Config{}, store, nil)
	notice := Notice{CardID: "card-remote", Kind: KindNotice, NodeID: "attacker"}
	if err := AddRelayNotice(context.Background(), svc, "attacker", notice, relayTestNodes{"wsl"}); err == nil {
		t.Fatal("AddRelayNotice accepted an unconfigured node")
	}
	if card, ok := svc.Store().Get("card-remote"); ok {
		t.Fatalf("rejected relay wrote card: %+v", card)
	}
}

func TestAddRelayNoticeWritesConfiguredNodeCard(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "cards.json"))
	svc := New(Config{}, store, nil)
	notice := Notice{CardID: "card-wsl", Kind: KindNotice, NodeID: "wsl", Text: "hello"}
	if err := AddRelayNotice(context.Background(), svc, "wsl", notice, relayTestNodes{"wsl"}); err != nil {
		t.Fatalf("AddRelayNotice: %v", err)
	}
	card, ok := svc.Store().Get("card-wsl")
	if !ok {
		t.Fatal("relayed card was not recorded")
	}
	if card.NodeID != "wsl" || card.Text != "hello" || card.Kind != KindNotice {
		t.Fatalf("relayed card = %+v", card)
	}
}
