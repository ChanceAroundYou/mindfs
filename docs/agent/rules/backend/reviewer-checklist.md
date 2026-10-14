# 后端 Reviewer 执行指南

每次后端改动都必须完成本表。自动门禁失败或人工检查缺少要求的证据时，改动不得合并；没有按路径、历史状态或 reviewer 判断覆盖规则的机制。

## 必须检查

- [ ] `ws.go` 中每个 request handler 都经 goroutine 派发；由 `TestWSRequestHandlersAreDispatchedAsynchronously` 检查。
- [ ] `Interrupt`/`Cancel` 路径的阻塞调用有明确超时；相关回归测试必须保持有效。
- [ ] 新增 handler 从 `ScopedRouter` 获取请求账户对应的 context，不直接构造应用 context。
- [ ] 新增端点明确属于控制面或数据面，并由对应角色路由测试覆盖。
- [ ] SQLite 变更记录回滚方式；新 handler/usecase 同批提供行为测试。
- [ ] 所有手写 Go 文件符合统一长度规则；文件名与包职责相符，错误经 `apperr` 构造。

## 快速自查命令

```bash
# 同步派发候选位置
grep -rn "[^o] h\.handle" server/internal/api/ws.go
# AppContext 直构造候选位置
grep -rn "AppContext{" server/internal/api --include="*.go" | grep -v _test
```

这些命令用于定位需复核的代码；最终判定以行为测试和调用上下文为准。

## 自动覆盖映射

- WS handler 异步派发：`server/internal/api/ws_test.go`。
- Interrupt/Cancel 超时：`server/internal/agent/cancel_rpc_timeout_guard_test.go`。
- 请求账户路由与应用 context 生命周期：`server/internal/api/workspace_test.go`。
- 控制面/数据面端点表：`server/internal/nodeinfo/role_test.go` 与 API role 测试。
- Go 文件长度由 `make test` 中的长度门禁覆盖；回滚说明、端点归属与错误文案由 reviewer 检查并记录依据。
