package usecase

import (
	"context"
	"strings"
	"testing"

	rootfs "mindfs/server/internal/fs"
	"mindfs/server/internal/session"
)

type compactCancelTestRegistry struct {
	commandTestRegistry
	tracker *HygieneTracker
}

func (r *compactCancelTestRegistry) SessionHygiene() *HygieneTracker { return r.tracker }

// T2：用户点停止时如果目标正是溢出压缩回合，拒绝取消 —— 否则 finishHygieneCompact
// 会把 aborted 记成失败并丢掉 G-QT 的自动重发待办。
func TestPinnedCancelRejectedWhileOverflowCompactPending(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	tracker := NewHygieneTracker(hygieneConfig())
	tracker.MarkCompactPending(root.ID, "transient-compact-cancel")
	svc := &Service{Registry: &compactCancelTestRegistry{
		commandTestRegistry: commandTestRegistry{root: root, manager: session.NewManager(root)},
		tracker:             tracker,
	}}

	err := svc.CancelSessionTurn(context.Background(), CancelSessionTurnInput{
		RootID:       root.ID,
		Key:          "transient-compact-cancel",
		TargetTurnID: 7,
		TargetPinned: true,
	})
	if err == nil || !strings.Contains(err.Error(), "压缩中") {
		t.Fatalf("压缩中 pinned cancel 应被拒绝并说明原因，得到 %v", err)
	}
}

// T2：内部取消（task/workflow 收尾、队列清理）没有 pinned 目标，不能受压缩闸门影响。
func TestUnpinnedCancelStillWorksWhileCompactPending(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	key := "transient-internal-cancel"
	tracker := NewHygieneTracker(hygieneConfig())
	tracker.MarkCompactPending(root.ID, key)
	svc := &Service{Registry: &compactCancelTestRegistry{
		commandTestRegistry: commandTestRegistry{root: root, manager: session.NewManager(root)},
		tracker:             tracker,
	}}
	cancelled := false
	registerActiveTurn(root.ID, key, func() { cancelled = true })
	defer unregisterActiveTurn(root.ID, key)

	if err := svc.CancelSessionTurn(context.Background(), CancelSessionTurnInput{RootID: root.ID, Key: key}); err != nil {
		t.Fatalf("内部取消被压缩闸门误拦：%v", err)
	}
	if !cancelled {
		t.Fatal("内部取消没有真的取消回合")
	}
}
