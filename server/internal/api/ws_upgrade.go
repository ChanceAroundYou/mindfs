package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"mindfs/server/internal/api/usecase"

	"github.com/gorilla/websocket"
)

const (
	wsPingInterval          = 30 * time.Second
	wsPongWait              = 2 * time.Minute
	wsCloseClientSuperseded = 4000

	sessionDoneSettleWindow = 50 * time.Millisecond
	sessionDoneMaxWait      = 2 * time.Second
)

var upgrader = websocket.Upgrader{}

// WSHandler manages JSON-RPC over WebSocket.
type WSHandler struct {
	AppContext      *AppContext
	fileOnce        sync.Once
	relatedFileOnce sync.Once
	proberOnce      sync.Once
	updateOnce      sync.Once
	githubOnce      sync.Once
	requestMu       sync.Mutex
	requests        map[string]time.Time
}

type StreamEvent struct {
	Type      string    `json:"type"`
	Data      any       `json:"data,omitempty"`
	Timestamp time.Time `json:"timestamp,omitempty"`
	MindFSID  string    `json:"mindfs_id,omitempty"`
	// EventCursor 是**服务端内部**编号（"<baseExchangeSeq>:<eventSeq>"），只用来给
	// ReplyingList 排序、以及在排空循环里判断「哪些事件还没发给这个客户端」。
	// 客户端既不读也不回传 —— 挂载会话是快照重建，不是按序号续传（见 ReplayPending）。
	EventCursor string `json:"event_cursor,omitempty"`
}

// ServeHTTP upgrades the connection and processes JSON-RPC messages.
func (h *WSHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.fileOnce.Do(func() {
		if h.AppContext != nil {
			h.AppContext.AddFileChangeListener(h.broadcastFileChange)
			h.AppContext.AddFileChangeBatchListener(h.broadcastFileChangeBatch)
		}
	})
	h.relatedFileOnce.Do(func() {
		if h.AppContext != nil {
			h.AppContext.AddRelatedFileListener(h.broadcastRelatedFileChange)
		}
	})
	h.proberOnce.Do(func() {
		if h.AppContext != nil && h.AppContext.GetProber() != nil {
			h.AppContext.GetProber().AddListener(h.broadcastAgentStatusChange)
		}
	})
	h.updateOnce.Do(func() {
		if h.AppContext != nil && h.AppContext.GetUpdateService() != nil {
			h.AppContext.GetUpdateService().AddListener(h.broadcastAppUpdate)
		}
	})
	h.githubOnce.Do(func() {
		if h.AppContext != nil && h.AppContext.GetGitHubImportService() != nil {
			h.AppContext.GetGitHubImportService().AddListener(h.broadcastGitHubImport)
		}
	})
	clientID := strings.TrimSpace(r.URL.Query().Get("client_id"))
	if clientID == "" {
		http.Error(w, "client_id required", http.StatusBadRequest)
		return
	}
	nodeID := strings.TrimSpace(r.URL.Query().Get("node_id"))
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	log.Printf("[ws] connected client=%s remote=%s path=%s node=%s", clientID, r.RemoteAddr, r.URL.Path, nodeID)
	if h.AppContext != nil {
		var previous *websocket.Conn
		if nodeID != "" {
			previous = h.AppContext.GetSessionStreamHub().RegisterClientWithNode(clientID, conn, nodeID)
		} else {
			previous = h.AppContext.GetSessionStreamHub().RegisterClient(clientID, conn)
		}
		if previous != nil {
			log.Printf("[ws] superseded client=%s remote=%s path=%s", clientID, r.RemoteAddr, r.URL.Path)
			_ = previous.WriteControl(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(wsCloseClientSuperseded, "client_superseded"),
				time.Now().Add(5*time.Second),
			)
			_ = previous.Close()
		}
		h.pushInitialAppUpdate(clientID)
		h.pushInitialGitHubImports(clientID)
	}
	defer func() {
		if h.AppContext != nil {
			h.AppContext.GetSessionStreamHub().UnregisterClient(clientID, conn)
		}
		log.Printf("[ws] disconnected client=%s remote=%s path=%s node=%s", clientID, r.RemoteAddr, r.URL.Path, nodeID)
		conn.Close()
	}()

	conn.SetReadDeadline(time.Now().Add(wsPongWait))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(wsPongWait))
		return nil
	})

	done := make(chan struct{})
	defer close(done)

	go func() {
		ticker := time.NewTicker(wsPingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
					_ = conn.Close()
					return
				}
			case <-done:
				return
			}
		}
	}()

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if closeErr, ok := err.(*websocket.CloseError); ok {
				log.Printf("[ws] read.closed client=%s remote=%s path=%s code=%d text=%q", clientID, r.RemoteAddr, r.URL.Path, closeErr.Code, closeErr.Text)
			} else {
				log.Printf("[ws] read.error client=%s remote=%s path=%s err=%v", clientID, r.RemoteAddr, r.URL.Path, err)
			}
			return
		}
		var req WSRequest
		if err := json.Unmarshal(message, &req); err != nil {
			h.sendWSError(conn, clientID, "", "invalid_request", "invalid request")
			continue
		}
		h.handleWSRequest(r.Context(), conn, clientID, req)
	}
}

