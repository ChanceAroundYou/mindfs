package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"
)

// CLI runs lark-cli. Every call is a child process with a fixed argv: no
// shell, no user input in the command line.
type CLI struct {
	Path    string
	Timeout time.Duration
}

type cliReply struct {
	OK   bool `json:"ok"`
	Data struct {
		MessageID string `json:"message_id"`
	} `json:"data"`
	Error *struct {
		Type       string `json:"type"`
		Message    string `json:"message"`
		ConsoleURL string `json:"console_url"`
		Hint       string `json:"hint"`
	} `json:"error"`
}

// SendInteractive sends a card to one user and returns its message ID. The
// idempotency key makes a retry after a crash return the same message.
func (c *CLI) SendInteractive(ctx context.Context, userID, content, idempotencyKey string) (string, error) {
	out, err := c.run(ctx, []string{
		"im", "+messages-send",
		"--user-id", userID,
		"--as", "bot",
		"--msg-type", "interactive",
		"--idempotency-key", idempotencyKey,
		"--content", content,
	}, "")
	if err != nil {
		return "", err
	}
	if out.Data.MessageID == "" {
		return "", errors.New("lark-cli 发送卡片成功但没有返回 message_id")
	}
	return out.Data.MessageID, nil
}

// PatchInteractive replaces a card's content in place. Feishu only lets the
// bot edit messages the bot sent, which every card here is.
func (c *CLI) PatchInteractive(ctx context.Context, messageID, content string) error {
	body, err := json.Marshal(map[string]string{"content": content})
	if err != nil {
		return err
	}
	_, err = c.run(ctx, []string{
		"im", "messages", "patch",
		"--message-id", messageID,
		"--as", "bot",
		"--data", "-",
	}, string(body))
	return err
}

func (c *CLI) run(ctx context.Context, args []string, stdin string) (cliReply, error) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, c.Path, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	where := "lark-cli " + strings.Join(args[:min(3, len(args))], " ")

	runErr := cmd.Run()
	printed := stdout.String()
	if runErr != nil && strings.TrimSpace(printed) == "" {
		printed = stderr.String()
	}
	var out cliReply
	parsed := json.Unmarshal([]byte(strings.TrimSpace(printed)), &out) == nil
	if parsed && (out.Error != nil || !out.OK) {
		if out.Error != nil {
			said := strings.TrimSpace(out.Error.Type)
			if msg := oneLine(out.Error.Message); msg != "" {
				said = strings.TrimPrefix(said+"："+msg, "：")
			}
			return out, fmt.Errorf("%s 失败：飞书说 %s", where, said)
		}
		return out, fmt.Errorf("%s 失败：飞书没有确认", where)
	}
	if runErr != nil {
		if msg := oneLine(strings.TrimSpace(printed)); msg != "" {
			return out, fmt.Errorf("%s 失败：%s", where, msg)
		}
		return out, fmt.Errorf("%s 失败：%w", where, runErr)
	}
	if !parsed {
		return out, fmt.Errorf("%s 失败：返回的不是 JSON（%s）", where, oneLine(strings.TrimSpace(printed)))
	}
	return out, nil
}

// oneLine collapses whitespace and cuts to 300 runes without splitting a
// character. Error bodies are JSON meant for programs; logs need one line.
func oneLine(text string) string {
	collapsed := strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(collapsed) <= 300 {
		return collapsed
	}
	runes := []rune(collapsed)
	return string(runes[:300]) + "…"
}
