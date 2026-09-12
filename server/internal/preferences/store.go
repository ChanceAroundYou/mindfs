package preferences

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"mindfs/server/internal/agent"
	"mindfs/server/internal/config"
)

const preferencesFileName = "preferences.json"

const DefaultIdleSessionResourceReleaseHours = 72
const MaxIdleSessionResourceReleaseHours = 2_562_047

type Store struct {
	mu   sync.RWMutex
	path string
	data UserPreferences
}

type UserPreferences struct {
	Agents                          map[string]AgentDefaults `json:"agents,omitempty"`
	SessionNaming                   SessionNamingDefaults    `json:"session_naming,omitempty"`
	IdleSessionResourceReleaseHours int                      `json:"idle_session_resource_release_hours,omitempty"`
	NewProjectMetaLocation          string                   `json:"new_project_meta_location,omitempty"`
	CORS                            CORSPreferences          `json:"cors,omitempty"`
	SessionProjectPins              map[string]int64         `json:"session_project_pins,omitempty"`
}

type CORSPreferences struct {
	Mode         string   `json:"mode,omitempty"`
	AllowOrigins []string `json:"allow_origins,omitempty"`
}

func (s *Store) NewProjectMetaLocation() string {
	if s == nil {
		return "project"
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.data.NewProjectMetaLocation == "home" {
		return "home"
	}
	return "project"
}

func (s *Store) UpdateNewProjectMetaLocation(location string) error {
	if s == nil {
		return nil
	}
	location = strings.TrimSpace(location)
	if location != "project" && location != "home" {
		return errors.New("invalid new project metadata location")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data.NewProjectMetaLocation == location {
		return nil
	}
	s.data.NewProjectMetaLocation = location
	return s.saveLocked()
}

func (s *Store) IdleSessionResourceReleaseHours() int {
	if s == nil {
		return DefaultIdleSessionResourceReleaseHours
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.data.IdleSessionResourceReleaseHours <= 0 {
		return DefaultIdleSessionResourceReleaseHours
	}
	return s.data.IdleSessionResourceReleaseHours
}

func (s *Store) UpdateIdleSessionResourceReleaseHours(hours int) error {
	if s == nil {
		return nil
	}
	if hours <= 0 || hours > MaxIdleSessionResourceReleaseHours {
		return errors.New("idle session resource release hours are out of range")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data.IdleSessionResourceReleaseHours == hours {
		return nil
	}
	s.data.IdleSessionResourceReleaseHours = hours
	return s.saveLocked()
}

func (s *Store) CORSMode() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return strings.TrimSpace(s.data.CORS.Mode)
}

func (s *Store) CORSAllowOrigins() []string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, len(s.data.CORS.AllowOrigins))
	copy(out, s.data.CORS.AllowOrigins)
	return out
}

func (s *Store) IsCORSOriginAllowed(origin string) bool {
	if s == nil {
		return false
	}
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return false
	}
	// ponytail: exact origin match (scheme+host+port), case-insensitive; "*" means allow all
	allowed := s.CORSAllowOrigins()
	for _, entry := range allowed {
		e := strings.TrimSpace(entry)
		if e == "*" {
			return true
		}
		if strings.EqualFold(e, origin) {
			return true
		}
	}
	return false
}

