# 离线优先的本地控制面（Edge Control Replica）设计

Spec 2026-11-21。本文回答「主节点不可达时，本机还能不能完整地用」并给出架构。
用户已就六个问题作出裁定（见 §2），本文把这些裁定落成可执行的设计；
尚未确认的口径集中在 §12，逐条给了推荐默认值。

## 0. 先澄清：什么是「控制角色」

MindFS 同一份二进制有两个角色（`server/internal/nodeinfo/role.go`）：

| 角色 | 提供 | 不提供 |
|---|---|---|
| `control`（缺省） | 页面 + 控制面 + 数据面 | — |
| `worker` | 只有数据面 | 控制面端点一律 403，`GET /` 403，不装配静态目录 |

- **控制面**：账号、偏好、提示词、置顶、节点表、工作流库、WebPush 订阅、应用更新。
  今天只在主节点有一份真相，见 `docs/multi-node-control-plane.md`。
- **数据面**：项目（registry）、会话、任务、文件、git、agent 池、定时任务。
  每台机器自有，从不跨机复制。

所以「hasee 当控制端」= hasee 跑 `role=control`，自己发页面、自己跑数据面。
难点不在角色开关（这个已经支持），而在控制面：页面一旦由 hasee 发出，
`controlPath` 就固定打 hasee（`web/src/services/net/controlPlane.ts`），
而账号/工作流这些状态的真相在 home。用户要的六条，本质是
**给控制面加一份本机副本，并定义副本与主节点之间怎么合并**。

## 1. 现状证据

只列对方案有约束力的事实，全部来自代码与文档。

| 事实 | 位置 | 对方案的影响 |
|---|---|---|
| 前端不打进二进制，按 `<exeDir>/web/dist` → `<exeDir>/web` → `<exeDir>/../share/mindfs/web` 找目录，必须含 `index.html` + `favicon.svg`；`MINDFS_STATIC_DIR` 可覆盖 | `server/app/server_helpers.go` | 本机发页面是既有能力；「打包初始版本」只需把 `web/` 装到位 |
| 只有非 worker 装配静态目录，worker 的 `GET /` 返回 403 | `server/app/server.go`、`server/internal/api/http_frontend.go` | hasee 必须 `role=control` |
| 控制面前缀：`/api/auth`、`/api/users`、`/api/preferences`、`/api/pins`、`/api/prompts`、`/api/workflows`、`/api/web-push`、`/api/nodes`、`/api/node-info`、`/api/token-station`、`/api/app/update` | `server/internal/nodeinfo/role.go` | 转发/副本的域就是这张表，不需要另立清单 |
| `controlPath` 不接受 `nodeId`，永远打页面服务器 | `web/src/services/net/controlPlane.ts` | 本机副本必须由页面服务器自己提供，前端不改调用形状 |
| 账号存 `<configDir>/users.json`（0600，bcrypt，无明文），主账户 `PrimaryUserID`，其余账户数据在 `baseDir/<userID>` | `server/internal/auth/auth.go`、`server/app/workspace_build.go` | 离线要能登录 ⇒ 必须复制 password hash；账户 id 跨机必须一致 |
| 偏好 `preferences.json`、节点表 `nodes.json`、WebPush 订阅、工作流库都是**进程级共享一份**，不按账户分 | `server/app/server.go:201-218` | 这三个域全局同步，不按账户拆 |
| 置顶 `session_pins.json`、提示词 `prompts.json` 按账户各一份 | `server/app/workspace_build.go`、`server/internal/api/usecase/prompts.go` | 这两个域按账户目录逐个同步 |
| 工作流库是单个 `workflows.json`（temp + rename 原子写），草稿按 revision CAS，发布拒绝 stale | `server/internal/kanban/workflow_store.go` | 版本是追加型、草稿是 CAS 型，冲突语义要分开 |
| 节点表在客户端**只在内存**，服务端才是真相；节点 id 由前端 `genId()` 随机生成 | `web/src/services/net/nodeRegistry.ts` | 两台 control 各自生成 id 必然漂移；同步键必须换成 URL，不能是 id |
| 服务端已记录 `request_id → session key` 24 小时去重，但只在内存 | `server/internal/api/ws_broadcast.go` | 控制面不能复用它做幂等，需要自己的持久 change id |
| 服务端有持久 outbox（`continue_requests` / `node_runs` / `outbox_items`），`unknown` 永不自动重放 | `server/internal/kanban/service_outbox.go` | 可复用的语义范本：不确定就不动，交给人 |
| Service Worker 只缓存壳与静态资源；`/api/`、`/ws`、`/health` 显式绕过；导航 network-first，失败回落 precache 的 `index.html` | `web/vite.config.ts` | 离线能打开旧壳；API 不会假装成功 |
| Windows 安装器与发布 zip 已经带 `web/`；`deploy-pcwin.sh` 只换 exe | `scripts/install.ps1`、`scripts/build-all.sh`、`scripts/deploy-pcwin.sh` | 「初始版本」不用新建打包链，只需把镜像更新接上 |
| 没有任何控制面复制、leader election、分布式锁、版本向量或冲突解决 | 全仓 | 这套机制要新建，不能指望现有 CAS 跨机生效 |

