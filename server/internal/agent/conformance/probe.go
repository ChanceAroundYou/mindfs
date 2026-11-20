package conformance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// 真机探针：对一个**隔离实例**（绝不是 7331）连真实 WS，让 claude 与 codex 各跑两轮
// —— 一轮正常问答、一轮中途取消 —— 把收到的东西折成 Shape 交给 Compare。
//
// 判定与采集分开（见 conformance.go）：这里只负责「把真实事件流变成观测」，一致与否
// 由纯函数判。这样判定逻辑可以离线测，采集逻辑只在有实例时才跑。

// HarnessSpec 描述怎么在实例上开一个该 harness 的会话。
type HarnessSpec struct {
	Name string // "claude" / "codex"：报告里用的名字，也是例外表的键
	Type string // 会话类型（session.type），通常是 "chat"
	// Agent 是 agents.json 里的 harness 名；Model 留空表示用实例的默认模型。
	Agent string
	Model string
}

// DefaultHarnesses 是探针默认覆盖的两个 harness。
func DefaultHarnesses() []HarnessSpec {
	return []HarnessSpec{
		{Name: "claude", Type: "chat", Agent: "claude"},
		{Name: "codex", Type: "chat", Agent: "codex"},
	}
}

// Options 是一次探针运行的输入。
type Options struct {
	BaseURL string // http://127.0.0.1:7436/mindfs（含实例路径前缀）
	RootID  string // 实例上已注册的项目 id
	User    string // 删除会话时带上的账户
	// Harnesses 为空表示 DefaultHarnesses()。
	Harnesses []HarnessSpec
	// TurnTimeout 是一轮正常问答的上限；CancelTimeout 是取消后收尾的上限。
	TurnTimeout   time.Duration
	CancelTimeout time.Duration
}

// Result 是一次探针运行的结果。某个 harness 不可用时进 Skipped，不算失败 —— 但调用
// 方必须把它打印出来，不能当成「通过」。
type Result struct {
	Shapes   []Shape
	Skipped  []string
	Failures []string
	Notes    []string
}

const (
	basicPrompt  = "只回一个词：OK。不要调用任何工具。"
	cancelPrompt = "请逐个列出 1 到 50 的整数，每行一个，不要省略。"
)

// Run 对配置里的每个 harness 跑 basic 与 cancel 两轮，返回形状与失败明细。
// 单个 harness 失败不影响其他 harness —— 一次运行要尽可能多地拿到证据。
func Run(ctx context.Context, opts Options) Result {
	if opts.TurnTimeout <= 0 {
		opts.TurnTimeout = 180 * time.Second
	}
	if opts.CancelTimeout <= 0 {
		opts.CancelTimeout = 45 * time.Second
	}
	harnesses := opts.Harnesses
	if len(harnesses) == 0 {
		harnesses = DefaultHarnesses()
	}
	var result Result
	for _, spec := range harnesses {
		shape, skip, failures, notes := runHarness(ctx, opts, spec)
		result.Skipped = append(result.Skipped, skip...)
		result.Failures = append(result.Failures, failures...)
		result.Notes = append(result.Notes, notes...)
		if len(failures) == 0 {
			result.Shapes = append(result.Shapes, shape)
		}
	}
	return result
}

func runHarness(ctx context.Context, opts Options, spec HarnessSpec) (Shape, []string, []string, []string) {
	var failures, notes []string
	shape := Shape{Harness: spec.Name}

	conn, err := dial(ctx, opts.BaseURL)
	if err != nil {
		return shape, []string{fmt.Sprintf("%s: %v", spec.Name, err)}, nil, nil
	}
	defer conn.Close()

	basic, key, skipped, err := driveTurn(ctx, conn, opts, spec, "basic", basicPrompt, false)
	if skipped != "" {
		return shape, []string{fmt.Sprintf("%s: skipped: harness unavailable（%s）", spec.Name, skipped)}, nil, nil
	}
	if err != nil {
		failures = append(failures, fmt.Sprintf("%s: basic 回合：%v", spec.Name, err))
	} else {
		shape.Turns = append(shape.Turns, basic)
	}
	if key != "" {
		if err := deleteSession(ctx, opts, key); err != nil {
			notes = append(notes, fmt.Sprintf("%s: 删除会话 %s 失败：%v", spec.Name, key, err))
		} else if len(shape.Turns) > 0 {
			shape.Turns[0].Frames = append(shape.Turns[0].Frames, "session.deleted")
		}
	}

	cancel, _, _, err := driveTurn(ctx, conn, opts, spec, "cancel", cancelPrompt, true)
	if err != nil {
		failures = append(failures, fmt.Sprintf("%s: cancel 回合：%v", spec.Name, err))
	} else {
		shape.Turns = append(shape.Turns, cancel)
	}
	return shape, nil, failures, notes
}

