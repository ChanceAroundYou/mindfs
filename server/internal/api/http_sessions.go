package api

import (
	"encoding/json"
	"io"
	"net/http"

	"mindfs/server/internal/api/usecase"
	"mindfs/server/internal/session"
)

func (h *HTTPHandler) handlePromptSave(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&input); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid prompt payload"))
		return
	}
	out, err := h.service().SavePrompt(r.Context(), usecase.SavePromptInput{
		Text: input.Text,
	})
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSON(w, http.StatusOK, out)
}

func (h *HTTPHandler) handlePromptDelete(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&input); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid prompt payload"))
		return
	}
	out, err := h.service().DeletePrompt(r.Context(), usecase.DeletePromptInput{
		Text: input.Text,
	})
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSON(w, http.StatusOK, out)
}

func (h *HTTPHandler) sessionListResponse(s *session.Session) map[string]any {
	item := sessionListResponse(s, h.resolveCommandShell())
	// 「有个 ask_user 提问还没回答」随列表项下发（只在为真时出现）。
	//
	// 刻意**不**走 /api/replying-sessions：那条通道喂的是 pending 真相表，进去就等于
	// 点亮实心灯，而这里要表达的恰好是"不在跑、在等你"。列表项是"这条会话现在什么样"
	// 的载体，跟着它走，前端不必再为这一个布尔值开第二条状态通道。
	if s != nil && h.AppContext != nil && h.AppContext.GetSessionStreamHub().AwaitingAnswer(s.Key) {
		item["awaiting_answer"] = true
	}
	return item
}