现有「多控制端」的真实状态：**可以同时起两台 control，但它们各自持有互不相干的控制面**。
浏览器在哪台上，看到的就是哪台的账号/工作流；改一台不会到另一台。
这正是 `docs/multi-node-control-plane.md` 记录的漂移问题（`nodes.json`、`task_template.json` 都出过）。

## 2. 用户裁定（规划输入，不再重问）

| # | 裁定 | 落地口径 |
|---|---|---|
| 1 | 离线时能打开 UI、看本机数据、操作本机所有 agent、正常运转 | 页面与数据面完全本地；控制面读本机副本 |
| 2 | 账号、工作流等控制面在本机有备份；主节点不可达时**明确切换**到本地模式并检测恢复；恢复后回到在线模式并**受控同步所有内容**，有冲突则提示 | 显式模式状态机 + 增量同步 + 人工裁决冲突 |
| 3 | 离线时本机节点全部正常，其他节点不可访问 | 不复制数据面；远端节点标记不可达，禁止把失败当空数据 |
| 4 | 两侧都改了 → 明确冲突，用户决策 | 记录级冲突，绝不静默 last-write-wins |
| 5 | 模型端点不担心，已有独立配置切换 | 同步范围**不含** agent 凭据、模型端点、`channels.*` |
| 6 | 打包一份初始前端；主节点在线实时无感更新，离线用最新保留版本 | 安装器带初始 `web/`；边缘镜像主节点的构建产物并按版本原子切换 |

## 3. 架构选择

三个候选：

| 方案 | 一致性 | 代价 | 结论 |
|---|---|---|---|
| A. 两台 control 同时权威、持续双向同步 | 冲突常态化，两个浏览器可能看到不同世界 | 需要完整 CRDT/OT 级别的合并 | 否 |
| B. 纯离线客户端 + 手写上行队列 | 控制面离线不可用，账号/工作流还是读不到 | 要重做一套客户端状态 | 否 |
| **C. 在线单写者 + 边缘副本 + 离线接管 + 恢复合并** | 在线永远只有一个写者；冲突只在「离线期间两边都改」时出现 | 一个转发层 + 一个同步引擎 | **是** |

### C 的三条路径

**在线（`online`）**

- 页面的控制面请求原样转发给主节点（`/api/auth`、`/api/preferences`、`/api/nodes`、
  `/api/workflows`……），响应原样回传；本地副本只读，由变更流追平。
- 这样在线时**没有第二个写者**，不存在「两台 control 同时改」的常态冲突。
- 例外（不转发，留在本机）：
  - `/api/web-push`：订阅是浏览器 endpoint + VAPID 私钥，天然设备本地；
  - `/api/app/update`：回答的是本机二进制该不该更新；
  - `/api/node-info`：回答本机角色/版本。
  这三条在 §5 标为「本地域」。

**离线（`offline-local`）**

- 页面、控制面、数据面全部由本机提供；控制面写进入本地副本 + 待推送日志。
- 本机数据面（项目/会话/任务/agent）照常；远端节点请求失败要在 UI 显示「不可达」，
  不能回落成空列表（沿用 `[[multi-node-state-merge-rule]]` 的教训）。

**恢复（`reconciling` → `conflict-review` → `online`）**

- 主节点重新可达后先冻结控制面写入（读仍可用、数据面不受影响），
  推送本地待推送变更、拉取主节点增量。
- 逐记录比对：主节点记录 hash 仍等于本地记录的 `base_hash` → 直接应用；
  已被主节点改过且内容不同 → 记为冲突，两边都不覆盖。
- 有冲突 → 进入 `conflict-review`，前端逐条展示「本地 / 主节点」，用户选择后才写。
- 全部解决 → 回到 `online`。

## 4. 数据模型

### 4.1 Record

