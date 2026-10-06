package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// respondJSONList 的守卫：去空值的语义 + ETag/304 协商。
//
// 这一层直接决定响应体里**有没有**某些键，所以最容易出「悄悄变了个样子」的问题 ——
// 而前端对每个字段都是防御式读取，缺键不会报错、只会静默回退。
// 所以这里把去键与不去键的边界写死，而不是靠肉眼核对。
func TestRespondJSONListStripsBlankFieldsButKeepsZeros(t *testing.T) {
	rec := httptest.NewRecorder()
	respondJSONList(rec, httptest.NewRequest(http.MethodGet, "/x", nil), map[string]any{
		// 空值：必须被去掉
		"blank_string": "",
		"blank_null":   nil,
		"blank_false":  false,
		"blank_array":  []any{},
		"blank_object": map[string]any{},
		// 非空：必须留着
		"real_string":     "你好",
		"real_true":       true,
		"real_array":      []any{"a"},
		"real_object":     map[string]any{"k": "v"},
		"real_null_field": nil, // 见下：顶层与嵌套的 null 都要去
		// **0 不能被去掉**：total_count: 0 / dirty_count: 0 可能正是要表达的信息
		"total_count": 0,
		"dirty_count": 0,
		"nested": map[string]any{
			"empty": "",
			"keep":  "值",
			"zero":  0,
			"list":  []any{map[string]any{"drop": "", "keep": 1}, "", "x"},
		},
	})

	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	for _, key := range []string{"blank_string", "blank_null", "blank_false", "blank_array", "blank_object", "real_null_field"} {
		if _, ok := got[key]; ok {
			t.Fatalf("空字段 %q 不该出现在响应里：%s", key, rec.Body.String())
		}
	}
	for _, key := range []string{"real_string", "real_true", "real_array", "real_object"} {
		if _, ok := got[key]; !ok {
			t.Fatalf("非空字段 %q 被误删了：%s", key, rec.Body.String())
		}
	}
	if got["total_count"] != float64(0) || got["dirty_count"] != float64(0) {
		t.Fatalf("`0` 被当成空删掉了 —— 这类字段可能是承重的：%s", rec.Body.String())
	}
	nested, _ := got["nested"].(map[string]any)
	if _, ok := nested["empty"]; ok {
		t.Fatalf("嵌套的空字段没被去掉")
	}
	if _, ok := nested["zero"]; !ok {
		t.Fatalf("嵌套的 0 被误删了")
	}
	if nested["keep"] != "值" {
		t.Fatalf("嵌套的非空字段被误删了")
	}
	list, _ := nested["list"].([]any)
	// 数组元素**不删**：数组有位置语义，删掉第 i 个会让索引错位。
	// 这里只递归进去把嵌套对象里的空字段去掉。
	if len(list) != 3 {
		t.Fatalf("数组元素不该被删（位置语义）：%v", list)
	}
	if list[0].(map[string]any)["drop"] != nil {
		t.Fatalf("数组元素里的嵌套空字段该被去掉")
	}
}

func TestRespondJSONListETagAnd304(t *testing.T) {
	newReq := func(etag string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		rec := httptest.NewRecorder()
		respondJSONList(rec, req, map[string]any{
			"items":       []any{map[string]any{"key": "k1", "name": "n"}},
			"total_count": 1,
		})
		return rec
	}

	first := newReq("")
	if first.Code != http.StatusOK {
		t.Fatalf("首次请求状态 = %d，应为 200", first.Code)
	}
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatalf("响应缺少 ETag")
	}
	if ct := first.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("Content-Type = %q", ct)
	}

	// 同样的内容必须编出同样的 ETag，否则 304 永远命中不了。
	second := newReq("")
	if second.Header().Get("ETag") != etag {
		t.Fatalf("ETag 不稳定：%q vs %q", etag, second.Header().Get("ETag"))
	}

	// 带上 If-None-Match 且内容一致 → 304，且不带正文（省的就是这个）。
	match := newReq(etag)
	if match.Code != http.StatusNotModified {
		t.Fatalf("If-None-Match 命中时状态 = %d，应为 304", match.Code)
	}
	if strings.TrimSpace(match.Body.String()) != "" {
		t.Fatalf("304 不该带正文，实际有 %d 字节", match.Body.Len())
	}

	// ETag 不匹配 → 照常回 200 全量。
	mismatch := newReq(`W/"deadbeef"`)
	if mismatch.Code != http.StatusOK || mismatch.Body.Len() == 0 {
		t.Fatalf("If-None-Match 不匹配时应回 200 全量，实际 status=%d len=%d", mismatch.Code, mismatch.Body.Len())
	}
}
