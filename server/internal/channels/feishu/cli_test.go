package feishu

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeLark writes a shell script that records its argv and stdin, then prints
// the given JSON. The CLI under test must never need the real lark-cli.
func fakeLark(t *testing.T, output string) (bin, argsPath, stdinPath string) {
	t.Helper()
	dir := t.TempDir()
	argsPath = filepath.Join(dir, "args.txt")
	stdinPath = filepath.Join(dir, "stdin.txt")
	bin = filepath.Join(dir, "lark-cli")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$@\" > " + argsPath + "\n" +
		"cat > " + stdinPath + "\n" +
		"cat <<'JSON'\n" + output + "\nJSON\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, argsPath, stdinPath
}

func readText(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestCLISendUsesFixedArgumentsAndReturnsMessageID(t *testing.T) {
	bin, argsPath, _ := fakeLark(t, `{"ok":true,"identity":"bot","data":{"message_id":"om_123"}}`)
	cli := &CLI{Path: bin, Timeout: time.Second}
	id, err := cli.SendInteractive(context.Background(), "ou_owner", `{"schema":"2.0"}`, "mindfs-card-gate-1")
	if err != nil || id != "om_123" {
		t.Fatalf("id=%q err=%v", id, err)
	}
	args := readText(t, argsPath)
	for _, want := range []string{"im\n+messages-send", "--user-id\nou_owner", "--as\nbot",
		"--msg-type\ninteractive", "--idempotency-key\nmindfs-card-gate-1", "--content\n{\"schema\":\"2.0\"}"} {
		if !strings.Contains(args, want) {
			t.Fatalf("args missing %q:\n%s", want, args)
		}
	}
}

func TestCLIPatchFeedsContentOnStdin(t *testing.T) {
	bin, argsPath, stdinPath := fakeLark(t, `{"ok":true,"data":{}}`)
	if err := (&CLI{Path: bin, Timeout: time.Second}).PatchInteractive(context.Background(), "om_1", `{"schema":"2.0"}`); err != nil {
		t.Fatal(err)
	}
	args := readText(t, argsPath)
	for _, want := range []string{"im\nmessages\npatch", "--message-id\nom_1", "--as\nbot", "--data\n-"} {
		if !strings.Contains(args, want) {
			t.Fatalf("args missing %q:\n%s", want, args)
		}
	}
	if got := readText(t, stdinPath); !strings.Contains(got, `"content":"{\"schema\":\"2.0\"}"`) {
		t.Fatalf("stdin = %s", got)
	}
}

func TestCLISurfacesFeishuErrorBodies(t *testing.T) {
	bin, _, _ := fakeLark(t, `{"ok":false,"error":{"type":"authorization","message":"no scope"}}`)
	err := (&CLI{Path: bin, Timeout: time.Second}).PatchInteractive(context.Background(), "om_1", `{}`)
	if err == nil || !strings.Contains(err.Error(), "authorization") || !strings.Contains(err.Error(), "no scope") {
		t.Fatalf("err = %v", err)
	}
}

func TestCLIRejectsNonJSONAndMissingBinary(t *testing.T) {
	bin, _, _ := fakeLark(t, `not json at all`)
	if _, err := (&CLI{Path: bin, Timeout: time.Second}).SendInteractive(context.Background(), "ou", "{}", "k"); err == nil {
		t.Fatal("non-JSON output accepted")
	}
	if _, err := (&CLI{Path: filepath.Join(t.TempDir(), "nope"), Timeout: time.Second}).SendInteractive(context.Background(), "ou", "{}", "k"); err == nil {
		t.Fatal("missing binary accepted")
	}
}

// lark-cli 在 JSON 之前打一行提示（版本提醒、登录态警告）是常态；只认整段
// stdout 会让每张卡片都被当成「不是 JSON」而永久重试。取最后一个 JSON 对象。
func TestCLIIgnoresLinesBeforeTheJSONReply(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "lark-cli")
	script := "#!/bin/sh\n" +
		"echo 'warning: a newer lark-cli is available'\n" +
		"cat <<'JSON'\n{\"ok\":true,\"data\":{\"message_id\":\"om_777\"}}\nJSON\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cli := &CLI{Path: bin, Timeout: 5 * time.Second}
	id, err := cli.SendInteractive(context.Background(), "ou_owner", "{}", "key")
	if err != nil {
		t.Fatalf("SendInteractive: %v", err)
	}
	if id != "om_777" {
		t.Fatalf("message id = %q", id)
	}
}
