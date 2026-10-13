# 后端命名规范（长期规则）

| 对象 | 规则 | 例 |
|---|---|---|
| 包名 | 全小写单数，域名词 | `session` `kanban` `gitview` `usecase` |
| 文件 | `snake_case.go`，一域/一主题一文件 | `session_repoint.go` `http_tasks.go` |
| 测试 | 同目录 `xxx_test.go`，与被测文件同名 | `manager_test.go` `session_repoint_test.go` |
| 导出类型 | `PascalCase`，接口以能力命名 | `Session` `Manager` `Service` |
| 导出函数 | `PascalCase` 动词开头 | `CreateTask` `RepointAgentBinding` |
| 未导出 | `camelCase` | `loadForMove` `pruneChildState` |
| 错误 | `apperr` 统一构造，禁止裸 `errors.New` 拼用户可见文案 | — |
| Handler | `handle` + 资源 + 动作 | `handleSessionMessage` `handleSessionCancel` |

三条铁律：① 文件名 = 主题名单数；② 一个文件一个主题（`http.go` 只做路由注册，
各域 handler 分 `http_*.go`）；③ 测试文件名 = 被测文件名 + `_test`。
