package app

import (
	"os"
	"strings"

	"mindfs/server/internal/nodeinfo"
)

// 主控的缺省身份：id 参与 scope 键与持久化，名字只用于展示。
//
// `local` 曾把「当前页面所在设备」当成一台叫 local 的机器，主控和边缘都叫 local；
// 现在每台机器自报身份，主控固定是 home / 主控。
const (
	PrimaryNodeID   = "home"
	PrimaryNodeName = "主控"
)

// resolveNodeIdentity 决定本机在节点表与前端里的身份。
//
// 顺序：显式 node.id/name > 边缘的 control_sync.instance_id > 主机名 > 主控缺省。
// 运行节点（role=worker）不提供前端，但仍然要有一个稳定的 id/name，
// 否则 node-info 回给前端的是主控的身份，跨节点调试会认错机器。
//
// 放在 app 而不是各入口：cli/cmd 与 server/cmd/mindfs-server 读同一份 config.json，
// 两处各写一遍就会出现「开发服务器认成另一台机器」。
func resolveNodeIdentity(opts StartOptions) (string, string) {
	id := strings.TrimSpace(opts.NodeID)
	name := strings.TrimSpace(opts.NodeName)

	instance := ""
	edge := false
	if opts.ControlSync != nil {
		instance = strings.TrimSpace(opts.ControlSync.InstanceID)
		edge = strings.EqualFold(strings.TrimSpace(opts.ControlSync.Role), "edge")
	}
	worker := nodeinfo.Normalize(opts.Role).IsWorker()
	host, _ := os.Hostname()
	host = strings.TrimSpace(host)

	fallbackID, fallbackName := PrimaryNodeID, PrimaryNodeName
	switch {
	case edge && instance != "":
		fallbackID, fallbackName = instance, instance
	case (edge || worker) && host != "":
		fallbackID, fallbackName = host, host
	case worker:
		fallbackID, fallbackName = "worker", "worker"
	}

	if id == "" {
		id = fallbackID
	}
	if name == "" {
		name = fallbackName
	}
	return id, name
}
