package conformance

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// 真机探针的入口。默认跳过：它需要一个**隔离实例**（独立 root + 端口），不能对着
// 本机 7331 跑 —— 那会真的开两个会话、真的花 token。
//
//	MINDFS_CONFORMANCE_URL   http://127.0.0.1:7436/mindfs
//	MINDFS_CONFORMANCE_ROOT  实例上已注册的项目 id
//	MINDFS_CONFORMANCE_USER  账户 id（删除会话时带上）
//	MINDFS_CONFORMANCE_HARNESSES 可选，默认 claude,codex
//
// 缺 harness 时输出「skipped: harness unavailable」，测试以 skip 结束 —— 不算通过，
// 因为「没跑」与「一致」是两件事。
func TestLiveHarnessConformance(t *testing.T) {
	base := strings.TrimSpace(os.Getenv("MINDFS_CONFORMANCE_URL"))
	if base == "" {
		t.Skip("skipped: harness unavailable（未设置 MINDFS_CONFORMANCE_URL）")
	}
	opts := Options{
		BaseURL: base,
		RootID:  strings.TrimSpace(os.Getenv("MINDFS_CONFORMANCE_ROOT")),
		User:    strings.TrimSpace(os.Getenv("MINDFS_CONFORMANCE_USER")),
	}
	if raw := strings.TrimSpace(os.Getenv("MINDFS_CONFORMANCE_HARNESSES")); raw != "" {
		for _, name := range strings.Split(raw, ",") {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			opts.Harnesses = append(opts.Harnesses, HarnessSpec{Name: name, Type: "chat", Agent: name})
		}
	}
	if raw := strings.TrimSpace(os.Getenv("MINDFS_CONFORMANCE_TURN_TIMEOUT_SECONDS")); raw != "" {
		if seconds, err := strconv.Atoi(raw); err == nil && seconds > 0 {
			opts.TurnTimeout = time.Duration(seconds) * time.Second
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	result := Run(ctx, opts)

	for _, note := range result.Notes {
		t.Log(note)
	}
	for _, skipped := range result.Skipped {
		t.Log(skipped)
	}
	for _, shape := range result.Shapes {
		encoded, _ := json.MarshalIndent(shape, "", "  ")
		t.Logf("shape %s:\n%s", shape.Harness, encoded)
	}
	for _, failure := range result.Failures {
		t.Error(failure)
	}
	issues := Compare(result.Shapes...)
	for _, issue := range issues {
		t.Error(issue)
	}
	if len(result.Skipped) > 0 {
		// 有 harness 没跑：本次运行不能算「一致」，只是现有证据没问题。
		t.Skipf("skipped: harness unavailable（%s）", strings.Join(result.Skipped, "; "))
	}
	if len(result.Shapes) < len(opts.harnessesOrDefault()) {
		t.Fatalf("只采集到 %d 个形状，预期 %d 个", len(result.Shapes), len(opts.harnessesOrDefault()))
	}
}

func (o Options) harnessesOrDefault() []HarnessSpec {
	if len(o.Harnesses) == 0 {
		return DefaultHarnesses()
	}
	return o.Harnesses
}