// driveTurn 发一条留言、收集到终止帧为止；cancel=true 时在收到第一条输出后立刻取消。
func driveTurn(ctx context.Context, conn *websocket.Conn, opts Options, spec HarnessSpec,
	name, prompt string, cancel bool) (Turn, string, string, error) {

	requestID := fmt.Sprintf("conformance-%s-%s-%d", spec.Name, name, time.Now().UnixNano())
	payload := map[string]any{
		"root_id": opts.RootID,
		"type":    spec.Type,
		"agent":   spec.Agent,
		"content": prompt,
	}
	if spec.Model != "" {
		payload["model"] = spec.Model
	}
	if err := conn.WriteJSON(map[string]any{"id": requestID, "type": "session.message", "payload": payload}); err != nil {
		return Turn{}, "", "", fmt.Errorf("发 session.message 失败：%w", err)
	}

	timeout := opts.TurnTimeout
	if cancel {
		timeout = opts.CancelTimeout
	}
	deadline := time.Now().Add(timeout)
	var observations []Observation
	var sessionKey string
	cancelled := false
	for time.Now().Before(deadline) {
		_ = conn.SetReadDeadline(deadline)
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return Turn{}, sessionKey, "", fmt.Errorf("读 WS 失败：%w", err)
		}
		var frame struct {
			ID      string         `json:"id"`
			Type    string         `json:"type"`
			Payload map[string]any `json:"payload"`
			Error   *struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(raw, &frame); err != nil {
			continue
		}
		key, _ := frame.Payload["session_key"].(string)
		if sessionKey == "" {
			sessionKey = key
		}
		if sessionKey != "" && key != "" && key != sessionKey {
			continue // 别的会话的广播（同一实例上的其他活动）
		}
		switch frame.Type {
		case "session.accepted":
			observations = append(observations, Observation{Frame: "session.accepted"})
		case "session.stream":
			for _, event := range streamEvents(frame.Payload) {
				if event.Type == "error" && harnessUnavailable(eventMessage(event.Data)) {
					return Turn{}, sessionKey, eventMessage(event.Data), nil
				}
				obs := Observation{Frame: "session.stream", Event: event.Type, Fields: flattenData(event.Data)}
				observations = append(observations, obs)
				if cancel && !cancelled && isOutputEvent(event.Type) {
					cancelled = true
					if err := conn.WriteJSON(map[string]any{
						"id":      requestID + "-cancel",
						"type":    "session.cancel",
						"payload": map[string]any{"root_id": opts.RootID, "session_key": sessionKey},
					}); err != nil {
						return Turn{}, sessionKey, "", fmt.Errorf("发 session.cancel 失败：%w", err)
					}
				}
			}
		case "session.done":
			observations = append(observations, Observation{Frame: "session.done"})
			return BuildTurn(name, observations), sessionKey, "", nil
		case "session.error":
			code := ""
			message := ""
			if frame.Error != nil {
				code = frame.Error.Code
				message = frame.Error.Message
			}
			verdict, detail := errorFrameVerdict(code, message, len(observations) > 0)
			switch verdict {
			case verdictSkip:
				return Turn{}, "", detail, nil
			case verdictFailure:
				return Turn{}, "", "", fmt.Errorf("会话没能建立：%s", detail)
			}
			observations = append(observations, Observation{Frame: "session.error"})
			return BuildTurn(name, observations), sessionKey, "", nil
		}
	}
	return Turn{}, sessionKey, "", fmt.Errorf("%s 内没有收到终止帧（超时 %s）", timeout, timeout)
}

