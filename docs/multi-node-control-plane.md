# 控制面 / 数据面分离

> 2026-10-04。回答一个新 API 调用该打哪儿：**`controlPath` 还是 `appPath`/`appURL`？**

## 为什么有这份文档

mindfs 是同一个二进制在每台机器上完整跑一遍。这没问题 —— 前提是每台机器只承担**一类**职责。
今天两类职责混在一起，于是控制面状态在每台机器上各有一份，且**已经漂移**：

```
$ cat ~/.config/mindfs/nodes.json            # 本机
[ {id:"local", url:"https://home…"}, {id:"721372d9-…", name:"pc", url:"https://pc…"} ]

$ ssh wsl cat ~/.config/mindfs/nodes.json     # PC
[ {id:"local", url:"https://pc…"},   {id:"ab16422e-…", name:"home", url:"https://home…"} ]
```

两张表互相矛盾：同一台物理机器在两边拿到不同 node id。这是所有跨节点 bug 的根源 ——
`session_project_pins` 里同时躺着 `local::mindfs`（PC 写的）和 `721372d9-…::CMAI`（本机写的），
键空间已经碎裂。模板同样漂移：本机 3 个、PC 5 个（PC 独有论文评审/论文修改/润色+排版），
`stage_template.json` 只有 PC 有。

## 划分

| | 是什么 | 归属 |
|---|---|---|
| **控制面** | 只有一份真相的状态：账户表、偏好、提示词库、看板模板、节点表、WebPush 订阅、relay/e2ee 绑定、应用更新 | **只在主节点** |
| **数据面** | 天然按机器分的状态：项目列表、会话库、任务库、文件、git、agent 进程池、定时任务 | 每台机器各一份（本就如此） |

节点角色由 `nodeinfo.Role` 决定，启动参数 `-role worker` 或配置文件 `{"role":"worker"}`：

- `control`（**默认**）—— 提供 UI + 控制面 + 数据面。零值就是它，所以不配置等于改造前的行为。
- `worker` —— 只提供数据面。控制面端点与 `GET /` 一律 403，不注入静态目录（前端不提供）。

配置文件是 `<config-dir>/config.json`（Linux 下 `~/.config/mindfs/config.json`）。
**不传 `-config` 时读这个缺省路径**，所以写文件即可生效，不必改 systemd unit；
显式传 `-config` 指向的文件不存在会**报错退出**（那是操作失误），但缺省路径下文件不存在是常态，不报错。
命令行 flag 优先于文件（`-role worker` 覆盖文件里的值）。

> **改启动参数只能改 `cli/cmd`。** `make build` 装的是 `./cli/cmd`，而
> `server/cmd/mindfs-server` 只有 `make dev-backend` 引用 —— 后者不在发布二进制里，
> 在那儿加 flag 单元测试会绿、真机却毫无作用（role 写成这样踩过一次）。
> 另注 `cli/cmd` 属于根 module，import 不到 `server/internal/*`（Go internal 规则），
> 所以 `app.StartOptions.Role` 是 `string`，归一化在 `app.Start` 内部做。

## 铁律

**控制面请求必须用 `controlPath`（打页面服务器），数据面请求继续用 `appPath`/`appURL`（跟随选中节点）。**

```ts
// services/controlPlane.ts —— 不接受 nodeId
export function controlPath(path: string, params?: URLSearchParams): string
```

`controlPath` **故意不接受 `nodeId`**。控制面不属于任何节点；调用方如果想传 `nodeId`，
那说明它要的多半是数据面请求。

**判断方法**：「这个东西在另一台机器上有意义吗？」有 → 数据面。没有，只有一份该是权威的 → 控制面。

| 端点 | 归属 |
|---|---|
| `/api/users*`、`/api/auth/*`、`/api/preferences/*`、`/api/prompts*` | 控制面 |
| `/api/task-templates*`、`/api/task-stage-templates*` | 控制面 |
| `/api/web-push*`、`/api/nodes`、`/api/node-info` | 控制面 |
| `/api/relay*`、`/api/e2ee*`、`/api/token-station*`、`/api/app/update*` | 控制面 |
| `/api/dirs`、`/api/sessions*`、`/api/tasks*`、`/api/tree`、`/api/file`、`/api/git/*` | 数据面 |
| `/api/agents*`、`/api/agent-config/*`、`/api/agent-api-providers` | **数据面** |
| `/ws`、`/health` | 数据面 |

`agent-config` 归数据面是有意的：它改的是**本机 agent 运行时**配置，运行节点需要它。
`/health` 也在数据面 —— 它是公开端点且 worker 必须能报出自己的角色，前端靠它区分
「对面挂了」和「对面是运行节点，本来就不提供 UI」。

> `/api/node-info` 自己在控制面表里，所以 worker 上它是 403。**探测角色要打 `/health`，
> 不是 `/api/node-info`** —— 后者会让每个运行节点都被误判成「离线」。

