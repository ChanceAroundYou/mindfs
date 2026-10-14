# 后端 reviewer 执行指南（长期规则）

> 每次后端改动按本表打勾。任一红灯项不通过即挡合并。

## 红灯（BLOCK）

- [ ] `ws.go` 的每个 request handler 都必须通过 goroutine 派发；AST 回归测试 `TestWSRequestHandlersAreDispatchedAsynchronously` 扫描 `handleWSRequest` 中全部 `h.handle*` 调用。
- [ ] **Interrupt/Cancel 路径**无裸 `context.Background()`（必须 `WithTimeout` 包装）。
      注意：定时任务、后台轮询、进程生命周期等场景用 `Background()` 是合法的，不在本条约束内。
- [ ] 无新增 `h.AppContext` 直构造（一处不改，188 处只减不增；新 handler 走 ScopedRouter）。
- [ ] 新增端点已定控制面/数据面归属；控制面不在 `ControlPlanePrefixes()` 漏网，
      数据面不在表内（`role_test.go` 会卡）。
- [ ] SQLite 变更只增不改；提交信息注明库文件与回滚方式。
- [ ] 新增 handler/usecase 同批有测试；三类契约测试未降级。

## 黄灯（WARN）

- [ ] 文件名 = 主题名单数；`http_*.go` 一域一文件，无跨域 handler 混装。
- [ ] 错误经 `apperr` 构造，无裸拼用户可见文案。
- [ ] 超 1500 行文件有拆分 TODO；`manager.go` 新增逻辑优先拆 `manager_*.go`。

## 快速自查命令

> 以下 grep 是**候选筛查**，不是判定器——命中项需人工确认是否真缺超时包装
> （`WithTimeout(context.Background(), …)` 是合规写法，也会被 grep 命中）。

```bash
# 同步派发（应无输出）
grep -rn "[^o] h\.handle" server/internal/api/ws.go
# Interrupt/Cancel 路径的 Background（候选，人工确认是否有 WithTimeout）
grep -rn "func.*CancelCurrentTurn" server/internal --include="*.go" -A15 \
  | grep -v _test | grep "context.Background()"
# AppContext 直构造（应无输出）
grep -rn "AppContext{" server/internal/api --include="*.go" | grep -v _test
```

## 自动覆盖映射

- ws handler 异步派发：`server/internal/api/ws_test.go:TestWSRequestHandlersAreDispatchedAsynchronously`（`make test`）。
- Interrupt/Cancel 超时：`server/internal/agent/cancel_rpc_timeout_guard_test.go`（`make test`）。
- `AppContext` 直接构造与访问面增长、Go 文件硬行数：`server/internal/api/workspace_test.go`（`make test`）。
- 控制面/数据面端点表：`server/internal/nodeinfo/role_test.go`、`server/internal/api/http_node_role_test.go`（`make test`）。
- SQLite 只增不改、回滚说明、新 handler/usecase 同批行为测试、错误构造与文件分域：仍需逐次 reviewer 人工核对。
