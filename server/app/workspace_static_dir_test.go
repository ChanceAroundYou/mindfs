package app

import (
	"testing"
)

// 静态目录的「运行期可变」要一路传进 handler：中间任何一跳丢掉 provider，
// 镜像我行我素地切版本，页面却永远出安装目录那份 —— 那种缺陷在线上只表现为
// 「更新了但页面没变」，没有任何报错。
func TestHandlerDefaultsCarryStaticDirProvider(t *testing.T) {
	m := &workspaceManager{}
	m.SetHandlerDefaults("/install/web", nil)
	m.SetStaticDirProvider(func() string { return "/mirror/v1" })

	staticDir, provider, _, _ := m.handlerDefaults()

	if staticDir != "/install/web" {
		t.Fatalf("安装目录 = %q", staticDir)
	}
	if provider == nil {
		t.Fatal("provider 没被带出来：SetStaticDirProvider 之后 handlerDefaults 仍是 nil")
	}
	if got := provider(); got != "/mirror/v1" {
		t.Fatalf("provider() = %q", got)
	}
}
