# MindFS 多账户（用户管理模块）PRD

> 状态：**已实施完毕**（P1–P4 全部落地，P5 因设计变更取消）· 起草 2026-09-17
> 前置：单密码页面闸门 `8b59720`
> 决策者：用户（2026-09-17）——四条约束见 §1

## 1. 已确认的约束

| 决策 | 取值 |
|---|---|
| 普通账户能否自己加项目目录 | **能**（各加各的，只分列表） |
| 角色 | 管理员 + 普通用户 两级 |
| 存量数据 | 全部归管理员账户 |
| **API 鉴权** | **不做，保持匿名；账户只在前端分区** |
| 改密码 | 管理员管所有 + 普通用户可改自己的 |

### 1.1 这个系统的性质，别搞错

**它是「多配置档」，不是「多用户」。** 类比 Chrome 的多 profile，不是 Linux 的多用户。

- **密码是装饰性的**：服务端按账户分区存储，就必须由客户端声明身份；API 匿名 ⇒ 服务端只能信声明 ⇒
  把请求里的 `user=` 改成别人的 id 就能读走全部项目与会话。**登录页绕得过去，而且不需要什么技巧。**
- 同理，它不是安全边界：mindfs 以单个 OS 用户运行、能读任意路径、能起 agent 执行 shell。

**因此：** 不要对外说它"隔离了用户"、不要拿它挡人、敏感机器不要因为"反正有登录"就暴露到公网。
真要隔人 → 一账户一 OS 用户一实例，那是另一件事，本方案不做。

## 2. 现状摸底（证据）

全部状态挂在**同一个扁平目录**，且**每一处都只经过 `config.MindFSConfigDir()` 定位**：

| 状态 | 路径 | 证据 |
|---|---|---|
| 项目列表 | `<cfg>/registry.json` | `fs/registry.go:31-38` |
| 偏好 | `<cfg>/preferences.json` | `preferences/store.go:240` |
| 节点 | `<cfg>/nodes.json` | `nodes/store.go:14,32` |
| 会话库 | `<cfg>/<rootID>/session-list.db` | `session/manager.go:2757-2767` |
| 看板 | `<cfg>/tasks/task-kanban.db` | `kanban/task_store.go:19` |
| WebPush 订阅 | `<cfg>/web-push-subscriptions.json` | `webpush/service.go:132` |
| 定时任务 | `<root meta>/scheduled-agent-tasks.json` | `scheduled/tasks.go:25` |

17 处调用 `MindFSConfigDir()`。长生命周期对象**全部在 `server/app/server.go:80-228` 单例构造**，
装进单个 `api.AppContext`（`api/appcontext.go:42-57`）。`api` 包内无跨用户全局量
（`projectedSessions sync.Map` 仅日志去重，键为全局唯一 session key，`http.go:1276-1288`）。

会话表无 owner 列（`session/manager.go:69-117`）。WS 广播无身份维度：
`BroadcastAll` 推给**所有连接**（`stream_hub.go:872-876`），会话级只按 session_key 过滤（`:363`）。

### 2.1 串号陷阱（必修）

元数据目录**不含账户维度**：

- `RootInfo.MetaDir()`（`fs/fs.go:197-209`）：项目内 meta → `<项目目录>/.mindfs`；home meta → `~/.mindfs/<rootID>`（`fs.go:123-131`，用 `os.UserHomeDir()`）
- 落到这里的：会话库、定时任务、file-meta

两个账户加同一目录 → 共用 meta → **会话直接串号**。
解法：meta 根按账户分（`~/.mindfs/users/<userID>/<rootID>`），项目内 meta 模式追加账户标识文件名
（`session-list.<userID>.db`），或对非 admin 账户禁用项目内 meta。

## 3. 设计

### 3.1 账户

`<cfg>/users.json`（0600）：

```json
{"users":[{"id":"u_xxx","username":"xkb","password_hash":"$2a$...","role":"admin",
           "created_at":"...","disabled":false}]}
```

- 口令 **bcrypt**（`golang.org/x/crypto` 已是直接依赖，零新增）
- **迁移**：首启若 `users.json` 不存在，用现有 `login.json` 的单密码建 `admin`，`login.json` 存为回退凭据
- 登录成功后服务端**只返回账户记录**，不签发 token（无 API 鉴权，token 无意义）

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/auth/login` | `{username,password}` → `{user:{id,username,role}}` |
| GET | `/api/users` | 账户列表 |
| POST | `/api/users` | 新建 |
| PUT | `/api/users/{id}` | 改密 / 禁用 / 改名 |
| DELETE | `/api/users/{id}` | 删除（**拒绝删除最后一个 admin**） |

全部匿名可访问——这是 §1.1 的直接后果，不是疏漏。

### 3.2 身份传递：客户端声明

服务端从请求里读账户 id，**不校验**：

- 请求：`?user=<userID>`（缺省 → admin，保证老客户端 / CLI / 定时任务不炸）
- WS：连接 URL 带 `?user=<userID>`
- 前端**单点注入**：`services/base.ts` 的 `appPath()` 是全部 fetch / WS / 资源 URL 的唯一汇聚点，
  在那里挂 `user=` 即可，12 处裸 fetch 与所有 `appURL/wsURL` 自动覆盖

### 3.3 每用户状态 = 每用户 config 目录

```
<cfg>/
  users.json
  users/<userID>/
    registry.json  preferences.json  nodes.json  web-push-subscriptions.json
    <rootID>/session-list.db
    tasks/task-kanban.db
