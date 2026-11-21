package controlsync

import "strings"

// pathDomains 把控制面路径映射到同步域。
//
// 只列**参与同步**的域：WebPush 订阅、节点信息、token-station、应用更新都不出本机
// （设备专属凭据与订阅端点，复制过去只会制造假数据），它们在转发层同样是「留在本机」
// 的例外，不会走到这里。
var pathDomains = []struct {
	prefix string
	domain string
}{
	{"/api/users", DomainUsers},
	{"/api/preferences", DomainPreferences},
	{"/api/pins", DomainPins},
	{"/api/prompts", DomainPrompts},
	{"/api/workflows", DomainWorkflows},
	{"/api/nodes", DomainNodes},
}

// DomainOfControlPath 返回一条（已剥掉部署前缀的）路径所属的同步域。
//
// ok=false 有两种含义，调用方按场景区分：数据面路径，或「属于控制面但不参与同步」
// 的路径（/api/web-push 等）。两者在冻结判断里都是「不按域冻结」。
func DomainOfControlPath(path string) (string, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", false
	}
	for _, pd := range pathDomains {
		if path == pd.prefix || strings.HasPrefix(path, pd.prefix+"/") {
			return pd.domain, true
		}
	}
	return "", false
}