同步的最小单位是「一条记录」：

```json
{
  "domain": "preferences",
  "key": "agents",
  "hash": "sha256:…",
  "rev": 1042,
  "updated_at": "2026-11-21T09:30:00Z",
  "origin": "home",
  "deleted": false,
  "payload": { }
}
```

- `key` 在域内唯一且**稳定**：不用随机 id（节点用 URL 归一化键，工作流用 workflow id，
  提示词用文本 hash，偏好用顶层字段名）。
- `hash` = 对规范化 JSON（键排序、无空白）取 sha256。只比 hash，不比时间戳。
- `deleted=true` 是 tombstone，保留 90 天；不能只靠「不在列表里」表达删除。
- `origin` 只用于诊断与冲突展示，不参与裁决。

### 4.2 主节点

`<configDir>/control_sync.db`（SQLite）：

```sql
records(domain, key, rev, hash, payload, deleted, updated_at, origin, PRIMARY KEY(domain,key));
changes(rev INTEGER PRIMARY KEY, domain, key, hash, deleted, origin, created_at);
applied_changes(change_id TEXT PRIMARY KEY, rev, applied_at);
conflicts(id TEXT PRIMARY KEY, domain, key, local_payload, remote_payload,
          local_hash, remote_hash, created_at, resolved_at, resolution);
```

`rev` 是主节点单调递增整数，每次成功写入控制面后分配。
`changes` 是变更日志，供边缘按 `since=rev` 增量拉取；按 90 天或 50 万行清理。

清理（`PruneChanges`，主节点启动一次 + 每小时一次）只删最旧的一批，单次上限
5000 行 —— 一次删几十万行会把写锁攥住，期间采集与边缘拉取都停摆。删空的 rev 不会
被复用：`state.pruned_through_rev` 同时充当 rev 分配下限与 `HeadRev` 下限，否则水位
停在旧值的边缘会把新变更当成「已应用过」全部跳过。拉取方要的 `since` 落在已清理
区间（`since <= pruned_through_rev`）时返回 `ErrRetentionGap`（HTTP 409），
边缘据此锁进 `offline-local`：重试不会让删掉的历史回来，只能由人决定是否
`adopt_primary` 重新采纳主节点。

### 4.3 边缘

`<configDir>/control_replica.db`：

```sql
replica(domain, key, rev, hash, payload, deleted, updated_at, origin, PRIMARY KEY(domain,key));
pending(change_id, domain, key, base_hash, payload, deleted, created_at, PRIMARY KEY(domain,key));
state(id INTEGER PRIMARY KEY CHECK(id=1), instance_id, mode, base_rev, last_primary_at, last_error);
conflicts(...);  -- 与主节点同构，便于 UI 只写一套读取逻辑
```

- `pending` 以 `(domain,key)` 为主键：同一记录离线期间改多次只保留最后一次，
  `base_hash` 固定为**第一次离线写之前**的值 —— 这正是三方合并的「共同祖先」。
- 应用远端记录时走域适配器的 `Apply`，并**抑制变更记录**，否则会自激成回环。

## 5. 域清单

| 域 | 后端存储 | key | 合并单位 | 备注 |
|---|---|---|---|---|
| `users` | `<configDir>/users.json` | `username`（小写归一） | 单账号整条 | 含 bcrypt hash；主账户标记单独一条 |
| `preferences` | `preferences.json` | 顶层字段名（`agents` / `session_naming` / `cors` / …） | 单字段 | 字段级合并，避免整文件互相覆盖 |
| `nodes` | `nodes.json` | `nodeBaseKey(url)`（协议+主机+端口+部署前缀） | 单节点 | **不按 id 合并**，id 只是载荷 |
| `prompts` | `<账户目录>/prompts.json` | `sha256(text)` | 单条提示词 | 数组语义转成记录语义，删除用 tombstone |
| `pins` | `<账户目录>/session_pins.json` | `project:<id>` / `session:<key>` | 单条 | 置顶是布尔存在性，删除即 tombstone |
| `workflows` | `<configDir>/workflows.json` | `wf:<id>:meta` / `wf:<id>:draft` / `wf:<id>:version:<n>` | 记录 | 版本不可变、只追加；发布提案不同步 |
| 本地域（不同步） | `session_pins` 的 VAPID/订阅、`app/update`、`node-info`、`registry.json`、`agents.json`、`channels.*`、任务库 | — | — | 凭据与设备状态不外传 |

