// Package nodeinfo describes what a single MindFS instance is responsible for.
//
// 一个 mindfs 实例同时有两类职责：
//
//   - 控制面（control）：账户表、偏好、提示词、看板模板、节点表、WebPush 订阅、
//     relay/e2ee 绑定、应用更新。**全局只应有一份真相**，由主节点持有。
//   - 数据面（worker 也要有）：项目列表、会话库、任务库、文件读写、git、agent
//     进程池、定时任务。这些天然按机器分，每台机器各一份是对的。
//
// 两类混在一起、每台机器都完整跑一遍，是控制面副本漂移的根源（同一个物理节点在
// 两边拿到不同 id、模板两边各有一半）。本包只做一件事：声明「这台机器是什么角色」，
// 以及「哪些路径属于控制面」，供路由层拒绝越权请求。
package nodeinfo

import "strings"

// Role 是节点角色。零值（""）等价于 RoleControl，保证不配置就是改造前的行为。
type Role string

const (
	// RoleControl 是主节点：serve 前端 + 控制面 + 数据面。默认值。
	RoleControl Role = "control"
	// RoleWorker 是运行节点：只提供数据面。控制面端点与静态资源一律 403。
	RoleWorker Role = "worker"
)

// Normalize 把外部输入（命令行 flag / 启动配置 JSON）收敛成合法角色。
// 未知值一律回落到 RoleControl —— 猜错成 worker 会让节点丧失全部 UI，
// 猜错成 control 只是多提供几个没人用的端点，前者危险得多。
func Normalize(value string) Role {
	switch Role(strings.ToLower(strings.TrimSpace(value))) {
	case RoleWorker:
		return RoleWorker
	default:
		return RoleControl
	}
}

// IsWorker 判断是否运行节点。零值按 control 处理（向后兼容）。
func (r Role) IsWorker() bool { return r == RoleWorker }

// controlPlanePrefixes 是「只有主节点才提供」的路径前缀。
//
// 刻意**不含**数据面（/api/dirs、/api/sessions、/api/tasks、/api/tree、/api/file、
// /api/git/*）、/health、/ws：worker 照常提供这些，否则它无法执行任何任务。
//
// 同样**不含** agent 配置类端点（/api/agents、/api/agent-config、
// /api/agent-api-providers、/api/agents/*）：它们改的是**本机 agent 运行时**，
// 不是全局控制面，worker 需要它们才能跑任务。
var controlPlanePrefixes = []string{
	"/api/auth",
	"/api/users",
	"/api/preferences",
	"/api/prompts",
	"/api/task-templates",
	"/api/task-stage-templates",
	"/api/web-push",
	"/api/nodes",
	"/api/node-info",
	"/api/relay",
	"/api/e2ee",
	"/api/token-station",
	"/api/app/update",
}

// ControlPlanePrefixes 返回控制面前缀表（副本，调用方改它不影响包内状态）。
func ControlPlanePrefixes() []string {
	out := make([]string, len(controlPlanePrefixes))
	copy(out, controlPlanePrefixes)
	return out
}

// IsControlPlane 判断一个（已剥掉部署前缀的）请求路径是否属于控制面。
//
// 用「路径段前缀」而不是裸字符串前缀：/api/users 与 /api/users-and-more 这类
// 不存在，但 /api/preferences 与 /api/preferences-extra 会。段边界比字符串前缀严谨。
func IsControlPlane(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	for _, prefix := range controlPlanePrefixes {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}