func dial(ctx context.Context, baseURL string) (*websocket.Conn, error) {
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil {
		return nil, fmt.Errorf("解析实例地址失败：%w", err)
	}
	switch parsed.Scheme {
	case "http":
		parsed.Scheme = "ws"
	case "https":
		parsed.Scheme = "wss"
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/ws"
	clientID := fmt.Sprintf("conformance-%d", time.Now().UnixNano())
	query := parsed.Query()
	query.Set("client_id", clientID)
	parsed.RawQuery = query.Encode()
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, parsed.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("连 WS 失败：%w", err)
	}
	return conn, nil
}

func deleteSession(ctx context.Context, opts Options, key string) error {
	target := strings.TrimRight(opts.BaseURL, "/") + "/api/sessions/" + url.PathEscape(key)
	query := url.Values{}
	if opts.RootID != "" {
		query.Set("root", opts.RootID)
	}
	if opts.User != "" {
		query.Set("user", opts.User)
	}
	if encoded := query.Encode(); encoded != "" {
		target += "?" + encoded
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, target, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("DELETE 返回 %d", resp.StatusCode)
	}
	return nil
}

type streamEvent struct {
	Type string
	Data any
}

// streamEvents 展开 session.stream 的两种形状：单条 `event` 与批量 `events`。
func streamEvents(payload map[string]any) []streamEvent {
	var events []streamEvent
	appendOne := func(value any) {
		item, ok := value.(map[string]any)
		if !ok {
			return
		}
		eventType, _ := item["type"].(string)
		if eventType == "" {
			return
		}
		events = append(events, streamEvent{Type: eventType, Data: item["data"]})
	}
	if batch, ok := payload["events"].([]any); ok {
		for _, item := range batch {
			appendOne(item)
		}
	}
	appendOne(payload["event"])
	return events
}

func isOutputEvent(eventType string) bool {
	switch eventType {
	case "message_chunk", "thought_chunk", "tool_call":
		return true
	}
	return false
}

// errorVerdict 说明一条错误帧该怎么算。
type errorVerdict int

const (
	// verdictTerminal：回合已经开始跑了，错误只是它的收尾（取消后很常见）。
	verdictTerminal errorVerdict = iota
	// verdictSkip：实例明说这个 harness 不可用 —— 没跑，不算通过。
	verdictSkip
	// verdictFailure：会话根本没建起来。这是探针或实例配置的问题，必须报失败。
	verdictFailure
)

// harnessUnavailable 只认实例自己说的「不可用」。`create_failed: root not found`
// 之类不能算 —— 那会把一个坏掉的探针伪装成「没跑」。
func harnessUnavailable(message string) bool {
	lower := strings.ToLower(message)
	for _, marker := range []string{"unavailable", "not installed", "未安装", "不可用"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// errorFrameVerdict 判定一条 session.error 帧：不可用 → 跳过；已有输出 → 正常收尾；
// 还没有任何输出就报错 → 会话没建立，判失败。明细里带上实例给的原因，方便现场定位。
func errorFrameVerdict(code, message string, sawOutput bool) (errorVerdict, string) {
	detail := strings.TrimSpace(code)
	if reason := strings.TrimSpace(message); reason != "" {
		if detail != "" {
			detail += ": " + reason
		} else {
			detail = reason
		}
	}
	if harnessUnavailable(detail) {
		return verdictSkip, detail
	}
	if !sawOutput {
		return verdictFailure, detail
	}
	return verdictTerminal, detail
}

// eventMessage 取 stream 事件 data 里的 message 文本（错误事件的正文）。
func eventMessage(data any) string {
	object, ok := data.(map[string]any)
	if !ok {
		return ""
	}
	message, _ := object["message"].(string)
	return message
}

// flattenData 把事件的 data 折成字段路径（data + 最多两层），只记「有没有」，不记值。
func flattenData(value any) []string {
	fields := map[string]bool{}
	flattenInto("data", value, fields, 0)
	paths := make([]string, 0, len(fields))
	for path := range fields {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func flattenInto(prefix string, value any, out map[string]bool, depth int) {
	object, ok := value.(map[string]any)
	if !ok {
		return
	}
	for key, child := range object {
		path := prefix + "." + key
		out[path] = true
		if depth < 1 {
			flattenInto(path, child, out, depth+1)
		}
	}
}