`users` 的密码 hash 属于敏感载荷：只走已认证的同步通道，落盘 0600，
不进日志、不进冲突 UI 的明文展示（UI 只显示「账号已修改」与用户名）。

## 6. 同步协议

全部挂在 `/api/control-sync/*`，只允许在 `role=control` 且 `control_sync.enabled=true` 时注册。

| 方法 | 路径 | 作用 |
|---|---|---|
| `GET` | `/api/control-sync/head` | `{head_rev, instance_id, build_version}`，用于探活 |
| `GET` | `/api/control-sync/changes?since=&limit=` | 增量拉取，`limit` 默认 500、上限 2000 |
| `POST` | `/api/control-sync/push` | 推送待同步变更，返回 `applied` / `conflicts` / `rejected` |
| `GET` | `/api/control-sync/conflicts` | 冲突列表 |
| `POST` | `/api/control-sync/conflicts/{id}/resolve` | `{choice: "local"\|"primary"\|"payload", payload?}` |
| `GET` | `/api/control-sync/status` | 模式、主节点可达性、`base_rev`、待推送数、冲突数（**两种模式都可用**） |
| `POST` | `/api/control-sync/mode` | 显式切换 / 立即重试连接 |
| `GET` | `/api/control-sync/ui/manifest` | 前端构建清单（文件名、大小、sha256、`min_server_build`） |
| `GET` | `/api/control-sync/ui/bundle` | 前端构建 tar.gz |

### 6.1 推送语义

请求：

```json
{
  "instance_id": "hasee-…",
  "base_rev": 1031,
  "changes": [
    {"change_id":"…","domain":"preferences","key":"agents",
     "base_hash":"sha256:…","payload":{…},"deleted":false}
  ]
}
```

主节点逐条处理（单事务）：

1. `change_id` 已存在于 `applied_changes` → 记为 `applied`，不重复写（幂等）。
2. 当前记录 hash == `base_hash` → 应用，分配新 `rev`，写 `changes`。
3. 当前记录 hash == 载荷 hash → 已收敛，记为 `applied`。
4. 其它 → `conflict`，写 `conflicts`，**不改记录**。
5. `deleted=true` 且记录本就不存在 → `applied`（幂等删除）。

响应里每条都要有结论；没拿到结论的变更，边缘保留在 `pending` 等下一轮，
不推断成功。整批 push 不要求原子（记录相互独立），但**单条记录的写入必须原子**。

### 6.2 拉取语义

边缘从 `base_rev+1` 拉到 `head_rev`，逐条应用；应用前先看本地 `pending`：

- 本地没有 pending → 直接应用，`base_rev` 前进。
- 本地有 pending 且远端 hash == `pending.base_hash` → 远端是旧值，保留 pending。
- 本地有 pending 且远端 hash == pending 载荷 hash → 已收敛，清 pending。
- 否则 → 冲突，保留两边，等用户裁决。

拉取按页推进，每页提交后再取下一页；进程中途退出可从 `base_rev` 续传。

### 6.3 认证

`control_sync` 配置（`config.json`，缺省关闭）：

```json
{
  "control_sync": {
    "enabled": true,
    "primary_url": "https://home.xiaokubao.space/mindfs",
    "instance_id": "hasee",
    "timeout": "10s",
    "ca_file": ""
  }
}
```

- 共享密钥**只**从 `<configDir>/control-sync.token`（0600）读，配置里只有
  `token_file` 指路，**没有内联字段、没有环境变量、没有 flag** —— 这条通道搬的是
  含口令 hash 的整份控制面，任何「顺手写在 config.json 里」的入口都是泄露面。
  token 少于 16 字节或含空白一律拒绝启动（`controlsync.ValidateSyncToken`）。
  **不写进仓库、不写进 config.json**。
- 每个请求带 `X-MindFS-Instance`、`X-MindFS-Timestamp`（Unix 秒）、`X-MindFS-Nonce`、
  `X-MindFS-Signature` = HMAC-SHA256(token, timestamp + "\n" + nonce + "\n" + body)。
- 时间窗 ±300 秒；nonce 在服务端缓存 10 分钟拒绝重放。
- 未配置 token → 这些端点不注册（404），不是「裸奔但没人知道」。
- **`primary_url` 必须是 `https://`**（用户裁定，2026-11-21）；`http://` 一律视为配置错误、
  拒绝启动同步。自签或内网 CA 通过 `ca_file` 指定信任根；不提供跳过校验的开关。
  隔离验收实例同样走 TLS（自签证书 + `ca_file`），不为测试留明文后门。