func (s *Store) CORSPreferences() CORSPreferences {
	if s == nil {
		return CORSPreferences{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	cp := s.data.CORS
	out := make([]string, len(cp.AllowOrigins))
	copy(out, cp.AllowOrigins)
	cp.AllowOrigins = out
	return cp
}

func (s *Store) UpdateCORSPreferences(mode string, allowOrigins []string) error {
	if s == nil {
		return nil
	}
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "open"
	}
	switch mode {
	case "open", "auto", "allow_all", "all", "*", "allowlist", "whitelist", "disabled", "off", "closed", "same_origin":
	default:
		return errors.New("invalid cors mode: use open/auto/allowlist/disabled")
	}
	normalized := make([]string, 0, len(allowOrigins))
	seen := map[string]struct{}{}
	for _, raw := range allowOrigins {
		v := strings.TrimSpace(raw)
		if v == "" {
			continue
		}
		if v != "*" {
			// basic origin shape check: must parse as URL with scheme+host
			// allow bare origin like https://host or https://host:port
			if !strings.Contains(v, "://") {
				return errors.New("allow_origins must be origins like https://host or *")
			}
		}
		low := strings.ToLower(v)
		if _, ok := seen[low]; ok {
			continue
		}
		seen[low] = struct{}{}
		if v == "*" {
			normalized = []string{"*"}
			break
		}
		normalized = append(normalized, v)
	}
	if (mode == "allowlist" || mode == "whitelist") && len(normalized) == 0 {
		return errors.New("allowlist mode requires at least one allow_origins entry")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := CORSPreferences{Mode: mode, AllowOrigins: normalized}
	if s.data.CORS.Mode == next.Mode && equalStringSlices(s.data.CORS.AllowOrigins, next.AllowOrigins) {
		return nil
	}
	s.data.CORS = next
	return s.saveLocked()
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

type SessionNamingDefaults struct {
	Agent    string `json:"agent,omitempty"`
	Model    string `json:"model,omitempty"`
	Disabled bool   `json:"disabled,omitempty"`
}

type AgentDefaults struct {
	Model               string               `json:"model,omitempty"`
	Effort              string               `json:"effort,omitempty"`
	FastService         string               `json:"fast_service,omitempty"`
	LastConfigSelection *LastConfigSelection `json:"last_config_selection,omitempty"`
}

type LastConfigSelection struct {
	Type string `json:"type,omitempty"`
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

func NewStore() (*Store, error) {
	configDir, err := config.MindFSConfigDir()
	if err != nil {
		return nil, err
	}
	store := &Store{
		path: filepath.Join(configDir, preferencesFileName),
		data: UserPreferences{Agents: map[string]AgentDefaults{}},
	}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return nil
	}
	var data UserPreferences
	if err := json.Unmarshal(b, &data); err != nil {
		return err
	}
	if data.Agents == nil {
		data.Agents = map[string]AgentDefaults{}
	}
	s.data = data
	return nil
}

func (s *Store) UpdateAgentDefaultsIfChanged(agentName, model, effort, fastService string) (bool, error) {
	if s == nil {
		return false, nil
	}
	agentName = strings.TrimSpace(agentName)
	if agentName == "" {
		return false, nil
	}
	model = strings.TrimSpace(model)
	effort = strings.TrimSpace(effort)
	fastService = strings.TrimSpace(fastService)
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.data.Agents == nil {
		s.data.Agents = map[string]AgentDefaults{}
	}
	next := s.data.Agents[agentName]
	if model != "" {
		next.Model = model
	}
	if effort != "" {
		next.Effort = effort
	}
	if fastService != "" {
		next.FastService = fastService
	}
	if s.data.Agents[agentName] == next {
		return false, nil
	}
	s.data.Agents[agentName] = next
	if err := s.saveLocked(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) UpdateAgentLastConfigSelection(agentName string, selection LastConfigSelection) error {
	if s == nil {
		return nil
	}
	agentName = strings.TrimSpace(agentName)
	selection.Type = strings.TrimSpace(selection.Type)
	selection.ID = strings.TrimSpace(selection.ID)
	selection.Name = strings.TrimSpace(selection.Name)
	if agentName == "" || selection.Type == "" || selection.ID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.data.Agents == nil {
		s.data.Agents = map[string]AgentDefaults{}
	}
	next := s.data.Agents[agentName]
	if next.LastConfigSelection != nil && *next.LastConfigSelection == selection {
		return nil
	}
	next.LastConfigSelection = &selection
	s.data.Agents[agentName] = next
	return s.saveLocked()
}

func (s *Store) SessionNamingDefaults() SessionNamingDefaults {
	if s == nil {
		return SessionNamingDefaults{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data.SessionNaming
}

func (s *Store) UpdateSessionNamingDefaults(agentName, model string, disabled bool) error {
	if s == nil {
		return nil
	}
	next := SessionNamingDefaults{
		Agent:    strings.TrimSpace(agentName),
		Model:    strings.TrimSpace(model),
		Disabled: disabled,
	}
	if next.Agent == "" && !next.Disabled {
		return errors.New("session naming agent is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data.SessionNaming == next {
		return nil
	}
	s.data.SessionNaming = next
	return s.saveLocked()
}

func (s *Store) ApplyAgentDefaults(statuses []agent.Status) []agent.Status {
	if s == nil || len(statuses) == 0 {
		return statuses
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.data.Agents) == 0 {
		return statuses
	}
	out := append([]agent.Status(nil), statuses...)
	for i := range out {
		defaults := s.data.Agents[strings.TrimSpace(out[i].Name)]
		if defaults.Model != "" {
			out[i].DefaultModelID = defaults.Model
			// 过期默认模型回归：provider 切换（cc-switch）后旧模型（of/os 等）
			// 不在当前网关目录内，回退到目录模型避免新建会话直接 400。
			// 存储保留原值，切回原 provider 时自动恢复。
			out[i] = agent.SanitizeDefaultModelID(out[i])
		}
		if defaults.Effort != "" {
			out[i].DefaultEffort = defaults.Effort
		}
		if defaults.FastService != "" {
			out[i].DefaultFastService = defaults.FastService
		}
		if defaults.LastConfigSelection != nil {
			out[i].LastConfigSelection = *defaults.LastConfigSelection
		}
	}
	return out
}

// SessionProjectPins: 右侧会话栏项目置顶时间戳（key=scopeKey，value=置顶时间 ms，0 表示未置顶）。
func (s *Store) SessionProjectPins() map[string]int64 {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]int64, len(s.data.SessionProjectPins))
	for k, v := range s.data.SessionProjectPins {
		out[k] = v
	}
	return out
}

func (s *Store) UpdateSessionProjectPins(pins map[string]int64) error {
	if s == nil {
		return nil
	}
	if pins == nil {
		pins = map[string]int64{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(pins) == len(s.data.SessionProjectPins) {
		same := true
		for k, v := range pins {
			if s.data.SessionProjectPins[k] != v {
				same = false
				break
			}
		}
		if same {
			return nil
		}
	}
	next := make(map[string]int64, len(pins))
	for k, v := range pins {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		next[k] = v
	}
	s.data.SessionProjectPins = next
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(s.path)
		if retryErr := os.Rename(tmp, s.path); retryErr != nil {
			return err
		}
	}
	return nil
}
