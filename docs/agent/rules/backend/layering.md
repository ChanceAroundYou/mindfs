# 后端分层与并发铁律（长期规则）

```
WS/HTTP (api/) → usecase/ → session/kanban/等域包 → SQLite (WAL)
```

## 并发铁律（BLOCK 级，来自 AGENTS.md，经实测）

1. **ws.go 单读 goroutine**：每个连接只有一个读 goroutine，所有 handler 必须 `go h.handleXxx(...)`
   异步派发。同步派发会冻结整个连接（`session.cancel` 曾因此"停止按钮失效"）。
2. **取消路径必须加超时**：agent session 的取消（`Interrupt`/`Cancel`）是跨进程 RPC，
   对端不 ack 会永久挂起（SDK 用 `context.Background()`）；调用方必须 `WithTimeout` 包装。
   定时任务、后台轮询、进程生命周期等场景用 `Background()` 是合法的，不在本条约束内。
   守卫：`server/internal/agent/cancel_rpc_timeout_guard_test.go`。
3. **共享实例禁双跑**：共享服务（偏好/节点表/update）用同一份实例；
   按账户分的（看板/定时任务/session manager）每账户一份。同一份文件两份实例会双跑调度。

## 分层铁律

1. `api/` 只做参数解析 + 派发，禁止业务逻辑下沉到 handler。
2. `usecase/` 禁止直调其他域的存储细节，只调各域包的公开 `Service`/`Manager` 方法。
3. `cli/cmd` 不许 import `server/internal/*`（Go internal 规则）；`Role` 以 string 透传，
   归一化在 `app.Start`。
