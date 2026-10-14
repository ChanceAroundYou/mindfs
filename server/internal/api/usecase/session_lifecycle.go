package usecase

import (
	"context"
	"errors"
	"log"
	"strings"

	"mindfs/server/internal/commandexec"
	"mindfs/server/internal/fs"
	"mindfs/server/internal/session"
)

func (s *Service) CloseSession(ctx context.Context, in CloseSessionInput) (*session.Session, error) {
	if err := s.ensureRegistry(); err != nil {
		return nil, err
	}
	manager, err := s.Registry.GetSessionManager(in.RootID)
	if err != nil {
		return nil, err
	}
	closed, err := manager.Close(ctx, in.Key)
	if err != nil {
		return nil, err
	}
	if pool := s.Registry.GetAgentPool(); pool != nil && closed != nil {
		for agentName := range closed.AgentCtxSeq {
			pool.Close(agentPoolSessionKey(closed.Key, agentName))
		}
	}
	s.Registry.ReleaseFileWatcher(in.RootID, in.Key)
	return closed, nil
}

type DeleteSessionInput struct {
	RootID string
	Key    string
}

// DeleteSession 删除会话**整棵子树**，返回被删掉的 key 集合（父 + 全部子会话）。
//
// 返回 key 列表是给调用方的：上层要拿它去解绑任务引用（Task.MainSessionKey /
// StageRun.SessionKey），否则任务会一直指向一个已不存在的会话。
func (s *Service) DeleteSession(ctx context.Context, in DeleteSessionInput) ([]string, error) {
	if err := s.ensureRegistry(); err != nil {
		return nil, err
	}
	root, err := s.Registry.GetRoot(in.RootID)
	if err != nil {
		return nil, err
	}
	manager, err := s.Registry.GetSessionManager(in.RootID)
	if err != nil {
		return nil, err
	}
	keys, err := deleteSessionCascadeKeys(ctx, manager, in.Key)
	if err != nil {
		return nil, err
	}
	if err := s.deleteSessionKeys(ctx, root, manager, keys); err != nil {
		return nil, err
	}
	return keys, nil
}

// deleteSessionKeys 真删一批会话的完整清理序列：先掐断在跑的 turn，再逐个收资源。
// 归档要删子树时也走这里 —— 归档过的子会话和普通删除在资源层面没有区别。
func (s *Service) deleteSessionKeys(
	ctx context.Context,
	root fs.RootInfo,
	manager *session.Manager,
	keys []string,
) error {
	for _, key := range keys {
		cancelActiveSessionTurn(root.ID, key)
	}
	for _, key := range keys {
		cancelActiveSessionTurn(root.ID, key)
		if err := manager.Delete(ctx, key); err != nil {
			return err
		}
		if err := root.RemoveSessionFileMeta(key); err != nil {
			return err
		}
		commandexec.CloseSession(root.ID, key)
		s.Registry.ReleaseFileWatcher(root.ID, key)
	}
	return nil
}

// sessionSubtreeKeys 收集 key 及其全部后代（子会话）的 key 集合。
//
// 删除和归档共用：两者都是「整棵树一起处理」，区别只在最后一步是删还是打归档标记。
// 按 ParentSessionKey 遍历，fork 会话不在其中（fork 只是 source 里记了来源，不是父子关系）。
func sessionSubtreeKeys(ctx context.Context, manager *session.Manager, key string) ([]string, error) {
	rootKey := strings.TrimSpace(key)
	if rootKey == "" {
		return nil, errors.New("session key required")
	}
	items, err := manager.ListMetas(ctx)
	if err != nil {
		return nil, err
	}
	childrenByParent := make(map[string][]string)
	exists := false
	for _, item := range items {
		if item == nil {
			continue
		}
		itemKey := strings.TrimSpace(item.Key)
		if itemKey == "" {
			continue
		}
		if itemKey == rootKey {
			exists = true
		}
		parentKey := strings.TrimSpace(item.ParentSessionKey)
		if parentKey != "" {
			childrenByParent[parentKey] = append(childrenByParent[parentKey], itemKey)
		}
	}
	if !exists {
		return nil, errors.New("session not found")
	}
	keys := make([]string, 0, 1)
	seen := make(map[string]bool)
	var visit func(string)
	visit = func(current string) {
		if seen[current] {
			return
		}
		seen[current] = true
		for _, childKey := range childrenByParent[current] {
			visit(childKey)
		}
		keys = append(keys, current)
	}
	visit(rootKey)
	return keys, nil
}

