package api

import (
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
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
	writeJSONWithETag(w, r, body)
}

// respondJSONConditional 只做 ETag/304 协商，**不动载荷**。
//
// 给「载荷本身就是详情、少一个键就改语义」的端点用：git status 的 `dirty_count: 0`、
// agents 的空数组、tasks 的 stages/events 都是承重信息，套 respondJSONList 会被删掉。
// 这些端点内容变化不频繁、调用却很密（/api/agents 59KB × 98 次/h、
// /api/task-templates 24.8KB × 161 次/h、/api/tree 7.7KB × 161 次/h、
// /api/replying-sessions 2.9KB × 508 次/h），协商后内容没变时是**零字节**，
// 手机端收益最大。请求次数不变，服务端算 ETag 的成本是一次 sha1。
func respondJSONConditional(w http.ResponseWriter, r *http.Request, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSONWithETag(w, r, body)
}

// writeJSONWithETag 按响应体算弱 ETag（`W/"<sha1>"`）；If-None-Match 命中回 304 + 无正文。
func writeJSONWithETag(w http.ResponseWriter, r *http.Request, body []byte) {
	etag := fmt.Sprintf("W/\"%x\"", sha1.Sum(body))
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

// stripEmptyJSONValues 返回删掉空值字段的**副本**。
// map 键序由 encoding/json 统一按字典序输出，所以同样的内容永远编出同样的字节 ——
// 这是 ETag 能稳定的前提。
//
// 不修改入参：早先的写法原地删键，于是「调用方必须传本请求现造的结构」成了一条
// 只写在注释里的契约 —— 谁传了共享缓存，那些键就会在下次响应里永久消失。
// 改成返回副本后这条契约不存在了（代价是每个请求多一次 map 分配，与紧随其后的
// json.Marshal 同量级）。
func stripEmptyJSONValues(node any) any {
	switch typed := node.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, value := range typed {
			if isBlankJSONValue(value) {
				continue
			}
			out[key] = stripEmptyJSONValues(value)
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, stripEmptyJSONValues(item))
		}
		return out
	}
	// 具名容器（`[]map[string]any`、`map[string]map[string]any`…）匹配不到上面的类型开关，
	// 早先会掉进 default 原样返回 —— 于是**整个 items 数组一个键都没省**，
	// 而响应状态、ETag、304 全都正常，属于「静默无效」：改完看不出任何区别，
	// 只有量一下字节数才发现没生效（2026-10-07 实测：会话列表 19 条仍是 21 键/条）。
	//
	// 这里按反射统一处理同构容器，避免再靠「恰好是哪种切片」来决定是否生效。
	// 判空仍然只认 isBlankJSONValue 那几条规则，`0` 照旧保留。
	value := reflect.ValueOf(node)
	switch value.Kind() {
	case reflect.Slice:
		if value.Type().Elem().Kind() == reflect.Uint8 {
			return node // []byte 是标量，不是容器
		}
		out := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		for i := 0; i < value.Len(); i++ {
			if stripped := reflect.ValueOf(stripEmptyJSONValues(value.Index(i).Interface())); stripped.IsValid() {
				out.Index(i).Set(stripped)
			}
		}
		return out.Interface()
	case reflect.Map:
		if value.Type().Key().Kind() != reflect.String {
			return node
		}
		out := reflect.MakeMapWithSize(value.Type(), value.Len())
		iter := value.MapRange()
		for iter.Next() {
			item := iter.Value().Interface()
			if isBlankJSONValue(item) {
				continue
			}
			if stripped := reflect.ValueOf(stripEmptyJSONValues(item)); stripped.IsValid() {
				out.SetMapIndex(iter.Key(), stripped)
			}
		}
		return out.Interface()
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
