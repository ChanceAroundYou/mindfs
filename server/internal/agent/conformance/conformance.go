// Package conformance 是跨 harness 一致性探针的判定层（spec §6.12）。
//
// 为什么需要它：MindFS 有两个真实在用的 agent 适配器（claude / codex），前端只认
// 归一化之后的事件流。适配器各自演进时，最容易出的事故不是「报错」，而是**悄悄少
// 报**：某个 harness 的 message_done 不再携带用量、会话结束时少发一个终止帧、取消
// 之后回合永远不收尾。这些在单元测试里都绿，因为它们只测各自的适配器；只有在同一套
// 探针下把两个 harness 的形状摆在一起看，缺的那一格才显形。
//
// 这个文件只做形状判定，不发请求：真机探针在 probe.go，由 probe_live_test.go 在
// 有隔离实例时驱动。判定与采集分开，是为了让「什么算一致」可以用纯数据测，不必每
// 次都起实例。
//
// 形状的定义刻意很窄：帧类型、归一化事件类型、以及 message_done 的字段路径。断言
// 的不是文本内容，而是「两者都该有的那一格有没有」。
package conformance

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// ProbedHarnesses 是本探针覆盖的 harness。新增 harness 必须同时在 Exceptions 里
// 登记（哪怕是空列表）—— 没有例外表意味着没人想过它和别家的差异在哪。
var ProbedHarnesses = []string{"claude", "codex"}

// Exceptions 是 harness 特有的、允许不一致的字段路径。
//
// 只允许出现**固有差异**：某个后端就是不提供这类遥测，或命名属于该后端自己的概念。
// 「本来该有但现在没有」不属于例外 —— 那正是探针要抓的东西。路径按前缀匹配，末尾
// 的 * 表示该前缀下全部字段。
var Exceptions = map[string][]string{
	"claude": {
		// Claude 的缓存遥测由上游按请求返回，Codex 侧没有等价物。
		"data.tokenUsage.cacheReadTokens",
		"data.tokenUsage.cacheWriteTokens",
	},
	"codex": {
		"data.tokenUsage.cacheReadTokens",
		"data.tokenUsage.cacheWriteTokens",
		// Codex 的 rollout 概念，Claude 侧没有对应字段。
		"data.threadId",
	},
}

// Observation 是采集层交回来的一条观测：收到了哪个帧，若是 stream 帧则带上事件
// 类型与该事件 data 里出现过的字段路径。
type Observation struct {
	Frame  string
	Event  string
	Fields []string
}

// Turn 是一个回合的可比对形状。
type Turn struct {
	Name   string
	Frames []string
	Events []string
	Fields map[string][]string
}

// Shape 是一个 harness 在一组回合里的形状。
type Shape struct {
	Harness string
	Turns   []Turn
}

// TurnContract 是一个回合至少必须出现的形状。Terminal 是「到这一组里任意一个」即
// 算收尾 —— 取消之后落 done 还是 error 是产品决定，但必须有一个，不能悬空。
type TurnContract struct {
	Frames   []string
	Events   []string
	Fields   map[string][]string
	Terminal []string
}

// Required 是共同面上的下限。只放两边都必须做到的事；某一侧做不到的叫缺陷，不叫
// 契约差异。
var Required = map[string]TurnContract{
	"basic": {
		Frames: []string{"session.accepted", "session.stream"},
		Events: []string{"message_chunk", "message_done"},
		Fields: map[string][]string{
			"message_done": {
				"data.contextWindow",
				"data.tokenUsage",
				"data.tokenUsage.inputTokens",
				"data.tokenUsage.outputTokens",
			},
		},
		Terminal: []string{"session.done"},
	},
	"cancel": {
		Frames:   []string{"session.accepted"},
		Terminal: []string{"session.done", "session.error"},
	},
}

// CompactEventContract 是「归一化 compact 事件」这一跨 harness 共同面的契约。
//
// 共同面只有一层：两个适配器都把各家 wire 上的压缩通知折叠成同一个
// types.EventTypeCompact 事件，载荷是 types.CompactNotice。wire 形态（Claude 的
// compact boundary；Codex 的 compacted item、unknown 与 typed contextCompaction）
// 属于适配器实现细节，不在本契约内，只由各适配器自己的用例钉住 —— 把它们写成跨
// harness 契约，正是 spec §6.12 曾经的错。
type CompactEventContract struct {
	// EventType 是归一化事件类型名，必须等于 types.EventTypeCompact。
	EventType string
	// PayloadFields 是两个适配器都必须填写的载荷字段（JSON 名）。只列两边今天
	// 都真正赋值的字段；只有单边有值的（如 post_tokens，Codex 恒为 0）不算共同面。
	PayloadFields []string
}