// deleteSessionCascadeKeys 保留旧名：删除用例用它，语义就是「要删的整棵子树」。
func deleteSessionCascadeKeys(ctx context.Context, manager *session.Manager, key string) ([]string, error) {
	return sessionSubtreeKeys(ctx, manager, key)
}

type ArchiveSessionInput struct {
	RootID string
	Key    string
	// Archived = true 归档（自己），false 取消归档。
	Archived bool
}

type ArchiveSessionOutput struct {
	// Session 是被归档/取消归档的那个会话本身（请求的那个 key）。
	Session *session.Session
	// DeletedKeys 是随这次归档被**删掉**的子会话。父会话只被标记，子会话是真删除 ——
	// 两种不同的状态：父会话留在归档区，子会话从主面板和归档区都消失。
	DeletedKeys []string
}

// ArchiveSession 归档一个会话：**只把它自己**打归档标记，它整棵子树的子会话**直接删除**。
//
// 为什么子会话是删而不是跟着归档：归档区按项目分组平铺，子会话挂在那儿只会变成
// 孤儿行（父被折叠就找不到，或撑成两层）。而归档本来就不该保留过程噪声 —— 用户
// 归档的是「这段对话到此为止」，子会话是它内部的展开步骤，不值得单独占一行。
//
// 与 DeleteSession 的差别在**对象**：删除要收掉整棵树（包括被点的那个），归档只删
// 后代、被点的那个只打标记不动内容（深链接能打开、还能继续跑）。
//
// 取消归档维持原样（整棵子树一起解标记）：删除不可逆，逆操作没法补偿，只解标记
// 是当前能给出的最接近还原的行为。
func (s *Service) ArchiveSession(ctx context.Context, in ArchiveSessionInput) (ArchiveSessionOutput, error) {
	if err := s.ensureRegistry(); err != nil {
		return ArchiveSessionOutput{}, err
	}
	root, err := s.Registry.GetRoot(in.RootID)
	if err != nil {
		return ArchiveSessionOutput{}, err
	}
	manager, err := s.Registry.GetSessionManager(in.RootID)
	if err != nil {
		return ArchiveSessionOutput{}, err
	}
	self := strings.TrimSpace(in.Key)
	keys, err := sessionSubtreeKeys(ctx, manager, self)
	if err != nil {
		return ArchiveSessionOutput{}, err
	}
	if !in.Archived {
		// 取消归档：整棵子树一起解标记。
		for _, key := range keys {
			if _, err := manager.SetArchived(ctx, key, false); err != nil {
				return ArchiveSessionOutput{}, err
			}
		}
		updated, err := manager.SetArchived(ctx, self, false)
		if err != nil {
			return ArchiveSessionOutput{}, err
		}
		return ArchiveSessionOutput{Session: updated}, nil
	}
	// 归档：先删子树（删的是后代，自己不在里面），再给自己打标记。
	// 顺序反了会出问题 —— 删完子树后 self 的后代链已不存在，但 self 本身仍在，
	// 两种顺序都能跑；先删是为了中途失败时不留下「已归档但子会话还在」的窗口。
	children := make([]string, 0, len(keys))
	for _, key := range keys {
		if key != self {
			children = append(children, key)
		}
	}
	if len(children) > 0 {
		if err := s.deleteSessionKeys(ctx, root, manager, children); err != nil {
			return ArchiveSessionOutput{}, err
		}
	}
	updated, err := manager.SetArchived(ctx, self, true)
	if err != nil {
		return ArchiveSessionOutput{}, err
	}
	return ArchiveSessionOutput{Session: updated, DeletedKeys: children}, nil
}

func cancelActiveSessionTurn(rootID, sessionKey string) {
	active := getActiveTurn(rootID, sessionKey)
	if active == nil {
		return
	}
	active.cancel()
	if active.session != nil {
		if err := active.session.CancelCurrentTurn(); err != nil {
			log.Printf("[session] turn.cancel.error root=%s session=%s err=%v", rootID, sessionKey, err)
		}
	}
}

type RenameSessionInput struct {
	RootID string
	Key    string
	Name   string
}

func (s *Service) RenameSession(ctx context.Context, in RenameSessionInput) (*session.Session, error) {
	if err := s.ensureRegistry(); err != nil {
		return nil, err
	}
	manager, err := s.Registry.GetSessionManager(in.RootID)
	if err != nil {
		return nil, err
	}
	return manager.Rename(ctx, in.Key, in.Name)
}
