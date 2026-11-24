package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"

	"mindfs/server/internal/api/usecase"
)

// handleLocalDirsCreate 承接「导入项目」路径浏览里的新建文件夹。
//
// 它只建目录、不注册项目：用户此刻在挑位置，注册仍由随后的 POST /api/dirs
// （加入本地目录）或空白项目 / GitHub 导入完成。
func (h *HTTPHandler) handleLocalDirsCreate(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	var req struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json body"))
		return
	}
	out, err := h.service().CreateLocalDir(r.Context(), usecase.CreateLocalDirInput{Path: req.Path, Name: req.Name})
	if err != nil {
		respondError(w, localDirsCreateStatus(err), err)
		return
	}
	respondJSON(w, http.StatusCreated, out)
}

// localDirsCreateStatus 把文件系统错误映射成前端能区分处理的状态码。
func localDirsCreateStatus(err error) int {
	switch {
	case errors.Is(err, os.ErrExist):
		return http.StatusConflict
	case errors.Is(err, os.ErrNotExist):
		return http.StatusNotFound
	case os.IsPermission(err):
		return http.StatusForbidden
	default:
		return http.StatusBadRequest
	}
}
