package claude

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// 复现 2026-11-27 事故的 CLI 形态：子进程拿到 initialize 请求后直接退出，
// 既不回 control response 也不让 SDK 的读循环报错 → Initialize 永久阻塞。
// 有上限后，OpenSession 必须在毫秒级返回错误而不是挂住。
func TestOpenSessionDoesNotHangWhenCLIExitsDuringInitialize(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub CLI uses /bin/sh")
	}
	prev := claudeInitTimeout
	claudeInitTimeout = 300 * time.Millisecond
	t.Cleanup(func() { claudeInitTimeout = prev })

	dir := t.TempDir()
	configDir := filepath.Join(dir, "claude-config")
	slug := filepath.Join(configDir, "projects", "-stub")
	if err := os.MkdirAll(slug, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	if err := os.WriteFile(filepath.Join(slug, sessionID+".jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stub := filepath.Join(dir, "claude-stub.sh")
	// 读走 initialize 请求后直接退出：写入成功、响应永不出现——事故的真实形态。
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nIFS= read -r line\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	_, err := NewRuntime().OpenSession(context.Background(), OpenOptions{
		AgentName:       "claude",
		SessionKey:      "stub-session",
		RootPath:        dir,
		Command:         stub,
		ConfigDir:       configDir,
		ResumeSessionID: sessionID,
	})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected initialization error from dead CLI")
	}
	if elapsed > 3*time.Second {
		t.Fatalf("OpenSession blocked for %s; init timeout not applied", elapsed)
	}
	// 下界证明走的是超时分支：stub 收下请求后不回答，若写管道失败会立刻返回。
	if elapsed < 200*time.Millisecond {
		t.Fatalf("OpenSession returned in %s (err=%v); stub never consumed initialize, timeout branch untested", elapsed, err)
	}
}

// 转录不存在时必须在起 CLI 之前就失败，交给上层改开新会话。
func TestOpenSessionRejectsMissingResumeTranscript(t *testing.T) {
	_, err := NewRuntime().OpenSession(context.Background(), OpenOptions{
		AgentName:       "claude",
		SessionKey:      "missing-session",
		RootPath:        t.TempDir(),
		ConfigDir:       t.TempDir(),
		ResumeSessionID: "99999999-0000-0000-0000-000000000000",
	})
	if err == nil || !errors.Is(err, ErrResumeTranscriptMissing) {
		t.Fatalf("err = %v, want ErrResumeTranscriptMissing", err)
	}
}
