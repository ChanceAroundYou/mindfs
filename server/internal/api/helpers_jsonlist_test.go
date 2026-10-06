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

// respondJSONConditional 的守卫：**只协商、不瘦身**。
//
// 这些端点（git status / agents / tree / 看板 tasks / replying-sessions / task-templates）
// 的载荷本身就是详情：`dirty_count: 0`、空数组、`stages`/`events` 流水都是承重信息。
// 一旦有人「顺手统一」成 respondJSONList，丢的是真数据，而且前端防御式读取不会报错 ——
// 所以这里正面钉住「空值必须原样留着」。
func TestRespondJSONConditionalKeepsBlankFields(t *testing.T) {
	payload := map[string]any{
		"dirty_count": 0,
		"clean":       false,
		"entries":     []any{},
		"empty_name":  "",
		"stages":      []any{map[string]any{"prompt_template": "", "role": "agent"}},
	}

	rec := httptest.NewRecorder()
	respondJSONConditional(rec, httptest.NewRequest(http.MethodGet, "/api/git/status", nil), payload)

	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"dirty_count", "clean", "entries", "empty_name", "stages"} {
		if _, ok := got[key]; !ok {
			t.Fatalf("respondJSONConditional 绝不能删键，%q 消失了：%s", key, rec.Body.String())
		}
	}
	stages, _ := got["stages"].([]any)
	if len(stages) != 1 || stages[0].(map[string]any)["prompt_template"] != "" {
		t.Fatalf("嵌套的空串也必须原样保留：%s", rec.Body.String())
	}

	// 协商照样生效：同一载荷第二次带 ETag 请求回 304。
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatalf("响应缺少 ETag")
	}
	req := httptest.NewRequest(http.MethodGet, "/api/git/status", nil)
	req.Header.Set("If-None-Match", etag)
	second := httptest.NewRecorder()
	respondJSONConditional(second, req, payload)
	if second.Code != http.StatusNotModified {
		t.Fatalf("状态 = %d，应为 304", second.Code)
	}
	if second.Body.Len() != 0 {
		t.Fatalf("304 不该带正文，实际 %d 字节", second.Body.Len())
	}
}

// stripEmptyJSONValues 必须**返回副本**，不得原地删键。
//
// 原地删键时，「调用方必须传本请求现造的结构」只是注释里的契约：谁把共享缓存传进来，
// 那些键就在下一次响应里永久消失 —— 而且只在第二次请求才显现，最难查的那类 bug。
func TestStripEmptyJSONValuesDoesNotMutateInput(t *testing.T) {
	shared := map[string]any{
		"keep":  "值",
		"empty": "",
		"list":  []any{map[string]any{"gone": "", "kept": 1}},
	}

	stripped, _ := stripEmptyJSONValues(shared).(map[string]any)
	if _, ok := stripped["empty"]; ok {
		t.Fatalf("副本里不该有空键")
	}

	// 原结构必须一字未动 —— 再剥一次仍然得到同样的结果。
	if _, ok := shared["empty"]; !ok {
		t.Fatalf("入参被原地修改了：空的键已被删除，共享缓存会永久缺这个键")
	}
	list, _ := shared["list"].([]any)
	if _, ok := list[0].(map[string]any)["gone"]; !ok {
		t.Fatalf("嵌套结构也被原地修改了")
	}
	again, _ := stripEmptyJSONValues(shared).(map[string]any)
	if len(again) != len(stripped) {
		t.Fatalf("第二次剥离结果不同：%v vs %v", again, stripped)
	}
}
