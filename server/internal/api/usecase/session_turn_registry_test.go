package usecase

import (
	"context"
	"errors"
	"testing"

	"mindfs/server/internal/agent"
	agenttypes "mindfs/server/internal/agent/types"
	rootfs "mindfs/server/internal/fs"
	"mindfs/server/internal/preferences"
	"mindfs/server/internal/session"
)

type uploadTestRegistry struct {
	root rootfs.RootInfo
}

func (r uploadTestRegistry) GetRoot(rootID string) (rootfs.RootInfo, error) {
	if rootID != r.root.ID {
		return rootfs.RootInfo{}, errors.New("root not found")
	}
	return r.root, nil
}

func (uploadTestRegistry) GetSessionManager(string) (*session.Manager, error) {
	return nil, nil
}

func (uploadTestRegistry) UpsertRoot(string) (rootfs.RootInfo, error) {
	return rootfs.RootInfo{}, nil
}

func (uploadTestRegistry) RemoveRoot(string) (rootfs.RootInfo, error) {
	return rootfs.RootInfo{}, nil
}

func (uploadTestRegistry) UpdateDisplayName(string, string) (rootfs.RootInfo, error) {
	return rootfs.RootInfo{}, errors.New("not implemented")
}

func (uploadTestRegistry) RenameRoot(string, string, string) (rootfs.RootInfo, error) {
	return rootfs.RootInfo{}, nil
}

func (uploadTestRegistry) ListRoots() []rootfs.RootInfo {
	return nil
}

func (uploadTestRegistry) GetAgentPool() *agent.Pool {
	return nil
}

func (uploadTestRegistry) GetPreferences() *preferences.Store {
	return nil
}

func (uploadTestRegistry) GetExternalSessionImporter(string) (agenttypes.ExternalSessionImporter, error) {
	return nil, errors.New("not implemented")
}

func (uploadTestRegistry) GetProber() *agent.Prober {
	return nil
}

func (uploadTestRegistry) GetCandidateRegistry() *CandidateRegistry {
	return nil
}

func (uploadTestRegistry) GetFileWatcher(string, *session.Manager) (*rootfs.SharedFileWatcher, error) {
	return nil, nil
}

func (uploadTestRegistry) ReleaseFileWatcher(string, string) {}

func TestCancelSessionTurnCancelsTransientActiveTurn(t *testing.T) {
	rootDir := t.TempDir()
	root := rootfs.NewRootInfo("mindfs", "mindfs", rootDir)
	manager := session.NewManager(root)
	service := Service{Registry: &commandTestRegistry{root: root, manager: manager}}
	sessionKey := "transient-login-test"
	turnCtx, cancel := context.WithCancel(context.Background())
	fakeSession := &fakeUsecaseAgentSession{}
	registerActiveTurn(root.ID, sessionKey, cancel)
	setActiveTurnSession(root.ID, sessionKey, fakeSession)
	defer unregisterActiveTurn(root.ID, sessionKey)

	if err := service.CancelSessionTurn(context.Background(), CancelSessionTurnInput{
		RootID: root.ID,
		Key:    sessionKey,
	}); err != nil {
		t.Fatalf("CancelSessionTurn returned error: %v", err)
	}
	if turnCtx.Err() == nil {
		t.Fatal("expected transient turn context to be canceled")
	}
	if fakeSession.cancelCalls != 1 {
		t.Fatalf("CancelCurrentTurn calls = %d, want 1", fakeSession.cancelCalls)
	}
}

