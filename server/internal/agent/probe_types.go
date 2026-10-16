package agent

import (
	"log"
	"path/filepath"
	"strings"
	"sync"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
	configpkg "mindfs/server/internal/config"
)

type Status struct {
	Name                          string                   `json:"name"`
	Protocol                      Protocol                 `json:"protocol,omitempty"`
	Installed                     bool                     `json:"installed"`
	Available                     bool                     `json:"available"`
	Version                       string                   `json:"version,omitempty"`
	Error                         string                   `json:"error,omitempty"`
	RuntimeError                  string                   `json:"-"`
	ProbeError                    string                   `json:"-"`
	LastProbe                     time.Time                `json:"last_probe"`
	CurrentModelID                string                   `json:"current_model_id,omitempty"`
	CurrentModeID                 string                   `json:"current_mode_id,omitempty"`
	DefaultModelID                string                   `json:"default_model_id,omitempty"`
	DefaultEffort                 string                   `json:"default_effort,omitempty"`
	DefaultFastService            string                   `json:"default_fast_service,omitempty"`
	LastConfigSelection           any                      `json:"last_config_selection,omitempty"`
	SupportsAPIProviderSwitch     bool                     `json:"supports_api_provider_switch"`
	SupportedAPIProviderProtocols []string                 `json:"supported_api_provider_protocols,omitempty"`
	SupportsFastService           bool                     `json:"supports_fast_service"`
	Models                        []agenttypes.ModelInfo   `json:"models,omitempty"`
	Modes                         []agenttypes.ModeInfo    `json:"modes"`
	Efforts                       []string                 `json:"efforts,omitempty"`
	ModelsError                   string                   `json:"models_error,omitempty"`
	ModesError                    string                   `json:"modes_error,omitempty"`
	Commands                      []agenttypes.CommandInfo `json:"commands,omitempty"`
	CommandsError                 string                   `json:"commands_error,omitempty"`
	Brief                         string                   `json:"brief,omitempty"`
	InstallCommands               []string                 `json:"install_commands,omitempty"`
	UpdateCommands                []string                 `json:"update_commands,omitempty"`
}

const (
	probeSessionTimeout     = 45 * time.Second
	probeInteractionTimeout = 3 * time.Minute
	probeModelListTimeout   = 30 * time.Second
	probeCommandListTimeout = 30 * time.Second
	probeRotateAfterCount   = 100
	probeRotateAfterAge     = 24 * time.Hour
	probeSessionStoreFile   = "probe-sessions.json"
)

type probePhase string

const (
	probePhaseInitial    probePhase = "initial"
	probePhaseBackground probePhase = "background"
	probePhaseRecovery   probePhase = "recovery"
)

type ProbeSessionBinding struct {
	AgentSessionID string    `json:"agent_session_id"`
	ProbeCount     int       `json:"probe_count,omitempty"`
	CreatedAt      time.Time `json:"created_at,omitempty"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type probeSessionStore struct {
	mu   sync.RWMutex
	path string
	data map[string]ProbeSessionBinding
}

// Prober 管理 Agent 可用性探测
type Prober struct {
	cfg           *Config
	pool          *Pool
	probeSessions *probeSessionStore
	statuses      map[string]Status
	mu            sync.RWMutex
	inFlight      map[string]struct{} // per-agent probe 去重，mu 保护
	probeInterval time.Duration
	stopCh        chan struct{}
	listeners     []func(Status)
	// claudeSettingsMtimes 记录 ~/.claude/settings.json 的 mtime（cc-switch 改写 detect），mu 保护。
	claudeSettingsMtimes map[string]time.Time
}

func NewProber(cfg *Config, pool *Pool, probeInterval time.Duration) *Prober {
	if probeInterval <= 0 {
		probeInterval = 5 * time.Minute
	}
	probeSessions, err := loadProbeSessionStore()
	if err != nil {
		log.Printf("[agent/probe] probe_session_store.init_error err=%v", err)
	}
	p := &Prober{
		cfg:                  cfg,
		pool:                 pool,
		probeSessions:        probeSessions,
		statuses:             make(map[string]Status),
		inFlight:             make(map[string]struct{}),
		probeInterval:        probeInterval,
		stopCh:               make(chan struct{}),
		claudeSettingsMtimes: make(map[string]time.Time),
	}
	// Seed configured agents so API can return stable list before first probe completes.
	if cfg != nil {
		now := time.Now().UTC()
		for _, def := range cfg.Agents {
			p.statuses[def.Name] = normalizeStatus(probeInstallStatus(def.Name, def, now))
		}
	}
	return p
}

func loadProbeSessionStore() (*probeSessionStore, error) {
	configDir, err := configpkg.MindFSConfigDir()
	if err != nil {
		return nil, err
	}
	store := &probeSessionStore{
		path: filepath.Join(configDir, probeSessionStoreFile),
		data: make(map[string]ProbeSessionBinding),
	}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func probeSessionTimeoutForPhase(phase probePhase) (time.Duration, bool) {
	switch phase {
	case probePhaseInitial:
		return probeSessionTimeout, true
	case probePhaseRecovery:
		return 30 * time.Second, true
	default:
		return 0, false
	}
}

func probeInteractionTimeoutForPhase(phase probePhase) (time.Duration, bool) {
	switch phase {
	case probePhaseInitial:
		return probeInteractionTimeout, true
	case probePhaseRecovery:
		return 30 * time.Second, true
	default:
		return 0, false
	}
}

func loadProbeSessionBinding(store *probeSessionStore, agentName string) (ProbeSessionBinding, bool) {
	if store == nil {
		return ProbeSessionBinding{}, false
	}
	return store.Get(agentName)
}

func clearProbeSessionBinding(store *probeSessionStore, agentName string) error {
	if store == nil {
		return nil
	}
	return store.Delete(agentName)
}

func shouldRotateProbeSession(binding ProbeSessionBinding) bool {
	if binding.ProbeCount <= probeRotateAfterCount {
		return false
	}
	if binding.CreatedAt.IsZero() {
		return false
	}
	return time.Since(binding.CreatedAt.UTC()) > probeRotateAfterAge
}

func storeProbeSessionBinding(store *probeSessionStore, agentName, agentSessionID string, previous ProbeSessionBinding, resumed bool) error {
	if store == nil {
		return nil
	}
	agentSessionID = strings.TrimSpace(agentSessionID)
	if agentSessionID == "" {
		return nil
	}
	next := ProbeSessionBinding{
		AgentSessionID: agentSessionID,
		ProbeCount:     1,
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}
	if resumed && strings.TrimSpace(previous.AgentSessionID) == agentSessionID && previous.ProbeCount > 0 {
		next.ProbeCount = previous.ProbeCount + 1
		next.CreatedAt = previous.CreatedAt
		if next.CreatedAt.IsZero() {
			next.CreatedAt = time.Now().UTC()
		}
	}
	return store.PutBinding(agentName, next)
}
