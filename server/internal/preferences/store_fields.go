package preferences

import (
	"encoding/json"
	"errors"
	"reflect"
	"regexp"
	"strings"
)

// 顶层字段级读写：同步内核按字段同步偏好，而不是整文件覆盖 ——
// 整文件覆盖会让「A 改了 agents」把「B 刚改的 session_naming」一起冲掉。
//
// 未知字段（磁盘上有、本版本结构体里没有）必须原样保留：滚动升级期间新版本写的字段
// 会被老版本读进来再写回去，丢掉就等于老版本把新版本的数据静默删了。

var preferenceFieldNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// ValidFieldName 供同步内核校验记录 key。字段名会进 JSON 文档与文件名之外的路径，
// 只允许小写字母开头的短标识符，别让远端塞进来奇怪的东西。
func ValidFieldName(name string) bool {
	return preferenceFieldNamePattern.MatchString(strings.TrimSpace(name))
}

// knownPreferenceFields 从结构体标签反推，避免手写清单与结构体漂移。
var knownPreferenceFields = func() map[string]bool {
	out := map[string]bool{}
	typ := reflect.TypeOf(UserPreferences{})
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("json")
		name := strings.Split(tag, ",")[0]
		if name == "" || name == "-" {
			continue
		}
		out[name] = true
	}
	return out
}()

// Fields 返回当前偏好的顶层字段（原始 JSON），含未知字段。
func (s *Store) Fields() (map[string]json.RawMessage, error) {
	if s == nil {
		return nil, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.fieldsLocked()
}

func (s *Store) fieldsLocked() (map[string]json.RawMessage, error) {
	out := make(map[string]json.RawMessage, len(s.unknown)+len(knownPreferenceFields))
	for name, raw := range s.unknown {
		out[name] = raw
	}
	b, err := json.Marshal(s.data)
	if err != nil {
		return nil, err
	}
	var known map[string]json.RawMessage
	if err := json.Unmarshal(b, &known); err != nil {
		return nil, err
	}
	for name, raw := range known {
		out[name] = raw
	}
	return out, nil
}

// documentLocked 是写盘用的整份文档：结构体字段 + 未知字段。
func (s *Store) documentLocked() map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(s.unknown)+len(knownPreferenceFields))
	for name, raw := range s.unknown {
		out[name] = raw
	}
	if b, err := json.Marshal(s.data); err == nil {
		var known map[string]json.RawMessage
		if json.Unmarshal(b, &known) == nil {
			for name, raw := range known {
				out[name] = raw
			}
		}
	}
	return out
}

// SetField 写入或删除（raw == nil）一个顶层字段，其他字段与未知字段保持不变。
func (s *Store) SetField(name string, raw json.RawMessage) error {
	if s == nil {
		return errors.New("preferences store not configured")
	}
	name = strings.TrimSpace(name)
	if !preferenceFieldNamePattern.MatchString(name) {
		return errors.New("invalid preference field name: " + name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.fieldsLocked()
	if err != nil {
		return err
	}
	if raw == nil {
		delete(doc, name)
	} else {
		var probe any
		if err := json.Unmarshal(raw, &probe); err != nil {
			return errors.New("invalid preference field value for " + name + ": " + err.Error())
		}
		doc[name] = append(json.RawMessage(nil), raw...)
	}
	encoded, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	var next UserPreferences
	if err := json.Unmarshal(encoded, &next); err != nil {
		return err
	}
	if next.Agents == nil {
		next.Agents = map[string]AgentDefaults{}
	}
	unknown := map[string]json.RawMessage{}
	for field, value := range doc {
		if !knownPreferenceFields[field] {
			unknown[field] = value
		}
	}
	prevData, prevUnknown := s.data, s.unknown
	s.data, s.unknown = next, unknown
	if err := s.saveLocked(); err != nil {
		s.data, s.unknown = prevData, prevUnknown
		return err
	}
	return nil
}