## 7. 模式状态机

```
                 ┌──────────── 主节点连续 2 次可达 ────────────┐
                 ▼                                            │
   online ──(连续 3 次探测失败 / 用户点「切换本地模式」)──► offline-local
     ▲                                                        │
     │                                                        │
     └──── reconciling ◄──(主节点可达)─────────────────────────┘
              │
              ├── 无冲突 → online
              └── 有冲突 → conflict-review ──(全部裁决)──► reconciling → online
```

- 探测：每 10 秒 `GET <primary>/api/control-sync/head`，超时 3 秒；
  连续 3 次失败自动降级，连续 2 次成功才升级。降级/升级都写一条可见系统事件。
- 模式由**服务端**持有并持久化（`state` 表），多浏览器看到同一个结论；
  前端只展示与请求切换，不自行判定。
- `conflict-review` 期间：控制面写请求返回 `409 sync_conflict_pending`，
  读与数据面照常；UI 顶部常驻横幅 + 一次性通知。
- 在线转发遇到主节点刚好断掉：返回 `503 primary_unreachable`，**不自动在本地执行**，
  避免「转发失败又本地写一遍」的双写。前端提示「已切到本地模式，请重试」。
- 恢复前先做一次「只读对账」：比较 `instance_id`、`head_rev`、schema 版本；
  主节点被重建/换库时拒绝合并（`409 replica_origin_mismatch`），交给人处理。

## 8. 前端

1. **模式指示**：顶部全局横幅，四态 — 在线（无横幅）、本地模式（含「最近同步 HH:MM」）、
   同步中、冲突待处理。点击展开「立即重试 / 切回在线 / 查看冲突」。
2. **冲突面板**：按域分组，每条显示 key、两边摘要（账号只显示用户名）、
   选择「保留本地 / 采用主节点 / 手动合并」。批量动作必须二次确认，写明条数。
3. **节点可达性**：新增轻量探测（`/health`，15 秒一次），维护
   `online/offline/unknown`；所有跨节点 fan-out（`useManagedRoots`、会话列表、
   文件树）跳过不可达节点并**显式标注**，绝不把失败当空列表。
   不可达节点上的操作按钮禁用并给出原因。
4. **资源更新**：页面导航继续 network-first；新构建只在下次导航/刷新生效，
   不打断当前会话（现有 Service Worker 语义已经如此）。
5. 登录：离线也能用复制过来的账号登录；登录态仍存 localStorage（现状），
   不新增服务端会话。

## 9. 前端资源镜像与更新

**主节点**：发布构建时生成 `ui-manifest.json`（`make build-web` 顺带产出），
记录 `build_version`、`min_server_build`、每个文件的 `sha256` 与 `size`。

**边缘**：

1. 启动时与每 5 分钟比较 `head.build_version` 与本地指针。
2. 不同则下载 tar.gz 到 `<configDir>/ui/<version>.tmp`，逐文件校验 sha256；
   任一失败 → 丢弃，保留旧版本。
3. 校验通过后整目录 rename 为 `<configDir>/ui/<version>`，写 `.complete`，
   最后原子替换指针文件 `current`（内容为版本号；不用符号链接，Windows 无权限时会失败）。
4. 静态目录解析顺序改为：`MINDFS_STATIC_DIR` → **`<configDir>/ui/<current>`（校验完整）**
   → 安装目录 `web/dist` → `web` → `../share/mindfs/web`。
5. 只保留最近 2 个完整版本；`current` 指向的永不删。
6. `min_server_build` 大于本机二进制版本时**不激活**，横幅提示「前端有新版本，
   需先更新后端」，继续用本机旧构建 —— 避免新前端调用旧后端不存在的接口。
7. 离线时 `current` 不变，页面照常从本机磁盘加载。

Windows 安装器已带初始 `web/`（`scripts/install.ps1`），
首次安装即可离线打开；镜像只在主节点可达时更新。

## 10. 安全与隐私

- 同步通道只走 token + HMAC，且**一律 https**（用户裁定）；未配置 token 即关闭，
  `http://` 的 `primary_url` 直接拒绝启动。
- `users.json` 复制的是 bcrypt hash，不是明文；文件 0600；日志与错误信息不含 hash。
- 冲突 UI 对 `users` 域只显示用户名与「密码已变更」，不显示 hash。
- `ui/bundle` 解包防护：拒绝绝对路径、`..`、符号链接、设备文件；
  单包 ≤200 MB、≤5000 个文件；解包后逐文件校验。
