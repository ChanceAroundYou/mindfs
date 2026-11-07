package deploy

import "testing"

// 发布身份必须是本 fork 自己的仓库。这条不是风格问题：`server/app` 的自动更新服务、
// `make release` 的签名清单、`scripts/install.sh|ps1` 的下载源全都读它。
// 它曾经写着上游仓库，于是四个节点的更新检查挂在别人的发布通道上，
// 界面报「可更新到 v0.5.9」——那个 release 带我们早已删掉的 relay 远程模式。
func TestReleaseRepoIsThisForksOwnRepository(t *testing.T) {
	if ReleaseRepo != "ChanceAroundYou/mindfs" {
		t.Fatalf("ReleaseRepo = %q, want the fork's own repository", ReleaseRepo)
	}
	if got, want := ReleaseBaseURL(), "https://github.com/ChanceAroundYou/mindfs/releases"; got != want {
		t.Fatalf("ReleaseBaseURL() = %q, want %q", got, want)
	}
}
