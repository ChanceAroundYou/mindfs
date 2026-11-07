package deploy

// ReleaseRepo is the GitHub repository this build publishes to and checks for updates from.
//
// 它曾是上游 `a9gent/mindfs`：四个节点的自动更新检查因此挂在上游的发布通道上，
// 界面显示「可更新到 v0.5.9」，而那个 release 带 relay 远程模式与端到端加密 —— 我们早已整体删掉。
// 挡住自动安装的只有「release 校验公钥未配置」这一条意外状态，不是设计。
//
// 身份必须只在这一处定义：`server/app` 的两处 `update.NewService`、Makefile 的发布/签名参数、
// 以及 `scripts/install.sh|ps1` 的下载源都由它派生。加第二处字面量就是给未来的半吊子 repoint 开门
// （web/tests/agent9-traces.test.mjs 钉这条一致性）。
const ReleaseRepo = "ChanceAroundYou/mindfs"

// ReleaseBaseURL returns the releases root for this build's repository.
func ReleaseBaseURL() string {
	return "https://github.com/" + ReleaseRepo + "/releases"
}
