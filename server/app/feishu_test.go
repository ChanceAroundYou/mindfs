package app

import (
	"os"
	"path/filepath"
	"testing"
)

// 卡片状态文件读不回来时不能装作没事继续跑：空的仓库会忘掉哪些卡片已经答过，
// 同一张 gate 卡重启后再点一次就被当成新的点击。宁可这次不启动通道 ——
// 文件保持原样，人修好再重启即可。
func TestNewFeishuChannelRefusesToStartOnCorruptState(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "feishu"), 0o700); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(dir, "feishu", "cards.json")
	if err := os.WriteFile(statePath, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := newFeishuChannel(FeishuOptions{Enabled: true, OwnerOpenID: "ou_owner"}, dir)
	if svc != nil {
		t.Fatal("channel started on a corrupt card store")
	}
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("corrupt file must be left in place for inspection: %v", err)
	}
}