func cloneQuery(values url.Values) url.Values {
	next := make(url.Values, len(values))
	for key, value := range values {
		next[key] = append([]string(nil), value...)
	}
	return next
}

func (h *WSHandler) handleWSRequest(ctx context.Context, conn *websocket.Conn, clientID string, req WSRequest) {
	switch req.Type {
	case "ping":
		go h.handleWSPing(conn, clientID, req)
	case "session.message":
		go h.handleSessionMessage(ctx, conn, clientID, req)
	case "session.slash_command.run":
		go h.handleSessionSlashCommandRun(ctx, conn, clientID, req)
	case "session.plan_mode.set":
		go h.handleSessionPlanModeSet(ctx, conn, clientID, req)
	case "session.answer_question":
		go h.handleSessionAnswerQuestion(ctx, conn, clientID, req)
	case "session.ready":
		go h.handleSessionReady(clientID, req)
	case "session.cancel":
		// 异步处理：中断可能阻塞等待 CLI 确认（无超时），同步派发会卡死整个
		// 连接读循环，使后续 stop/消息都无法送达，表现为"停止按钮有时没反应"。
		//
		// 但「取消谁」必须在读循环里、此刻同步取证：异步落地可能晚几百毫秒，
		// 那时用户可能已经发了下一条消息、新一轮已经起来，按「当前活跃轮」去掐
		// 就会误伤（2026-10-02 dsh 空转 733ms 就是这么来的）。这里只取 id，
		// 真正的 Interrupt 仍在 goroutine 里做。
		targetTurnID := usecase.ActiveTurnID(
			getString(req.Payload, "root_id"),
			getString(req.Payload, "session_key"),
		)
		go h.handleSessionCancel(ctx, conn, clientID, req, targetTurnID)
	case "session.queue.remove":
		go h.handleSessionQueueRemove(ctx, conn, clientID, req)
	case "session.queue.update":
		go h.handleSessionQueueUpdate(ctx, conn, clientID, req)
	case "session.queue.send_now":
		go h.handleSessionQueueSendNow(ctx, conn, clientID, req)
	default:
		h.sendWSError(conn, clientID, req.ID, "method_not_found", "method not found")
	}
}

func (h *WSHandler) handleWSPing(conn *websocket.Conn, clientID string, req WSRequest) {
	resp := WSResponse{
		ID:      req.ID,
		Type:    "pong",
		Payload: map[string]any{"ts": time.Now().UTC().Format(time.RFC3339Nano)},
	}
	_ = h.writeWSJSON(clientID, conn, resp)
}

func (h *WSHandler) writeWSJSON(clientID string, conn *websocket.Conn, resp WSResponse) error {
	if h.AppContext != nil {
		return h.AppContext.GetSessionStreamHub().WriteJSON(clientID, conn, resp)
	}
	return conn.WriteJSON(resp)
}
