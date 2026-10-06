package api

import (
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"mindfs/server/internal/apperr"
)

func respondJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// respondJSONList 回应**列表类**端点：省略空字段 + ETag/304 协商。
//
// 两件事都只为省流量，而且都只对列表端点成立：
//
// ① 省略空字段（nil / "" / false / 空数组 / 空对象）。
//
// 实测会话列表 149KB 里有 **42.7%** 是恒空或恒 false 的字段（archived_at / closed_at /
// pinned_at / plan_mode / worktree_missing / effort / fast_service / mode / shell /
// source，实测一个非空的都没有）。
// 前端 toSessionItem 对每个字段都是 `typeof x === "…"` 的防御式读取 ——
// 缺键即回退，所以省掉空键不改变任何语义，只改体积。
//
// 刻意**不**省 `0`：`0` 在别的上下文里可能是承重信息（`total_count: 0`、
// `dirty_count: 0`），而列表里没有任何必须靠 `0` 表达的字段 —— 为省那点体积
// 引入这一类风险不值当。
//
// **只能用在列表类端点**：单条详情、错误响应、动作响应都不能套 ——
// 那里 `false` / `0` 可能正是要表达的信息。
//
// ② ETag + 304。内容没变时回 304，客户端直接复用本地那份 ——
// 手机端最省的传输是零字节。ETag 算在**瘦身之后**的字节上，
// 这样「内容没变」的判据与客户端实际缓存的那份一致。
//
// 前端要配合：`services/api.ts` 侧带上 `If-None-Match`，并在 304 时复用上次响应体。
// 没带请求头的旧标签页照旧收到完整响应，所以这条是**纯增量**的。
func respondJSONList(w http.ResponseWriter, r *http.Request, v any) {
	body, err := json.Marshal(stripEmptyJSONValues(v))
	if err != nil {
		respondError(w, http.StatusInternalServerError, err)
		return
	}
	sum := sha1.Sum(body)
	etag := fmt.Sprintf("W/\"%x\"", sum)
	w.Header().Set("ETag", etag)

	if match := strings.TrimSpace(r.Header.Get("If-None-Match")); match != "" {
		for _, candidate := range strings.Split(match, ",") {
			if strings.TrimSpace(candidate) == etag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(body)
}

// stripEmptyJSONValues 递归删掉空值字段。
// map 键序由 encoding/json 统一按字典序输出，所以同样的内容永远编出同样的字节 ——
// 这是 ETag 能稳定的前提。
//
// **原地修改**：调用方必须传「本请求现造」的结构，不能传共享缓存 —— 被删掉的键
// 在下次响应里也不会回来。当前两个调用点（会话列表、任务总览）都是按请求新建的。
func stripEmptyJSONValues(node any) any {
	switch typed := node.(type) {
	case map[string]any:
		for key, value := range typed {
			if isBlankJSONValue(value) {
				delete(typed, key)
				continue
			}
			typed[key] = stripEmptyJSONValues(value)
		}
		return typed
	case []any:
		for i, item := range typed {
			typed[i] = stripEmptyJSONValues(item)
		}
		return typed
	default:
		return node
	}
}

// isBlankJSONValue 判空。**不含 0**，理由见 respondJSONList。
func isBlankJSONValue(v any) bool {
	switch typed := v.(type) {
	case nil:
		return true
	case string:
		return typed == ""
	case bool:
		return !typed
	case []any:
		return len(typed) == 0
	case map[string]any:
		return len(typed) == 0
	default:
		return false
	}
}

func respondError(w http.ResponseWriter, status int, err error) {
	payload := map[string]any{"error": err.Error()}
	if appErr, ok := apperr.Classify(err); ok {
		payload["code"] = appErr.Code
		payload["message"] = appErr.Message
		if appErr.Op != "" {
			payload["operation"] = appErr.Op
		}
		if appErr.Path != "" {
			payload["path"] = appErr.Path
		}
		if appErr.Detail != "" {
			payload["detail"] = appErr.Detail
		}
	}
	respondJSON(w, status, payload)
}

func errInvalidRequest(message string) error {
	return errors.New(message)
}

func errServiceUnavailable(message string) error {
	return errors.New(message)
}