// 取消只能作用于「请求发起时正在跑的那一轮」。
//
// 取消是异步落地的（ws.go 怕阻塞读循环用 goroutine 派发），从用户点到执行可以隔
// 几百毫秒；这期间同一会话完全可能已经开了新一轮（用户点完停止紧接着发消息）。
// 2026-10-02 实测：用户在空闲会话上点停止，随后发的消息刚 send.begin 就被这个迟到的
// 取消掐掉，dsh 表现为「733ms 完成、什么都没吐」。
func TestCancelSessionTurnDropsStaleTargetTurn(t *testing.T) {
	rootDir := t.TempDir()
	root := rootfs.NewRootInfo("mindfs", "mindfs", rootDir)
	manager := session.NewManager(root)
	service := Service{Registry: &commandTestRegistry{root: root, manager: manager}}
	sessionKey := "transient-login-stale"
	defer unregisterActiveTurn(root.ID, sessionKey)

	// 第一轮：用户点停止时正在跑的就是它。
	oldCtx, oldCancel := context.WithCancel(context.Background())
	oldSession := &fakeUsecaseAgentSession{}
	registerActiveTurn(root.ID, sessionKey, oldCancel)
	setActiveTurnSession(root.ID, sessionKey, oldSession)
	staleTarget := ActiveTurnID(root.ID, sessionKey)
	if staleTarget == 0 {
		t.Fatal("ActiveTurnID returned 0 for a registered turn")
	}
	// 它自己在取消落地前结束了，紧接着来了新一轮。
	unregisterActiveTurn(root.ID, sessionKey)
	oldCancel()
	newCtx, newCancel := context.WithCancel(context.Background())
	newSession := &fakeUsecaseAgentSession{}
	registerActiveTurn(root.ID, sessionKey, newCancel)
	setActiveTurnSession(root.ID, sessionKey, newSession)

	if err := service.CancelSessionTurn(context.Background(), CancelSessionTurnInput{
		RootID:       root.ID,
		Key:          sessionKey,
		TargetTurnID: staleTarget,
		TargetPinned: true,
	}); err != nil {
		t.Fatalf("CancelSessionTurn returned error: %v", err)
	}
	if newCtx.Err() != nil {
		t.Fatal("迟到的取消掐掉了新一轮的 context")
	}
	if newSession.cancelCalls != 0 {
		t.Fatalf("新一轮被误伤：CancelCurrentTurn calls = %d, want 0", newSession.cancelCalls)
	}

	// 决定取消时根本没在跑（id=0）也必须丢弃：空闲时点的停止不能落到之后起来的新轮上。
	if err := service.CancelSessionTurn(context.Background(), CancelSessionTurnInput{
		RootID:       root.ID,
		Key:          sessionKey,
		TargetTurnID: 0,
		TargetPinned: true,
	}); err != nil {
		t.Fatalf("CancelSessionTurn returned error: %v", err)
	}
	if newCtx.Err() != nil || newSession.cancelCalls != 0 {
		t.Fatal("空闲时刻的取消落到了新轮上")
	}

	// id 对得上时照常取消：不能因为加了护栏就把取消本身废掉。
	if err := service.CancelSessionTurn(context.Background(), CancelSessionTurnInput{
		RootID:       root.ID,
		Key:          sessionKey,
		TargetTurnID: ActiveTurnID(root.ID, sessionKey),
		TargetPinned: true,
	}); err != nil {
		t.Fatalf("CancelSessionTurn returned error: %v", err)
	}
	if newCtx.Err() == nil {
		t.Fatal("id 匹配时应当取消")
	}
	if newSession.cancelCalls != 1 {
		t.Fatalf("CancelCurrentTurn calls = %d, want 1", newSession.cancelCalls)
	}
	_ = oldCtx
}

// 同一 sessionKey 的重叠登记：旧一轮的注销不能带走新一轮的登记。
//
// 现场是「停止按钮点了没反应」的上半段：重复 attach / compact 重入让同一个
// sessionKey 被登记两次，旧 close 只按 key 注销，把新一轮的登记一起删掉；
// 之后的停止请求拿到的 target_turn 恒为 0，CancelSessionTurn 在 active==nil
// 分支静默返回（线上 1795584428）。
func TestUnregisterActiveTurnOnlyRemovesMatchingID(t *testing.T) {
	rootID := "root-" + t.Name()
	sessionKey := "session-" + t.Name()
	idOld := registerActiveTurn(rootID, sessionKey, func() {})
	idNew := registerActiveTurn(rootID, sessionKey, func() {})
	defer unregisterActiveTurn(rootID, sessionKey, idNew)
	if idOld == 0 || idNew == 0 {
		t.Fatalf("registerActiveTurn ids = %d/%d, want non-zero", idOld, idNew)
	}
	if idOld == idNew {
		t.Fatalf("两次登记拿到了同一个 id %d：身份无法区分先后两轮", idOld)
	}

	unregisterActiveTurn(rootID, sessionKey, idOld)
	if got := ActiveTurnID(rootID, sessionKey); got != idNew {
		t.Fatalf("旧一轮注销后 ActiveTurnID = %d, want %d（旧登记删掉了新登记，停止按钮将永远 target_turn=0）", got, idNew)
	}

	unregisterActiveTurn(rootID, sessionKey, idNew)
	if got := ActiveTurnID(rootID, sessionKey); got != 0 {
		t.Fatalf("新一轮注销后 ActiveTurnID = %d, want 0", got)
	}
}
