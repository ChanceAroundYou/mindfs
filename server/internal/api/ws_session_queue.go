package api

import (
	"context"
	"log"
	"strings"

	"mindfs/server/internal/api/usecase"

	"github.com/gorilla/websocket"
)

func (h *WSHandler) handleSessionQueueRemove(_ context.Context, conn *websocket.Conn, clientID string, req WSRequest) {
	rootID := getString(req.Payload, "root_id")
	key := getString(req.Payload, "session_key")
	queueID := strings.TrimSpace(getString(req.Payload, "queue_id"))
	if rootID == "" || key == "" || queueID == "" {
		h.sendWSError(conn, clientID, req.ID, "invalid_request", "root_id, session_key and queue_id required")
		return
	}
	streamHub := h.AppContext.GetSessionStreamHub()
	queue := streamHub.RemoveQueuedSessionMessage(key, queueID)
	streamHub.BroadcastSessionQueueUpdated(rootID, key, queue)
	if req.ID != "" {
		h.sendWSAccepted(conn, clientID, req.ID, rootID, key)
	}
}

func (h *WSHandler) handleSessionQueueUpdate(_ context.Context, conn *websocket.Conn, clientID string, req WSRequest) {
	rootID := getString(req.Payload, "root_id")
	key := getString(req.Payload, "session_key")
	queueID := strings.TrimSpace(getString(req.Payload, "queue_id"))
	content := getString(req.Payload, "content")
	if rootID == "" || key == "" || queueID == "" || strings.TrimSpace(content) == "" {
		h.sendWSError(conn, clientID, req.ID, "invalid_request", "root_id, session_key, queue_id and content required")
		return
	}
	streamHub := h.AppContext.GetSessionStreamHub()
	queue := streamHub.UpdateQueuedSessionMessage(key, queueID, content)
	streamHub.BroadcastSessionQueueUpdated(rootID, key, queue)
	if req.ID != "" {
		h.sendWSAccepted(conn, clientID, req.ID, rootID, key)
	}
}

func (h *WSHandler) handleSessionQueueSendNow(ctx context.Context, conn *websocket.Conn, clientID string, req WSRequest) {
	rootID := getString(req.Payload, "root_id")
	key := getString(req.Payload, "session_key")
	queueID := strings.TrimSpace(getString(req.Payload, "queue_id"))
	if rootID == "" || key == "" || queueID == "" {
		h.sendWSError(conn, clientID, req.ID, "invalid_request", "root_id, session_key and queue_id required")
		return
	}
	streamHub := h.AppContext.GetSessionStreamHub()
	streamHub.BindSessionClient(key, clientID)
	queue, ok := streamHub.PromoteQueuedSessionMessage(key, queueID)
	if !ok {
		h.sendWSError(conn, clientID, req.ID, "not_found", "queued message not found")
		return
	}
	streamHub.BroadcastSessionQueueUpdated(rootID, key, queue)
	if req.ID != "" {
		h.sendWSAccepted(conn, clientID, req.ID, rootID, key)
	}
	if !streamHub.IsSessionReplying(key) {
		h.startNextQueuedSessionMessage(rootID, key)
		return
	}
	uc := &usecase.Service{Registry: h.AppContext}
	if err := uc.CancelSessionTurn(ctx, usecase.CancelSessionTurnInput{RootID: rootID, Key: key}); err != nil {
		log.Printf("[ws] session.queue.send_now.cancel.error root=%s session=%s request=%s err=%v", rootID, key, req.ID, err)
	}
}