- `/api/control-sync/*` 在生产默认不注册；即使开启也要求 token，
  不做「同源即信任」。
- 本地模式不是权限提升：API 仍然匿名（`docs/multi-user-prd.md` 的既有决策），
  模式切换不改变鉴权模型。

## 11. 边界（明确不做）

- **不复制数据面**：项目、会话、任务、文件、git、agent 运行态只在本机。
  离线时其他节点的数据不可访问，这是用户裁定 3 的明确含义。
- **不做多主同时写**：在线永远转发到主节点；只有离线态本机才权威。
- **不做自动冲突合并**：没有 last-write-wins、没有字段猜测，只提供人工裁决。
- **不复制设备凭据**：agent 登录态、`agents.json` env、`channels.feishu`、
  VAPID 私钥、WebPush 订阅都不出本机。
- **不做客户端离线写队列**（浏览器侧）：控制面离线写发生在**本机服务端**，
  前端仍是普通请求，不需要 IndexedDB 队列。
- **不做后台静默切模式**：自动降级只发生在「主节点不可达」这种单边事实下；
  恢复合并必须经过可见的 `reconciling` 与冲突面板。

## 12. 口径与结论

| # | 问题 | 结论 | 状态 |
|---|---|---|---|
| D1 | 「同步所有内容」是否包含任务/会话/文件 | **不包含**，只同步控制面；数据面各机自有 | 默认（改则重开设计评审） |
| D2 | 是否复制账号 password hash | **复制**（否则离线无法登录）；同步通道强制 https | **已确认（Q2，2026-11-21）** |
| D3 | 冲突粒度 | **记录级**；裁决前**按域冻结**，其他域照常 | **已确认（Q4，2026-11-21）** |
| D4 | 同步信任模型 | **一律 https** + 共享 token + HMAC（时间戳 + nonce）；禁止跳过校验 | **已确认（Q3，2026-11-21）** |
| D5 | 在线时是否允许本机直接写控制面 | **禁止**，一律转发（单写者） | 默认（改则重开设计评审） |
| D6 | 工作流版本冲突 | 版本不可变、只追加；同版本号不同 hash 一律冲突；草稿按记录合并 | 默认 |
| D7 | 前端新构建何时生效 | **下次导航/刷新**，不打断当前会话 | 默认 |
| D8 | 首次加入时账户 id 不同 | **作废**：id = `u_` + sha256(用户名)[:16]，跨机确定性一致，无需改名 | **已作废（证据：`auth/store_query.go:89`）** |
| D9 | 主节点在线时边缘页面形态 | **边缘出页面并转发**控制面请求；转发失败 503，不写本地 | **已确认（Q1，2026-11-21）** |

这些默认值按「安全优先、可解释、最小惊讶」选取；其中 D1、D5 是架构分水岭，
如果用户后续要改，需要重开设计评审而不是调参数。

## 13. 验收标准

| # | 场景 | 通过判据 |
|---|---|---|
| A1 | 在线转发 | 在 hasee 页面改偏好/工作流/节点，home 的对应文件立即变化；hasee 副本在 5 秒内追平；hasee 本地文件不出现第二份真相 |
| A2 | 离线启动 | 断开 home 后 ≤30 秒进入 `offline-local`，横幅可见；用复制来的账号能登录；本机项目/会话/agent 正常；远端节点显示「不可达」且不出空列表 |
| A3 | 离线写 | 离线改偏好、置顶、提示词、节点、工作流草稿；重启本机后仍在；`pending` 计数正确 |
| A4 | 恢复无冲突 | home 恢复后自动 `reconciling → online`；两端记录逐条一致（hash 相等）；无重复写入（`applied_changes` 去重生效） |
| A5 | 恢复有冲突 | 两侧改同一记录 → `conflict-review`；两边内容都不丢；选择后另一端追平；未裁决时控制面写被 409 拒绝、数据面不受影响 |
| A6 | 前端镜像 | home 发布新构建 → hasee 下载校验后切换；损坏 hash 时保留旧版本；`min_server_build` 不满足时不激活 |
| A7 | 安全 | 无 token 时端点 404；错误签名 401；重放被拒；`ui/bundle` 路径穿越被拒 |
| A8 | 断点续传 | 同步中途 kill -9，重启后从 `base_rev` 续传，无重复、无丢失 |

每条都要有对应自动化用例；关键守卫（冲突不覆盖、pending 不自激、tombstone 生效、
模式不双写、镜像校验）必须做变异验证 —— 把实现改坏，看用例是否红。
