// Package feishu's relay support lets a worker hand a card to the control
// node, which owns the lark-cli connection. All identity comes from the
// configured node table; nothing in the request body can choose a target.
package feishu

import (
	"context"
	"errors"
	"strings"

	"mindfs/server/internal/nodes"
)

// RelayTokenHeader carries the shared relay secret. The value is never logged.
const RelayTokenHeader = "X-MindFS-Relay-Token"

var (
	// ErrRelayTokenInvalid covers missing, too-short or malformed tokens.
	ErrRelayTokenInvalid = errors.New("feishu: invalid relay token")
	// ErrRelayNodeInvalid covers a node that is empty, local, unconfigured, or
	// disagreeing with the card's own node id.
	ErrRelayNodeInvalid = errors.New("feishu: invalid relay node")
	// ErrRelayCardInvalid covers an incomplete notice.
	ErrRelayCardInvalid = errors.New("feishu: invalid relay card")
	// ErrRelayServiceUnavailable means this instance has no Feishu channel.
	ErrRelayServiceUnavailable = errors.New("feishu: relay service unavailable")
)

// ValidateRelayToken checks the configured (or presented) token's shape after
// trimming whitespace. Comparison itself belongs to the HTTP layer, which
// uses a constant-time compare.
func ValidateRelayToken(token string) error {
	if len(strings.TrimSpace(token)) < 32 {
		return ErrRelayTokenInvalid
	}
	return nil
}

// RelayRequest is the body a worker POSTs to the control node. There is
// deliberately no URL field: a caller cannot redirect a relayed card.
type RelayRequest struct {
	NodeID string `json:"node_id"`
	Notice Notice `json:"notice"`
}

// NodeLister is the slice of the configured node table the relay needs.
type NodeLister interface {
	List() []nodes.NodeConnection
}

// ValidateRelayRequest checks the target against configured nodes and the
// card's own identity before anything is written to the card store.
func ValidateRelayRequest(req RelayRequest, lister NodeLister) error {
	nodeID := strings.TrimSpace(req.NodeID)
	if nodeID == "" || nodeID == "local" || lister == nil {
		return ErrRelayNodeInvalid
	}
	known := false
	for _, node := range lister.List() {
		if strings.TrimSpace(node.ID) == nodeID {
			known = true
			break
		}
	}
	if !known || req.Notice.NodeID != nodeID {
		return ErrRelayNodeInvalid
	}
	if strings.TrimSpace(req.Notice.CardID) == "" || req.Notice.Kind == "" {
		return ErrRelayCardInvalid
	}
	return nil
}

// AddRelayNotice validates the envelope and records the card on the control
// node. Validation happens before Notify so a rejected relay cannot leave a
// card behind.
func AddRelayNotice(ctx context.Context, svc *Service, nodeID string, notice Notice, lister NodeLister) error {
	if svc == nil {
		return ErrRelayServiceUnavailable
	}
	if err := ValidateRelayRequest(RelayRequest{NodeID: nodeID, Notice: notice}, lister); err != nil {
		return err
	}
	return svc.Notify(ctx, notice)
}