// CompactContract 是 compact 事件的跨 harness 契约，由各适配器的用例引用判定。
var CompactContract = CompactEventContract{
	EventType:     "compact_notice",
	PayloadFields: []string{"id", "status", "summary"},
}

// Matches 判定一条已归一化的 compact 事件是否符合契约：事件类型一致，且载荷是带齐
// PayloadFields 全部字段（按 JSON 名）的结构。值可以为空 —— 契约钉的是形状，不是
// 内容；某一次压缩没有摘要，不代表适配器少报了字段。
func (c CompactEventContract) Matches(eventType string, payload any) error {
	if eventType != c.EventType {
		return fmt.Errorf("compact event type = %q, want %q", eventType, c.EventType)
	}
	got := payloadFieldNames(payload)
	for _, want := range c.PayloadFields {
		if !got[want] {
			return fmt.Errorf("compact payload missing field %q (got %v)", want, sortedKeys(got))
		}
	}
	return nil
}

// payloadFieldNames 收出结构体载荷的 JSON 字段名。契约只关心「字段在不在」，不看值，
// 所以读结构体标签而不看序列化后的 JSON（omitempty 会把空值字段整个吞掉）。
func payloadFieldNames(payload any) map[string]bool {
	names := map[string]bool{}
	v := reflect.ValueOf(payload)
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return names
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return names
	}
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		if name := jsonFieldName(t.Field(i)); name != "" && name != "-" {
			names[name] = true
		}
	}
	return names
}

// jsonFieldName 取结构体字段的 JSON 名，规则与 encoding/json 对齐（取标签逗号前
// 部分，缺省用字段名）。本契约唯一的载荷 types.CompactNotice 没有内嵌字段，故不展开；
// 真出现内嵌时该改这里的判据，而不是在契约里塞一个假字段。
func jsonFieldName(field reflect.StructField) string {
	tag := field.Tag.Get("json")
	if tag == "-" {
		return "-"
	}
	if tag != "" {
		if idx := strings.IndexByte(tag, ','); idx >= 0 {
			tag = tag[:idx]
		}
		if tag != "" {
			return tag
		}
	}
	return field.Name
}

// BuildTurn 把采集到的观测折成一个形状：帧与事件去重后排序，字段按事件分组去重排序。
// 排序是契约的一部分 —— 不一致时的报错信息必须每次相同，否则没法贴进记录里比对。
func BuildTurn(name string, observations []Observation) Turn {
	turn := Turn{Name: name, Fields: map[string][]string{}}
	frames := map[string]bool{}
	events := map[string]bool{}
	fields := map[string]map[string]bool{}
	for _, obs := range observations {
		if obs.Frame != "" {
			frames[obs.Frame] = true
		}
		if obs.Event == "" {
			continue
		}
		events[obs.Event] = true
		if fields[obs.Event] == nil {
			fields[obs.Event] = map[string]bool{}
		}
		for _, field := range obs.Fields {
			fields[obs.Event][field] = true
		}
	}
	turn.Frames = sortedKeys(frames)
	turn.Events = sortedKeys(events)
	for event, set := range fields {
		turn.Fields[event] = sortedKeys(set)
	}
	return turn
}

// Compare 比对一组 harness 的形状，返回全部问题（空表示一致）。顺序稳定：先按回合名，
// 再按 Required 里的检查，最后按 harness 名。
func Compare(shapes ...Shape) []string {
	var issues []string
	byHarness := map[string]Shape{}
	for _, shape := range shapes {
		byHarness[shape.Harness] = shape
		if _, ok := Exceptions[shape.Harness]; !ok {
			issues = append(issues, fmt.Sprintf("%s: 没有登记例外表（就算是空的也要写一条）", shape.Harness))
		}
	}
	turnNames := sortedKeys(contractNames())
	for _, turnName := range turnNames {
		contract := Required[turnName]
		turns := map[string]Turn{}
		for _, harness := range sortedShapeNames(shapes) {
			turn, ok := findTurn(byHarness[harness], turnName)
			if !ok {
				issues = append(issues, fmt.Sprintf("%s: 回合 %q 没有采集到", harness, turnName))
				continue
			}
			turns[harness] = turn
			issues = append(issues, checkContract(harness, turnName, contract, turn)...)
		}
		issues = append(issues, compareHarnesses(turns)...)
	}
	return issues
}

