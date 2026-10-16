package api

import (
	"errors"
	"strings"

	"mindfs/server/internal/agent"
	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/api/usecase"
	"mindfs/server/internal/auth"
	"mindfs/server/internal/commandexec"
	"mindfs/server/internal/fs"
	"mindfs/server/internal/githubimport"
	"mindfs/server/internal/kanban"
	"mindfs/server/internal/pins"
	"mindfs/server/internal/preferences"
	"mindfs/server/internal/session"
	"mindfs/server/internal/update"
	"mindfs/server/internal/webpush"
)

func (s *AppContext) GetSessionManager(rootID string) (*session.Manager, error) {
	rootCtx, err := s.GetRootContext(rootID)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if rootCtx.Session != nil {
		return rootCtx.Session, nil
	}
	mgr := session.NewManager(rootCtx.Root)
	rootCtx.Session = mgr

	return mgr, nil
}

func (s *AppContext) GetKanbanService() (*kanban.Service, error) {
	if s == nil {
		return nil, errors.New("app context required")
	}
	s.mu.RLock()
	service := s.Kanban
	s.mu.RUnlock()
	if service == nil {
		return nil, errors.New("kanban service not configured")
	}
	return service, nil
}

func (s *AppContext) GetFileWatcher(rootID string, manager *session.Manager) (*fs.SharedFileWatcher, error) {
	rootCtx, err := s.GetRootContext(rootID)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if rootCtx.Watcher != nil {
		return rootCtx.Watcher, nil
	}
	watcher, err := fs.NewSharedFileWatcher(rootCtx.Root, manager, resolveRelatedWorktree)
	if err != nil {
		return nil, err
	}
	watcher.SetOnFileChange(s.emitFileChange)
	watcher.SetOnFileChangeBatch(s.emitFileChangeBatch)
	watcher.SetOnRelatedFile(s.emitRelatedFile)
	rootCtx.Watcher = watcher
	return watcher, nil
}

func (s *AppContext) GetAgentPool() *agent.Pool {
	return s.Agents
}

func (s *AppContext) GetPreferences() *preferences.Store {
	return s.Prefs
}

func (s *AppContext) GetPins() *pins.Store {
	return s.Pins
}

func (s *AppContext) GetExternalSessionImporter(agentName string) (agenttypes.ExternalSessionImporter, error) {
	if s.Agents == nil {
		return nil, errors.New("agent pool not configured")
	}
	trimmed := strings.TrimSpace(agentName)
	if trimmed == "" {
		return nil, errors.New("agent required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.externalImporters == nil {
		s.externalImporters = make(map[string]agenttypes.ExternalSessionImporter)
	}
	if importer, ok := s.externalImporters[trimmed]; ok && importer != nil {
		return importer, nil
	}
	def, ok := s.Agents.Config().GetAgent(trimmed)
	if !ok {
		return nil, errors.New("agent not configured: " + trimmed)
	}
	importer, err := agent.NewExternalSessionImporter(def)
	if err != nil {
		return nil, err
	}
	s.externalImporters[trimmed] = importer
	return importer, nil
}

func (s *AppContext) GetProber() *agent.Prober {
	return s.Prober
}

func (s *AppContext) GetDirRegistry() *fs.Registry {
	return s.Dirs
}

func (s *AppContext) GetUpdateService() *update.Service {
	return s.Update
}

func (s *AppContext) GetWebPushService() *webpush.Service {
	return s.WebPush
}

func (s *AppContext) GetGitHubImportService() *githubimport.Service {
	return s.GitHub
}

func (s *AppContext) GetAuthStore() *auth.Store {
	return s.Auth
}

func (s *AppContext) GetSessionStreamHub() *StreamHub {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.streamHub == nil {
		s.streamHub = NewStreamHub()
	}
	return s.streamHub
}

func (s *AppContext) GetCandidateRegistry() *usecase.CandidateRegistry {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.candidateRegistry == nil {
		registry := usecase.NewCandidateRegistry()
		registry.Register(usecase.NewFileCandidateProvider())
		if store, err := usecase.OpenPromptStore(s.ConfigDir()); err == nil {
			registry.Register(usecase.NewPromptCandidateProvider(store))
		}
		registry.Register(usecase.NewSkillCandidateProvider())
		registry.Register(usecase.NewCommandCandidateProvider(func() usecase.ShellHistorySpec {
			if s.Agents == nil {
				return usecase.ShellHistorySpec{}
			}
			cfg := s.Agents.Config()
			shells := make([]commandexec.ShellSpec, 0, len(cfg.Shells))
			for _, shell := range cfg.Shells {
				shells = append(shells, commandexec.ShellSpec{
					Command:       shell.Command,
					Args:          append([]string(nil), shell.Args...),
					LongShellArgs: append([]string(nil), shell.LongShellArgs...),
					CommandPrefix: shell.CommandPrefix,
				})
			}
			if shell, ok := commandexec.ResolveConfiguredShell(shells); ok {
				return usecase.ShellHistorySpec{Command: shell.Command}
			}
			return usecase.ShellHistorySpec{}
		}))
		registry.Register(usecase.NewSlashCommandCandidateProvider(func(agentName string) (agent.Status, bool) {
			if s.Prober == nil {
				return agent.Status{}, false
			}
			return s.Prober.GetStatus(agentName)
		}))
		s.candidateRegistry = registry
	}
	return s.candidateRegistry
}