```

**不复用 `user_id` 列**：十几张表逐个加列再逐个过滤，漏一处就是串号。
把目录做成账户的函数，store 逻辑一行不改。

代价：`fs.NewDefaultRegistry()` / `preferences.NewStore()` / `nodes.NewStore()` /
`kanban.*` / `webpush.*` / `scheduled.*` / `agent.NewPool()` 等构造函数需**显式接受目录参数**
（现为内部硬调 `MindFSConfigDir()`）。机械但必须做全——漏一处 = 两账户共用状态。

### 3.4 请求路由

```go
type WorkspaceManager struct {
    base   string                      // <cfg>/users
    mu     sync.Mutex
    byUser map[string]*api.AppContext  // 惰性构建
}
func (m *WorkspaceManager) For(userID string) (*api.AppContext, error) // 未知 id → 报错，不回退
```

- **每用户一份 `AppContext`**：registry / prefs / nodes / webpush / kanban / scheduled / **agent.Pool** / streamHub 随之独立
- **进程级共享**：relay（一机一隧道）、update（同一二进制）、notify script、e2ee（节点级）
- handler 从 `h.AppContext` 改为 `h.ws.For(claimedUserID)`
- **agent 池必须每用户独立**：`Pool` 按 `sessionKey` 复用子进程（`agent/pool.go:84-95`），
  共享池时 B 只要拿到 key 就能接管 A 的活跃 agent 会话
- 资源代价：N 账户 × M 会话 = N×M 个 claude 子进程，靠空闲释放（`IdleSessionResourceReleaseHours`）兜底

### 3.5 WebSocket

- 连接按 URL 上的 `user=` 绑定
- `BroadcastAll` → `BroadcastUser(userID, resp)`
- streamHub 随 AppContext 走（`GetSessionStreamHub` 惰性构建，`appcontext.go:770-777`），天然按账户分片

**漏掉这层**：B 登录后能实时看到 A 的流式输出。

### 3.6 前端

- 登录卡片加**用户名**（`components/AuthGate.tsx`）
- 当前账户存 localStorage，`appPath()` 注入 `?user=`
- 用户管理面板（仅 admin 可见）：列表 / 新建 / 改密 / 禁用 / 删除 —— 新组件，不侵入 15k 行的 `App.tsx`
- 「我的账户」改自己密码
- 退出登录（回到登录卡片，换账户）

## 4. 明确不做

- API 鉴权、Cookie、WS 鉴权（§1.1 决策的后果）
- 账户表跨节点同步：PC 与 WSL 各自独立，各登各的
- 细粒度权限（只读账户等）
- 不动 E2EE / relay

## 5. 分期（全部已完成）

| 期 | 内容 | 提交 |
|---|---|---|
| P1 | `users.json` + bcrypt + 登录带用户名 + 账户 CRUD API | `d05c97c`（含 `4031b8c` 修登录响应泄漏 password_hash） |
| P2a | 账户感知的 meta 根（`RootInfo.MetaRoot`） | `8b93a3a` |
| P2b | 每账户 config 目录 + `WorkspaceManager` + 请求路由 | `276c74f` |
| P3 | WS 连接归属 + 广播隔离 | 同 `276c74f`（每账户独立 StreamHub 天然分片）+ `4651fe5` 修前端漏带 `user=` |
| P4 | 用户管理界面 + 改自己密码 + 退出登录 | `fdf7fba` |
| ~~P5~~ | ~~存量迁移~~ | **不需要**：主账户沿用 `<cfg>/` 与原项目内 `.mindfs/`，零迁移 |

### 5.1 实施中发现的两个真问题（原方案没预见到）

1. **默认 meta 是「项目内」**：`meta_location` 缺省即 `project`，会话库/任务库/文件元数据都在 `<项目>/.mindfs/`。
   两个账户加同一项目会直接共用同一份会话库——比原方案 §2.1 预估的更严重。
   最终解法改成「主账户沿用旧路径 + 其余账户永远用账户私有 meta」，顺带消灭了 P5。
2. **WS 连接丢掉 `user=`**：前端同源判定用裸 `origin` 比较，而 `ws://host` ≠ `http://host`，
   导致本机 WS 被误判成跨节点、丢掉账户参数，**所有账户的实时流注册到主账户的 hub 上**。
   单测与读代码都没发现（漏的是"给不给 id"，而非"给了 id 隔不隔得开"），浏览器实测才抓到。
   修在 `4651fe5`，并加了 `web/tests/multi-account-partition.test.mjs` 守卫。

### 5.2 已知代价

- 每账户一套 agent 池与探针 → claude/codex 子进程数随账户数线性增长（靠空闲释放兜底）。
- 前端每次请求多一个 `user=` 参数；跨节点请求不带（由对方决定用它的主账户）。


## 6. 风险

| 风险 | 说明 |
|---|---|
| 构造函数改造漏改 | 漏一处即两账户共用状态。构造后断言目录前缀 + 双账户 E2E 覆盖 |
| `App.tsx` 15k 行 | 账户态挂 `services/session.ts`（已持有 rootId/nodeId）；管理面板独立组件 |
| agent 进程翻倍 | 每用户独立池，进程数随账户数线性增长 |
| 存量迁移不可逆 | 迁移前备份 `~/.config/mindfs/`，只做**复制**不删原文件 |
| **误以为这是隔离** | §1.1。README / UI 文案都不得暗示"账户隔离了数据" |