func checkContract(harness, turnName string, contract TurnContract, turn Turn) []string {
	var issues []string
	frames := setOf(turn.Frames)
	for _, frame := range contract.Frames {
		if !frames[frame] {
			issues = append(issues, fmt.Sprintf("%s: 回合 %q 缺帧 %s", harness, turnName, frame))
		}
	}
	if len(contract.Terminal) > 0 && !anyOf(frames, contract.Terminal) {
		issues = append(issues, fmt.Sprintf("%s: 回合 %q 没有终止帧（%s 任一）",
			harness, turnName, strings.Join(contract.Terminal, " / ")))
	}
	events := setOf(turn.Events)
	for _, event := range contract.Events {
		if !events[event] {
			issues = append(issues, fmt.Sprintf("%s: 回合 %q 缺事件 %s", harness, turnName, event))
		}
	}
	fieldOrder := sortedKeys(fieldListNames(contract.Fields))
	for _, event := range fieldOrder {
		present := setOf(turn.Fields[event])
		for _, field := range contract.Fields[event] {
			if present[field] || excepted(harness, field) {
				continue
			}
			issues = append(issues, fmt.Sprintf("%s: 回合 %q 的 %s 缺字段 %s", harness, turnName, event, field))
		}
	}
	return issues
}

// compareHarnesses 检查「一个 harness 报了、另一个没报」的字段。只在双方都收到过的
// 事件上比 —— 只有一侧出现的事件本身由 Required 或事件覆盖度决定，不在这一层判。
func compareHarnesses(turns map[string]Turn) []string {
	var issues []string
	harnesses := sortedKeys(stringSet(turns))
	if len(harnesses) < 2 {
		return nil
	}
	events := map[string]bool{}
	for _, turn := range turns {
		for _, event := range turn.Events {
			events[event] = true
		}
	}
	for _, event := range sortedKeys(events) {
		owners := map[string]map[string]bool{}
		for _, harness := range harnesses {
			turn := turns[harness]
			if !setOf(turn.Events)[event] {
				continue
			}
			owners[harness] = setOf(turn.Fields[event])
		}
		if len(owners) < 2 {
			continue
		}
		for _, owner := range sortedKeys(fieldSetNames(owners)) {
			for _, field := range sortedKeys(owners[owner]) {
				if excepted(owner, field) {
					// 例外表登记在**报出方**身上：这是它自己的概念，别人没有是对的。
					continue
				}
				for _, other := range sortedKeys(fieldSetNames(owners)) {
					if other == owner || owners[other][field] || excepted(other, field) {
						continue
					}
					issues = append(issues, fmt.Sprintf("%s: %s 报了字段 %s，%s 没有报", other, owner, field, other))
				}
			}
		}
	}
	return issues
}

// excepted 判断某个 harness 的某条字段路径是否在例外表里。末尾 * 按前缀匹配。
func excepted(harness, field string) bool {
	for _, entry := range Exceptions[harness] {
		if entry == field {
			return true
		}
		if strings.HasSuffix(entry, "*") && strings.HasPrefix(field, strings.TrimSuffix(entry, "*")) {
			return true
		}
	}
	return false
}

func findTurn(shape Shape, name string) (Turn, bool) {
	for _, turn := range shape.Turns {
		if turn.Name == name {
			return turn, true
		}
	}
	return Turn{}, false
}

func contractNames() map[string]bool {
	names := map[string]bool{}
	for name := range Required {
		names[name] = true
	}
	return names
}

func shapeNames(shapes []Shape) map[string]bool {
	names := map[string]bool{}
	for _, shape := range shapes {
		names[shape.Harness] = true
	}
	return names
}

func sortedShapeNames(shapes []Shape) []string {
	return sortedKeys(shapeNames(shapes))
}

func fieldListNames(fields map[string][]string) map[string]bool {
	names := map[string]bool{}
	for name := range fields {
		names[name] = true
	}
	return names
}

func fieldSetNames(sets map[string]map[string]bool) map[string]bool {
	names := map[string]bool{}
	for name := range sets {
		names[name] = true
	}
	return names
}

func stringSet(turns map[string]Turn) map[string]bool {
	names := map[string]bool{}
	for name := range turns {
		names[name] = true
	}
	return names
}

func setOf(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}

func anyOf(set map[string]bool, values []string) bool {
	for _, value := range values {
		if set[value] {
			return true
		}
	}
	return false
}

func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
