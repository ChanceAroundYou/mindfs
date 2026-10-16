package api

import (
	"context"
	"log"
	"strconv"
	"strings"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/api/usecase"
	"mindfs/server/internal/session"

	"github.com/gorilla/websocket"
)

func (h *WSHandler) handleSessionSlashCommandRun(ctx context.Context, conn *websocket.Conn, clientID string, req WSRequest) {
	rootID := getString(req.Payload, "root_id")
	key := getString(req.Payload, "session_key")
	requestID := strings.TrimSpace(req.ID)
	agentName := getString(req.Payload, "agent")
	model := getString(req.Payload, "model")
	agentMode := getString(req.Payload, "agent_mode")
	effort := getString(req.Payload, "effort")
	fastService := normalizeFastServiceValue(getString(req.Payload, "fast_service"))
	command := strings.TrimSpace(getString(req.Payload, "command"))
	normalizedCommand := strings.TrimPrefix(strings.ToLower(command), "/")
	if rootID == "" || agentName == "" || normalizedCommand == "" {
		h.sendWSError(conn, clientID, req.ID, "invalid_request", "root_id, agent and command required")
		return
	}
	if key == "" {
		if requestID != "" {
			key = "transient-" + requestID
		} else {
			key = "transient-" + normalizedCommand + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
		}
	}
	if agentName != "codex" || (normalizedCommand != "status" && normalizedCommand != "login") {
		h.sendWSError(conn, clientID, req.ID, "unsupported_slash_command", "only codex /status and /login are supported")
		return
	}
	if requestID != "" {
		_, reserved := h.reserveClientRequest(requestID)
		if !reserved {
			h.sendWSAccepted(conn, clientID, requestID, rootID, key)
			return
		}
		h.sendWSAccepted(conn, clientID, requestID, rootID, key)
	}
	if h.AppContext != nil {
		h.AppContext.GetSessionStreamHub().BindSessionClient(key, clientID)
	}

	uc := &usecase.Service{Registry: h.AppContext}
	msgCtx, cancel := h.sessionMessageContext()
	defer cancel()
	updateTracker := newTurnUpdateTracker()
	err := uc.RunTransientSlashCommand(msgCtx, usecase.RunTransientSlashCommandInput{
		RootID:      rootID,
		Key:         key,
		Agent:       agentName,
		Model:       model,
		Mode:        agentMode,
		Effort:      effort,
		FastService: fastService,
		Command:     normalizedCommand,
		OnUpdate: func(update agenttypes.Event) {
			updateTracker.Begin()
			defer updateTracker.End()
			event := updateToEvent(update)
			if event == nil {
				return
			}
			_ = h.writeWSJSON(clientID, conn, buildSlashCommandStreamResponse(rootID, key, normalizedCommand, requestID, event))
		},
	})
	if err != nil {
		log.Printf("[ws] session.slash_command.error root=%s session=%s command=%s request=%s err=%v", rootID, key, normalizedCommand, requestID, err)
		event := &StreamEvent{
			Type: "error",
			Data: map[string]string{"message": normalizeAgentErrorMessage(err)},
		}
		_ = h.writeWSJSON(clientID, conn, buildSlashCommandStreamResponse(rootID, key, normalizedCommand, requestID, event))
	}
	if ok := updateTracker.WaitIdle(msgCtx, sessionDoneSettleWindow, sessionDoneMaxWait); !ok {
		log.Printf("[ws] session.slash_command.done.wait_timeout root=%s session=%s command=%s request=%s", rootID, key, normalizedCommand, requestID)
	}
	_ = h.writeWSJSON(clientID, conn, buildSlashCommandDoneResponse(rootID, key, normalizedCommand, requestID))
}

func (h *WSHandler) handleSessionPlanModeSet(ctx context.Context, conn *websocket.Conn, clientID string, req WSRequest) {
	rootID := getString(req.Payload, "root_id")
	key := getString(req.Payload, "session_key")
	requestID := strings.TrimSpace(req.ID)
	enabled := getBool(req.Payload, "enabled")
	if rootID == "" || key == "" {
		h.sendWSError(conn, clientID, req.ID, "invalid_request", "root_id and session_key required")
		return
	}
	manager, err := h.AppContext.GetSessionManager(rootID)
	if err != nil {
		h.sendWSError(conn, clientID, req.ID, "session.plan_mode_failed", err.Error())
		return
	}
	current, err := manager.Get(ctx, key, 0)
	if err != nil {
		h.sendWSError(conn, clientID, req.ID, "session.plan_mode_failed", err.Error())
		return
	}
	previous := current.PlanMode
	if err := manager.UpdatePlanMode(ctx, current, enabled); err != nil {
		h.sendWSError(conn, clientID, req.ID, "session.plan_mode_failed", err.Error())
		return
	}
	if previous != enabled {
		h.switchSessionRuntimePlanMode(ctx, key, current, enabled)
	}
	updated, err := manager.Get(ctx, key, 0)
	if err != nil {
		h.sendWSError(conn, clientID, req.ID, "session.plan_mode_failed", err.Error())
		return
	}
	h.sendWSAccepted(conn, clientID, requestID, rootID, key)
	h.broadcastSessionMetaUpdated(rootID, updated)
	_ = h.writeWSJSON(clientID, conn, buildSessionDoneResponse(rootID, key, requestID))
}

func wsAgentPoolSessionKey(sessionKey, agentName string) string {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return ""
	}
	agentName = strings.TrimSpace(agentName)
	if agentName == "" {
		return sessionKey
	}
	return strings.ToLower(agentName) + "-" + sessionKey
}

func (h *WSHandler) switchSessionRuntimePlanMode(ctx context.Context, key string, current *session.Session, enabled bool) {
	if h == nil || h.AppContext == nil || current == nil {
		return
	}
	pool := h.AppContext.GetAgentPool()
	if pool == nil {
		return
	}
	agentName := session.InferAgentFromSession(current)
	if runtime, ok := pool.Get(wsAgentPoolSessionKey(key, agentName)); ok {
		if err := runtime.SetPlanMode(ctx, enabled); err != nil {
			log.Printf("[session/plan] runtime.switch.error session=%s agent=%s plan_mode=%t err=%v", key, agentName, enabled, err)
		}
	}
}