## 单节点部署下改绑是零行为变化

`pageServerPath` 用 `deriveLocalNodeBase(window.location.origin)`，`appPath` 用 `getActiveNode()`；
单节点时两者必然同源。**只有多节点场景下才有差异 —— 而那正是要修的 bug。**

## 两个容易漏的连带后果

**1. 建任务必须内联流水。** 模板库只在主节点，但任务可以建在运行节点上，而后端 `CreateTask`
在 `stages` 为空时会回查**任务所在节点**的模板库，那里必然查不到 →
`task template not found`，整个建任务失败。所以前端 `createTask` 每次都要带上流水
（`app/appTask.ts` 的 `taskStagesForCreate`），并顺带快照模板名 —— 看板上的模板来源标签
在运行节点上才不会是空白。

注意 `applyStageOverride` 在**没有覆盖时返回 `undefined`**，而那是最常见的路径；
`taskStagesForCreate` 就是为了兜住这个分支，`applyStageOverride` 本身语义不动。

**2. 模板按项目过滤，不按节点路由。** 模板带 `TaskTemplate.RootID`：空 = 全局，
任何项目都能套用；非空 = 该项目专用。列表按 `?root=<id>` 返回「全局 + 本项目」的并集 ——
别的项目的专用模板不该出现在这个项目的下拉框里。

## 迁移

`scripts/migrate-control-plane-to-primary.sh`（默认预览，`--apply` 才写）把运行节点独有的
控制面数据按 id 并集并进主机，冲突本机胜。幂等、写前备份 `.bak-<ts>`、不删远端文件。

**WebPush 订阅不能迁移**：VAPID 私钥在各自机器上，订阅密文绑定该密钥。需要在主节点页面
重新点一次订阅。

## 上线顺序

1. 先合并全部代码（两端都还是 `control`，行为不变）。
2. 跑一次迁移脚本。
3. 给运行节点写 `{"role":"worker"}`，重启。
4. 验证：数据面照常、UI 仍从主节点进、直接访问运行节点得到 403 `node_is_worker`。

> **运行节点上不需要源码库。** 自 2026-10-06 起 WSL 端已无 git checkout，只接收
> `scripts/deploy-all.sh` 推过去的编译产物。`role` 来自运行节点自己的
> `~/.config/mindfs/config.json`，与源码无关 —— 改角色只需改那个文件再重启，
> 不必（也无法）先把代码同步过去。

## 待办：把账户表收回主节点（方案 B）

> 2026-10-08 定方向，**尚未实施**。

### 现状

worker 也加载一份完整的 `users.json`（含密码哈希、角色）。但 worker 上
`/api/auth/*`、`/api/users/*` 本来就是 403（见上方控制面端点表），所以那份文件在
worker 上只有 `id` / `username` / `primary_user_id` 三个字段有用 —— 纯粹为了
**数据分区**：把 `user=xiaokubao` 解析成账户 id，决定数据落在 `users/<id>/`
还是 `<config-dir>/`。

后果：

- 每台机器一份账户表 = 控制面漂移，正是本文档想消灭的那个问题。
- 新节点首次启动会自动建一个 `admin` 空壳账户，然后一直躺着。
- 陌生 `user=` 落到「空工作区」，读得到空列表，但**写**会撞上
  `registry path required` 这种内部错误（已临时改成用户能看懂的话，根因会随本方案一起消失）。

### 目标

worker **不要账户表**。数据分区需要的账户清单从数据本身推导：
`users/<id>/` 存在 = 这个账户在本机有数据；首次写入时按需建目录。

配套可以删掉：

- `app.emptyWorkspace` 那套「陌生账户给只读空工作区」的逻辑（改成按需建目录）；
- `registry.saveLocked` 里 `path == ""` 这条分支（不再有人走到）。

### 硬前提：账户 id 必须跨机器稳定

现在 id 是随机生成的（本机 `u_pc_admin`、fn `u_yQgvkYcI7Na1tzIR`）。
worker 没有账户表，就**无法把用户名解析成目录名** —— 这是本方案的硬前提，
不解决则方案不成立。两个办法，二选一：

1. **id 改由用户名派生**（如 `u_` + hash(username)）：跨机器天然稳定，
   前端带用户名即可，worker 直接算出目录名。代价：存量账户要迁移目录名。
2. **每个账户目录里放 `account.json` 记 username**：worker 扫
   `users/*/account.json` 建映射。不改 id 生成规则，但多一个文件。

### 实施顺序

1. 先做「账户 id 跨机器稳定」—— 独立改动，主节点行为不变。
2. worker 去掉 `users.json`，改从数据推导 + 按需建目录。
3. 删掉 `emptyWorkspace` 与相关分支。
4. 验证：新节点上第一次加项目能成功；主节点登录 / 账户管理不变。