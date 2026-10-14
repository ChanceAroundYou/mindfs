# 后端目录划分（长期规则）

```
server/
├── app/                每账户 AppContext 惰性构建与缓存（workspace.go）+ 服务装配（server.go）
│                       ScopedRouter 按 user= 派发。禁业务逻辑下沉
├── internal/api/       HTTP 路由（http.go）+ WS 连接管理与分发（ws.go）+ 各域 handler（http_*.go）
│   └── usecase/        业务逻辑层（session CRUD、task、scheduled）。handler 只做参数解析与派发
├── internal/session/   Session 生命周期（manager.go，SQLite WAL）。核心热路径
├── internal/agent/     Agent 运行时抽象：types/（接口）+ pool.go（池）+ protocol.go（发现）
│                       + claude/ + codex/ + acp/（各后端实现）
├── internal/kanban/    看板任务（自带流水快照，预设解耦，无并发调度）
├── internal/<domain>/  fs / gitview / scheduled / preferences / auth / notify /
│                       relay / e2ee / config / update / pins / nodes / webpush …各守一域
└── cmd/mindfs-server/  后端入口（仅 make dev-backend 用；发布二进制是 cli/cmd）
cli/cmd/                发布二进制入口 + 启动配置（StartOptions.Role 为 string，
                        归一化在 app.Start；internal 不可 import，Go internal 规则）
```

## 三条判据

1. **请求走哪**：HTTP/WS 进 `api/` → 参数解析后调 `usecase/` → `usecase/` 调 `session/`/`kanban/` 等域包。
   handler 禁止直调 `manager.go` 细节，禁止跨域包直调。
2. **账户走哪**：新增 handler 只从 `ScopedRouter` 获取本账户实例；
   StreamHub 每账户独立，禁跨账户共享。
3. **控制面/数据面**：`nodeinfo.ControlPlanePrefixes()` 是唯一判据；新增端点必须先定面，
   控制面走 `controlPath`，数据面跟 `nodeId`。`role_test.go` 钉住"数据面端点不在表内"。
