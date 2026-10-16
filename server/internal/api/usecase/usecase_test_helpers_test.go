package usecase

import (
	"context"
	"errors"
	"path/filepath"

	"mindfs/server/internal/agent"
	agenttypes "mindfs/server/internal/agent/types"
	rootfs "mindfs/server/internal/fs"
	"mindfs/server/internal/preferences"
	"mindfs/server/internal/session"
)

type fakeUsecaseAgentSession struct {
	id          string
	model       string
	cancelCalls int
	closeCalls  int
	onUpdate    func(agenttypes.Event)
}

func (s *fakeUsecaseAgentSession) SendMessage(context.Context, string) error { return nil }

func (s *fakeUsecaseAgentSession) AnswerQuestion(context.Context, agenttypes.AskUserAnswer) error {
	return nil
}

func (s *fakeUsecaseAgentSession) CurrentModel() string { return s.model }

func (s *fakeUsecaseAgentSession) SetModel(context.Context, string) error { return nil }

func (s *fakeUsecaseAgentSession) ListModels(context.Context) (agenttypes.ModelList, error) {
	return agenttypes.ModelList{}, nil
}

func (s *fakeUsecaseAgentSession) SetMode(context.Context, string) error { return nil }

func (s *fakeUsecaseAgentSession) SetPlanMode(context.Context, bool) error { return nil }

func (s *fakeUsecaseAgentSession) ListModes(context.Context) (agenttypes.ModeList, error) {
	return agenttypes.ModeList{}, nil
}

func (s *fakeUsecaseAgentSession) ListCommands(context.Context) (agenttypes.CommandList, error) {
	return agenttypes.CommandList{}, nil
}

func (s *fakeUsecaseAgentSession) CancelCurrentTurn() error {
	s.cancelCalls++
	return nil
}

func (s *fakeUsecaseAgentSession) OnUpdate(onUpdate func(agenttypes.Event)) {
	s.onUpdate = onUpdate
}

func (s *fakeUsecaseAgentSession) SessionID() string { return s.id }

func (s *fakeUsecaseAgentSession) ContextWindow(context.Context) (agenttypes.ContextWindow, error) {
	return agenttypes.ContextWindow{}, nil
}

func (s *fakeUsecaseAgentSession) Close() error {
	s.closeCalls++
	return nil
}

func (s *fakeUsecaseAgentSession) emit(event agenttypes.Event) {
	if s.onUpdate != nil {
		s.onUpdate(event)
	}
}

type renameManagedDirTestRegistry struct {
	root                       rootfs.RootInfo
	others                     []rootfs.RootInfo
	renameErr                  error
	releaseRootResourcesCalled bool
}

func (*renameManagedDirTestRegistry) GetAgentPool() *agent.Pool {
	return nil
}

func (*renameManagedDirTestRegistry) GetPreferences() *preferences.Store {
	return nil
}

func (*renameManagedDirTestRegistry) GetExternalSessionImporter(string) (agenttypes.ExternalSessionImporter, error) {
	return nil, errors.New("not implemented")
}

func (*renameManagedDirTestRegistry) GetProber() *agent.Prober {
	return nil
}

func (*renameManagedDirTestRegistry) GetCandidateRegistry() *CandidateRegistry {
	return nil
}

func (*renameManagedDirTestRegistry) GetFileWatcher(string, *session.Manager) (*rootfs.SharedFileWatcher, error) {
	return nil, nil
}

func (*renameManagedDirTestRegistry) ReleaseFileWatcher(string, string) {}

func (r *renameManagedDirTestRegistry) GetRoot(rootID string) (rootfs.RootInfo, error) {
	if rootID != r.root.ID {
		return rootfs.RootInfo{}, errors.New("root not found")
	}
	return r.root, nil
}

func (*renameManagedDirTestRegistry) GetSessionManager(string) (*session.Manager, error) {
	return nil, nil
}

func (*renameManagedDirTestRegistry) UpsertRoot(string) (rootfs.RootInfo, error) {
	return rootfs.RootInfo{}, nil
}

func (*renameManagedDirTestRegistry) RemoveRoot(string) (rootfs.RootInfo, error) {
	return rootfs.RootInfo{}, nil
}

func (r *renameManagedDirTestRegistry) UpdateDisplayName(string, string) (rootfs.RootInfo, error) {
	return rootfs.RootInfo{}, errors.New("not implemented")
}

func (r *renameManagedDirTestRegistry) RenameRoot(rootID, name, rootPath string) (rootfs.RootInfo, error) {
	if r.renameErr != nil {
		return rootfs.RootInfo{}, r.renameErr
	}
	if rootID != r.root.ID {
		return rootfs.RootInfo{}, errors.New("root not found")
	}
	r.root.ID = name
	r.root.Name = name
	r.root.RootPath = filepath.Clean(rootPath)
	return r.root, nil
}

