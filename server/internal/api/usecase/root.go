package usecase

import (
	"errors"

	"mindfs/server/internal/agent"
	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/fs"
	"mindfs/server/internal/preferences"
	"mindfs/server/internal/session"
)

type Registry interface {
	GetRoot(rootID string) (fs.RootInfo, error)
	GetSessionManager(rootID string) (*session.Manager, error)
	UpsertRoot(path string) (fs.RootInfo, error)
	RemoveRoot(path string) (fs.RootInfo, error)
	RenameRoot(rootID, name, rootPath string) (fs.RootInfo, error)
	UpdateDisplayName(rootID, displayName string) (fs.RootInfo, error)
	ListRoots() []fs.RootInfo
	GetAgentPool() *agent.Pool
	GetPreferences() *preferences.Store
	GetExternalSessionImporter(agentName string) (agenttypes.ExternalSessionImporter, error)
	GetProber() *agent.Prober
	GetCandidateRegistry() *CandidateRegistry
	GetFileWatcher(rootID string, manager *session.Manager) (*fs.SharedFileWatcher, error)
	ReleaseFileWatcher(rootID, sessionKey string)
}

type Service struct {
	Registry Registry
}

// accountMetaRoot 取本账户私有的 meta 根（主账户/测试替身没有这个能力 → 空串）。
// 做成可选能力而不是 Registry 接口方法：只有真正按账户分区的实现才需要提供它。
func accountMetaRoot(registry Registry) string {
	provider, ok := registry.(interface{ MetaRoot() string })
	if !ok {
		return ""
	}
	return provider.MetaRoot()
}

func (s *Service) ensureRegistry() error {
	if s == nil || s.Registry == nil {
		return errors.New("services not configured")
	}
	return nil
}
