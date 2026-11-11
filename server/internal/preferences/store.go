package preferences

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"mindfs/server/internal/agent"
	agenttypes "mindfs/server/internal/agent/types"
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
	// CustomModels 是用户在界面上为某个 agent 追加的模型 ID（例如给 codex 加 "of"）。
	// 它们会并入 /api/agents 返回的 models 目录，因此能在会话的模型选择里直接选中。
	CustomModels []string `json:"custom_models,omitempty"`
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
	return NewStoreAt(configDir)
}

// NewStoreAt 把偏好放在指定目录（多账户：每个账户一套偏好）。
func NewStoreAt(configDir string) (*Store, error) {
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
	current := s.data.Agents[agentName]
	if current.Model == next.Model && current.Effort == next.Effort && current.FastService == next.FastService {
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

// AgentCustomModels 返回某个 agent 的自定义模型列表快照。
func (s *Store) AgentCustomModels(agentName string) []string {
	agentName = strings.TrimSpace(agentName)
	if s == nil || agentName == "" {
		return []string{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.data.Agents[agentName].CustomModels...)
}

// UpdateAgentCustomModels 覆盖某个 agent 的自定义模型列表；空列表等价于清空。
func (s *Store) UpdateAgentCustomModels(agentName string, models []string) error {
	agentName = strings.TrimSpace(agentName)
	if s == nil || agentName == "" {
		return errors.New("agent required")
	}
	next := normalizeCustomModels(models)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data.Agents == nil {
		s.data.Agents = map[string]AgentDefaults{}
	}
	defaults := s.data.Agents[agentName]
	if equalStrings(defaults.CustomModels, next) {
		return nil
	}
	defaults.CustomModels = next
	s.data.Agents[agentName] = defaults
	return s.saveLocked()
}

func normalizeCustomModels(input []string) []string {
	seen := make(map[string]bool, len(input))
	out := make([]string, 0, len(input))
	for _, item := range input {
		model := strings.TrimSpace(item)
		if model == "" || seen[model] {
			continue
		}
		seen[model] = true
		out = append(out, model)
	}
	return out
}

func equalStrings(a, b []string) bool {
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

func mergeCustomModels(models []agenttypes.ModelInfo, custom []string) []agenttypes.ModelInfo {
	custom = normalizeCustomModels(custom)
	if len(custom) == 0 {
		return models
	}
	seen := make(map[string]bool, len(models)+len(custom))
	for _, item := range models {
		seen[strings.TrimSpace(item.ID)] = true
	}
	out := append([]agenttypes.ModelInfo(nil), models...)
	for _, id := range custom {
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, agenttypes.ModelInfo{ID: id, Name: id})
	}
	return out
}

// AgentLastConfigSelections returns a snapshot of every agent's last manual
// config selection keyed by agent name. Callers must treat the result as
// read-only.
func (s *Store) AgentLastConfigSelections() map[string]LastConfigSelection {
	out := map[string]LastConfigSelection{}
	if s == nil {
		return out
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for name, defaults := range s.data.Agents {
		if defaults.LastConfigSelection != nil {
			out[strings.TrimSpace(name)] = *defaults.LastConfigSelection
		}
	}
	return out
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
		// 先并入自定义模型，再校验默认模型：否则用户刚添加的模型会因为
		// 不在厂商目录里被 SanitizeDefaultModelID 当场回退掉。
		if len(defaults.CustomModels) > 0 {
			out[i].Models = mergeCustomModels(out[i].Models, defaults.CustomModels)
		}
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