func (r *renameManagedDirTestRegistry) ReleaseRootResources(string) {
	r.releaseRootResourcesCalled = true
}

func (r *renameManagedDirTestRegistry) ListRoots() []rootfs.RootInfo {
	roots := []rootfs.RootInfo{r.root}
	roots = append(roots, r.others...)
	return roots
}

type commandTestRegistry struct {
	root    rootfs.RootInfo
	manager *session.Manager
}

func (r *commandTestRegistry) GetRoot(rootID string) (rootfs.RootInfo, error) {
	if rootID != r.root.ID {
		return rootfs.RootInfo{}, errors.New("root not found")
	}
	return r.root, nil
}

func (r *commandTestRegistry) GetSessionManager(string) (*session.Manager, error) {
	return r.manager, nil
}

func (r *commandTestRegistry) UpsertRoot(string) (rootfs.RootInfo, error) {
	return rootfs.RootInfo{}, nil
}

func (r *commandTestRegistry) RemoveRoot(string) (rootfs.RootInfo, error) {
	return rootfs.RootInfo{}, nil
}

func (r *commandTestRegistry) UpdateDisplayName(string, string) (rootfs.RootInfo, error) {
	return rootfs.RootInfo{}, errors.New("not implemented")
}

func (r *commandTestRegistry) RenameRoot(string, string, string) (rootfs.RootInfo, error) {
	return rootfs.RootInfo{}, nil
}

func (r *commandTestRegistry) ListRoots() []rootfs.RootInfo {
	return []rootfs.RootInfo{r.root}
}

func (r *commandTestRegistry) GetAgentPool() *agent.Pool {
	return nil
}

func (r *commandTestRegistry) GetPreferences() *preferences.Store {
	return nil
}

func (r *commandTestRegistry) GetExternalSessionImporter(string) (agenttypes.ExternalSessionImporter, error) {
	return nil, errors.New("not implemented")
}

func (r *commandTestRegistry) GetProber() *agent.Prober {
	return nil
}

func (r *commandTestRegistry) GetCandidateRegistry() *CandidateRegistry {
	return NewCandidateRegistry()
}

func (r *commandTestRegistry) GetFileWatcher(string, *session.Manager) (*rootfs.SharedFileWatcher, error) {
	return nil, nil
}

func (r *commandTestRegistry) ReleaseFileWatcher(string, string) {}

type multiRootSearchTestRegistry struct {
	roots    []rootfs.RootInfo
	managers map[string]*session.Manager
}

func (r *multiRootSearchTestRegistry) GetRoot(rootID string) (rootfs.RootInfo, error) {
	for _, root := range r.roots {
		if root.ID == rootID {
			return root, nil
		}
	}
	return rootfs.RootInfo{}, errors.New("root not found")
}

func (r *multiRootSearchTestRegistry) GetSessionManager(rootID string) (*session.Manager, error) {
	manager := r.managers[rootID]
	if manager == nil {
		return nil, errors.New("session manager not found")
	}
	return manager, nil
}

func (r *multiRootSearchTestRegistry) UpsertRoot(string) (rootfs.RootInfo, error) {
	return rootfs.RootInfo{}, nil
}

func (r *multiRootSearchTestRegistry) RemoveRoot(string) (rootfs.RootInfo, error) {
	return rootfs.RootInfo{}, nil
}

func (r *multiRootSearchTestRegistry) UpdateDisplayName(string, string) (rootfs.RootInfo, error) {
	return rootfs.RootInfo{}, errors.New("not implemented")
}

func (r *multiRootSearchTestRegistry) RenameRoot(string, string, string) (rootfs.RootInfo, error) {
	return rootfs.RootInfo{}, nil
}

func (r *multiRootSearchTestRegistry) ListRoots() []rootfs.RootInfo {
	return append([]rootfs.RootInfo(nil), r.roots...)
}

func (r *multiRootSearchTestRegistry) GetAgentPool() *agent.Pool {
	return nil
}

func (r *multiRootSearchTestRegistry) GetPreferences() *preferences.Store {
	return nil
}

func (r *multiRootSearchTestRegistry) GetExternalSessionImporter(string) (agenttypes.ExternalSessionImporter, error) {
	return nil, errors.New("not implemented")
}

func (r *multiRootSearchTestRegistry) GetProber() *agent.Prober {
	return nil
}

func (r *multiRootSearchTestRegistry) GetCandidateRegistry() *CandidateRegistry {
	return nil
}

func (r *multiRootSearchTestRegistry) GetFileWatcher(string, *session.Manager) (*rootfs.SharedFileWatcher, error) {
	return nil, nil
}

func (r *multiRootSearchTestRegistry) ReleaseFileWatcher(string, string) {}
