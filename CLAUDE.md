# MindFS — AI Agent Remote Access Gateway · Result Visualization

Go 后端 + React/TS 前端，Web 静态资源由后端 HTTP 服务。
两端都跑在 systemd 下，二进制 `~/.local/bin/mindfs`：本机（`ubuntu`）是 **system** 单元、只听 `127.0.0.1:7331`；
WSL（`PC-HOME`）是 **user** 单元、听 `0.0.0.0:7331`。详见下方「两台机器」。

## 两台机器（别叫错）

| 代号 | 实际是 | 主机名 / 用户 | 节点显示名 | mindfs 单元 | 监听 | 重启 |
|---|---|---|---|---|---|---|
| **本机** | Proxmox VM，Ubuntu 26.04，`home.xiaokubao.space`(192.168.1.6) | `ubuntu` / `xiaokubao` | `home` | **system**：`/etc/systemd/system/mindfs.service` | `127.0.0.1:7331` | `sudo systemctl restart mindfs`，**只由用户执行** |
| **wsl** | 用户 PC 上的 WSL，Ubuntu 24.04，`pc.xiaokubao.space` | `PC-HOME` / `nnb` | `pc` | **user**：`~/.config/systemd/user/mindfs.service` | `0.0.0.0:7331` | `ssh wsl 'systemctl --user restart mindfs'`，**Agent 可直接执行** |

- **本机不叫 PC**。说「PC」时指的是 WSL 所在的那台 Windows 物理机（`ssh pc` → Windows 宿主；`ssh wsl` → 里面的 WSL）。本机一律叫「本机」或 `ubuntu`，WSL 一律叫 `wsl` 或 `pc`。
- 判据（2026-09-21 复核）：本机 `curl 127.0.0.1:7331/mindfs/api/nodes` 与 `https://home.xiaokubao.space/mindfs/api/nodes` 返回完全相同（自己 `local`→home，对端 `pc`）；`https://pc.xiaokubao.space/...` 自己 `local`→pc，对端 `home`。
- 本机没有 user bus：`systemctl --user ...` 直接报 `Failed to connect to user scope bus`，别据此误判服务不存在——查归属看 `/proc/$(pgrep -x mindfs)/cgroup`（`/system.slice/` vs `/user@...`）。
- `/home/nnb/...` **不是**旧路径，是 WSL 端的家目录；本机是 `/home/xiaokubao`。
- 两台的账户表、节点 UUID 各自独立（`home.xiaokubao.space` → 本机，`pc.xiaokubao.space` → WSL）；判「本地节点」只认各自 `/api/nodes` 里报 `local` 的那个。

## 模块地图

```
server/cmd/mindfs-server/     后端入口
server/internal/api/http.go   HTTP 路由（REST）
server/internal/api/ws.go     WebSocket 连接管理 + 全部 WS 消息分发（单读 goroutine）
server/internal/api/usecase/  业务逻辑层（session CRUD、task、scheduled）
server/internal/api/workspace.go  按 user= 派发到各账户 handler（ScopedRouter）
server/app/workspace.go           每账户 AppContext 的惰性构建与缓存
server/internal/session/      Session 生命周期管理（manager.go，核心热路径）
server/internal/agent/        Agent 运行时抽象
  ├─ types/types.go           Session 接口 + Event/ToolCall/ContextWindow 等类型
  ├─ pool.go                  会话池（创建/复用/探测）
  ├─ protocol.go              协议发现
  ├─ claude/session.go        Claude Code 后端（claude-sdk CLI）
  └─ codex/session.go         Codex 后端
server/internal/relay/        远程隧道（a9gent.com）
server/internal/kanban/       看板任务（任务自带流水快照 + 预设解耦，无并发调度）
server/internal/e2ee/         端到端加密
server/internal/config/       配置管理
server/internal/fs/           文件浏览/读写
server/internal/gitview/      Git 视图
server/internal/notify/       通知（Web Push）
server/internal/scheduled/    定时任务
server/internal/preferences/  用户偏好
server/internal/auth/         账户表（users.json + bcrypt + 主账户）
server/internal/update/       自动更新
web/src/                      React/TS 前端（Vite 构建，dist/ 由后端静态服务）
  ├─ App.tsx                  核心状态 + 会话路由（最大文件，~15k 行）
  ├─ components/              UI 组件（ActionBar, SessionViewer, stream/ 等）
  ├─ hooks/                   React hooks（useSessionStream 等）
  ├─ services/session.ts      WS 通信层（前端 ↔ 后端）
  └─ i18n/                    国际化
```

## 构建与部署

```bash
# 编译（web dist + go binary）
make build

# 仅编译后端（含 web dist）
make install          # 安装到 ~/.local/bin/mindfs + ~/.local/share/mindfs/web/

# 仅编译前端
make build-web

# Go 测试
make test

# 本地开发
make dev              # go run 后端 + Vite 前端
make dev-backend      # 仅后端
make dev-web          # 仅 Vite
```

**Go 直接可用，不要绕**：工具链统一在 `$HOME/.local/share/go`（1.26.5），系统级 `/usr/local/go`
已归档为 `/usr/local/go.1.25.0.bak`（**不要**再往 PATH 里加它——两个版本共存时谁先命中取决于顺序，构建不可复现）。
`~/.zshenv` 第 3 段会自动建 `~/.local/bin/{go,gofmt}` 符号链接（幂等，丢了会自愈）。
所以 `go` / `make test` **裸跑即可，永久不要**再往命令前加 `export PATH=...`（无效，见下）。
报 `go: No such file or directory` 时跑一次 `zsh -c 'command -v go'` 即可触发自愈。

工具目录的**唯一上游**是 dotfiles 仓库的 `zsh/user-path.sh`（`~/.local/bin`、`~/.cargo/bin`），
由 `~/.zshenv` 第 2 段注入。cron / ssh / systemd `--user` 三个不读 rc 的场景，
由 dotfiles 的 `scripts/install-path-env.sh`（`~/.dotfiles/scripts/`，**不在本仓库**）
从同一上游展开写进 `/etc/environment` + crontab + `~/.config/environment.d/`。
改工具目录只改上游一处，然后跑一次那个脚本；它幂等，8 台机器都验过。

> 曾有个错误结论「Bash 工具不加载 .zshrc 所以 Go 不在 PATH」：Bash 工具其实就用 zsh、`.zshenv` 也加载了。
> 真正原因是它先 source 的 `~/.claude/shell-snapshots/snapshot-zsh-*.sh` 末尾有一行硬编码
> `export PATH=` 覆盖掉了。**该快照是会话级缓存，会话启动时生成一次就不再更新**——
> 所以刚改完 PATH 配置时 Bash 工具可能仍是旧值，重开会话即可恢复，配置侧无需改动。
> 只有 Claude Code 的 Bash 工具受此影响；ssh / cron / systemd / 交互与非交互 zsh 都正常
> （`zsh -c 'command -v cargo'` 可直接验证）。

**构建/重启分工（必守）**：`make build-web / make build / make install / make test` 等编译与安装验证由 Agent 自行执行并验证通过；本机 `sudo systemctl restart mindfs` 仅由用户显式执行（会杀掉所有托管的 claude 子进程），Agent 不得代为重启。`plan-only` / `只出方案` 时必须先落盘方案并经用户显式批准后才动手，禁止未审先做。

**代码变更收尾（必守）**：走 `ship` skill —— `bash scripts/deploy-all.sh` 一条命令完成门禁测试 → 提交推送 → 两侧编译安装 → WSL 拉取重建重启 → 两端对账。细节见 `.claude/skills/ship/SKILL.md`，不要手搓 make/ssh 序列。

## 关键架构事实（易踩坑）

1. **单读 goroutine 架构**：`ws.go` 中每个 WebSocket 连接只有一个读 goroutine，串行调用所有 handler。**所有 handler 必须异步派发**（`go h.handleXxx(...)`），否则一个阻塞的 handler 会冻结整个连接。`session.cancel` 曾因同步派发导致"停止按钮失效"。
2. **agent session 的 `Interrupt` 可能永久阻塞**：SDK 内部用 `context.Background()`，CLI 迟迟不 ack 时不返回。调用方必须加超时包装。
3. **前端 `App.tsx` 是巨文件**（~15k 行）：所有状态逻辑集中在此，修改前务必定位准确行号。
4. **静态 web 资源不走 go:embed**：由 `make install` 拷贝到 `~/.local/share/mindfs/web/`，修改前端后需重新 `make build-web && make install`。
5. **systemd 服务**：`~/.config/systemd/user/mindfs.service`，重启会杀掉所有托管的 claude 子进程。
6. **BottomSheet 拖拽**：默认 50% 高度，支持任意高度悬停，上沿 10% 吸顶；`pointercancel` 回弹 50%（避免异常悬停），触摸拖拽需防手势抢占。
7. **SessionList 展示规则**：仅 `fork` 子会话扁平为顶级，其它子会话保持折叠；已移除计数徽标，统一为普通会话样式。
8. **会话别名持久化**：存于 `server/internal/session/manager.go`（SQLite WAL），非前端 localStorage，保证跨端一致；浏览内容时会话锁由 `manager.go` 保持。
9. **项目显示别名持久化**：`server/internal/fs/registry.go` 维护 `display_name/display_name_raw + EffectiveName`，`ID (=Name=basename)` 与 `RootPath` 不变；`POST /api/dirs/{id}/display-name` 仅改显示名，落盘到 `registry.json` 的 `dirs[].display_name`，经 `managedDirResponse` 与 WS `display_name_changed` 广播；前端以 `App.getRootDisplayName/currentRootDisplayName` 贯通面包屑与会话分组，同 id 合并原地 `merged` 不走 ID 漂移；`Breadcrumbs` 编辑态 `✓/×`。
10. **主题色透传链路**：`web/src/services/nodeRegistry.ts` 的 `PALETTE` 单源 → 后端 `managedRootById._nodeColor` → 前端 `rootColor/accentHex/accentColorRaw` 贯通 `ModeIcon/SessionList 回复点/FileTree 尾点/SessionViewer 等待点/ActionBar 边框·阴影·发送按钮·Plan/附件胶囊·队列·小圆环·ModeSelector 气泡(仅 chat)·FileTree 顶栏 tab·看板 tab`；`FileTree` 以 `rootColor` prop 响应式覆盖 `getNodes()` 回退，避免切换节点不跟色。
11. **橡木徽标零跳变**：`FileViewer/GitDiffViewer/DefaultListView` 面包屑徽标统一为 `button + rootBadgeButtonStyle + data-onboarding="project-home"`（`var(--node-badge-bg) + 13px/1.2/600/radius6/padding1px4px/border:none`），消除 `span vs button` 盒模型差异；颜色由 `rootColor` 驱动（`var(--node-badge-bg)` 底 + `rootColor` 字）。
12. **多账户是「多配置档」，不是安全边界**（方案：`docs/multi-user-prd.md`）：
    - **API 层不做鉴权**（用户明确决策）。服务端按客户端声明的 `user=` 分区存储，**不校验**；
      URI 里换个 `user=` 就能读走别人的项目与会话，密码是装饰性的。不要"顺手"加 REST/WS 鉴权，别拿它挡人。
    - **共享范围（用户定）：只有「加载的项目」和「项目里的会话」按账户分，其余一律共享。**
      | 按账户 | 共享 |
      |---|---|
      | 项目列表 `registry.json` | 偏好、节点表、WebPush 订阅、提示词、看板模板 |
      | **会话库**（`MetaDir()`：会话 DB + exchange/aux） | **上传的文件、文件批注**（`SharedMetaDir()`） |
      | **看板任务库**、定时任务 | agent 配置、**agent 进程池**、探针、relay/update/e2ee/auth/notify |
      - 两个 meta 解析器：`MetaDir()` 是账户私有的会话/任务目录；`SharedMetaDir()` 是上传与批注，
        按注册表记的 `MetaLocation` 算、**不看账户**（两个账户必须算出同一个答案）。
        主账户两者相同。改路径相关代码别用错。
      - 共享一律用**同一份实例**，不是"同一份文件两份实例"：后者对带调度/后台循环的服务会双跑同一个任务。
        看板/定时任务因此必须每账户一个实例——它们按本账户项目调度，且执行时用本账户的 session manager
        （`scheduled/tasks.go` 的 `registry.GetSessionManager`），共享实例说不清该跑谁的会话。
      - 为什么会话库和任务库按账户分、上传不按：任务库若共享，两个调度器读同一份文件会双跑，
        且"该用谁的会话跑"无法自洽；上传与批注没有这层耦合，共享即所见即所得。
    - 数据根：主账户 `<cfg>/`（迁移前的存量数据，行为零变化）+ 项目内 `.mindfs/`；
      其余账户 `<cfg>/users/<id>/`，meta 落 `<cfg>/users/<id>/meta/<rootID>`，**永不碰项目里的 `.mindfs`**。
      `users.json` 的 `primary_user_id` 记录归属；主账户删不掉，要删先 `POST /api/users/{id}/primary` 转移。
    - 每账户一份 `api.AppContext`，由 `server/app/workspace.go` 惰性构建、
      `api.ScopedRouter` 按 `user=` 派发到该账户的 handler 实例——**211 处 `h.AppContext` 一处都不用改**。
      **StreamHub 必须每账户独立**，否则 WS 广播跨账户泄漏。
    - **meta 必须按账户分**（会话/任务）：`RootInfo.MetaRoot` 非空时 `MetaDir()` 返回 `<账户目录>/meta/<rootID>`，
      恒为 home 语义。默认 meta 是"项目内"，两人加同一项目会共用会话库（默认踩坑）。
      加目录时 `pending.EnsureMetaDir()` 会预建 meta，漏传 `MetaRoot` 会在**别人的项目目录里建出 `.mindfs`**。
    - **前端 `user=` 只在 `services/base.ts` 注入**（`appPath/appURL/wsURL` 三处唯一汇聚点）。
      同源判定必须把 `ws→http`、`wss→https` 归一后再比：裸 `origin` 比较会让 `ws://host` ≠ `http://host`，
      本机 WS 被误判成跨节点而丢掉 `user=`，**所有账户的实时流落到主账户 hub**（实测踩过，见 `web/tests/multi-account-partition.test.mjs`）。
      跨节点请求不带本机账户 id（账户表每台机器独立，带过去对方会 404）。
    - 共享契约有测试守着：`server/app/workspace_test.go`（共享必须是同一实例、项目/会话必须分开）。
    - **旁路状态必须逐节点拉，且合并只能增量**（2026-09-28 修蓝灯不亮）：
      `SessionItem` **没有** `running` 字段，小蓝灯只由 `session.pending` 决定，而它的真值来自独立的
      `multiProjectPendingByKey` —— 这类「不随主数据返回」的状态要跟 `loadMultiProjectSessionGroups` 一样遍历节点表。
      两个坑：① `appPath(path)` 不传 `nodeId` 会**静默打当前激活节点**（`getApiBaseURL(undefined)` → `getActiveNode()`），
      跨节点场景必须显式传；② 合并**不能整体替换**——切节点时 `nodes-changed` 会立刻刷新，整体替换会让旧节点在跑的
      会话当场全灭，节点请求失败也会被误当成「没有在回复」。只重算本轮成功节点的键，其余原样保留，
      实现见 `appSession.ts` 的 `mergeReplyingStateByNode` + `scope.ts` 的 `sessionKeyNodeId`。
      建键用 `scopeSessionKey(nid,…)` 而非 `rootSessionKey()`：后者走 `getNodeIdForRoot`，同名项目跨节点会错记。
      远端节点的 `session.done` 收不到（没订阅那些会话），靠 `App.tsx` 里 5s/visible-only 轮询兜。
      回归测试 `web/tests/cross-node-replying-state.test.mjs`。

13. **看板模型（2026-09 重做）**：卡=任务，列=全局状态（待开始/进行中/等待你/已完成/失败·取消，前端 `kanbanStageColumns`）；
    - **任务自带流水**：`Task.Stages []StageTemplate` 是创建时从预设拷贝的**快照**，之后与预设无关（改/删预设不影响在途任务）。
      预设（`task_templates`）退化为「新建时可套用的模板」。旧任务（无快照）在 `loadForMove` 里惰性从模板回填。
    - **无并发调度**：`queued` 与调度槽位已删除（迁移时 queued→pending），新建任务恒为 `waiting_user`；同一 agent 阶段的重复执行由 `taskRun/taskPend` 去重守卫拦。
    - 任意状态可追加 comment 作为下一段 prompt（`POST /api/tasks/{id}/add-stage`，运行中排队尾；waiting_user 时追加即批准当前段并开跑）。
    - **跨项目工作台**：无项目时任务视图渲染 `web/src/components/WorkspaceKanban.tsx`，数据来自 `GET /api/tasks/overview`（`Service.Overview` 汇总各项目，单项目失败跳过）。
      卡片操作按卡片自带 `root_id` 派发（完成/立即执行/跳会话/跳项目），详情先切项目再选中任务。
    - 前端守卫：`web/tests/workspace-kanban.test.mjs`（接线与 i18n key 契约）。
14. **worktree 会话靠 repoint 彻底搬家**（`server/internal/api/usecase/session_repoint.go`）：
    - **问题**：mindfs 为会话在 `<root>/.worktree/` 开工作树（`AppContext.CreateTaskWorktree`）。worktree 删掉后，
      转录仍在 `~/.claude/projects/-home-...-mindfs--worktree-<name>/` 下、cwd 指向已不存在的目录 —— 会话能读，
      **但不能在 mindfs 里继续聊**（主 checkout 开会话会写回主 slug，而 mindfs 侧 `related_worktree` 钉着旧 cwd，两边打架）。
    - **解法**：`POST /api/sessions/{key}/repoint`，或 `POST /api/sessions/repoint`（body 带 `agent_session_id`，
      给只有 `CLAUDE_CODE_SESSION_ID` 的脚本用，服务端反查 mindfs 会话）。四件事按**固定顺序**做：换 `agent_session_id`
      （`claude.RepointTranscript` fork 后把转录搬进主 slug 目录）→ 换绑定 id + 推进游标（**一个事务**，
      `Manager.RepointAgentBinding`）→ `ClearRelatedWorktree` 清归属。`tasks.main_session_key/worktree_path` 不动（纯历史）。
    - **两个必须守住的坑**：
      - **游标要推进到新转录 EOF，不能归零**。live 路径独占持久化、exchange 表已完整；游标写 0 会让下次 Full 同步
        整份重读转录，重导的行与实时写的行不逐字相同，2026-09-16「ask 下面又渲染一轮出现过的文字」就是这个形态。
      - **`agent_ctx_seq` 必须原样保留**（`b2a49c7` 修的 bug）。它是 Full 同步的幂等护栏，`externalSessionDeltaAfterCtxSeq`
        在 `agentCtxSeq <= 0` 时直接放行全部 —— 写 0 会让早已渲染过的回合被重导一遍。repoint 只换 id 和游标、不增删
        exchange，所以原值就是正确值。
    - **顺序不可换**：先 `pool.Close` 掐断活进程，之后才动文件（否则 claude 可能正按旧路径 append）；先搬文件再写游标
      （游标记的是新路径的 EOF）。旧 id 的转录**不删**，是回滚保险。
    - **没有 UI 按钮**：只为 worktree 收尾服务，由 `~/.claude/skills/worktree-finish/scripts/wt-finish.sh` 的
      `repoint` 子命令调用（`run` 的最后一步，`--detach --delay N` 让它掐断调用方之前先留时间给 agent 收尾报告）。
    - 端到端验过 task-1/8/9/10：转录行数零丢失、uuid 零重叠、type 直方图不变、ctx_seq 保留、同步零新增、
      活会话续聊的 `cwd` 已是主 checkout。
    - **先判断要不要 repoint —— 转录已在主 slug 时它解决不了任何事**（2026-10-02 实测）：
      Claude Code 按 **spawn cwd** 决定 slug，会话只要有一次是在主 checkout 起的，转录就已经在主 slug 下。
      此时 worktree 目录被手工删掉也**不会**让会话聊不了，唯一残留是 `sessions.related_worktree_json`
      还钉着坏路径 —— **清掉那一项就够了，不需要 fork、不换 session id、不动游标、不杀 agent 进程**。
      反过来，转录真在 `--worktree-*` 目录下时才必须走完整 repoint。判断法：看 `session_agent_bindings
      .external_source_path` 落在哪个 slug 目录。实测清完直接点「继续聊」即可无感续上。
15. **控制面只在主节点，数据面按机器分**（2026-10-04，详见 `docs/multi-node-control-plane.md`）：
    - **控制面**（账户表、偏好、提示词库、看板模板、节点表、WebPush 订阅、relay/e2ee 绑定、应用更新）
      **只有一份真相，在主节点**；**数据面**（项目列表、会话库、任务库、文件、git、agent 进程池、定时任务）
      每台机器各一份 —— 这本该如此。原先两类混在一起，控制面被复制并在各机漂移：
      两个节点的 `nodes.json` 互相矛盾（同一台机器在两边拿到**不同 node id**，跨节点 bug 的根源），
      模板本机 3 个 / PC 5 个，`stage_template.json` 只有 PC 有。
    - **节点角色**：`nodeinfo.Role`，启动参数 `-role worker` 或配置 `{"role":"worker"}`。
      `control` 是**默认值**（零值即它，不配置等于改造前行为）；`worker` 只提供数据面，
      控制面端点与 `GET /` 一律 403、不注入静态目录。前缀表在 `nodeinfo.ControlPlanePrefixes()`，
      `role_test.go` 钉住「数据面端点必须不在表内」。
      **配置文件是 `<config-dir>/config.json`，不传 `-config` 时读这个缺省路径** ——
      曾经 role 写进 config.json 却不生效，因为 `loadStartupConfig` 在路径为空时直接返回，
      压根没人读文件。**而且修复必须落在 `cli/cmd`**：`make build` 装的是 `./cli/cmd`，
      `server/cmd/mindfs-server` 只有 `make dev-backend` 用，**发布二进制里没有后者的代码** ——
      第一版修复改在 `mindfs-server`，单元测试绿、真机 role 仍是 control。
      另注：`cli/cmd` 在根 module，import 不到 `server/internal/*`（Go internal 规则），
      所以 `StartOptions.Role` 是 `string` 而非 `nodeinfo.Role`，归一化在 `app.Start` 里做。
      缺省文件不存在是常态（不报错）；显式 `-config` 指向缺失文件仍报错；flag 优先于文件。
      测试 `cli/cmd/startup_config_test.go`。
    - **前端铁律**：控制面请求用 `controlPath`（打页面服务器），数据面继续用 `appPath`/`appURL`
      + `nodeId`（跟随选中节点，跨节点扇出是浏览器做的，没有后端代理层）。
      `controlPath` **故意不接受 `nodeId`** —— 想传 nodeId 说明要的多半是数据面请求。
      判断法：「这东西在另一台机器上有意义吗？」有 → 数据面；没有，只有一份该是权威的 → 控制面。
      `agent-config` 刻意留在数据面（改的是本机运行时，worker 需要）。
    - **单节点下改绑零行为变化**：`pageServerPath` 与 `appPath` 同源，只有多节点才有差异 —— 而那正是要修的 bug。
    - **两个连带后果**（漏了会在线上才发现）：
      ① **建任务必须内联流水** —— 模板库只在主节点，而任务可以建在运行节点上，
      后端 `CreateTask` 在 `stages` 为空时回查**本机**模板库必然失败（`task template not found`）。
      前端 `createTask` 每次带 `stages`（`appTask.ts` 的 `taskStagesForCreate`）并快照 `task_template_name`。
      注意 `applyStageOverride` 无覆盖时返回 `undefined`（最常见的路径），`taskStagesForCreate` 专门兜它。
      ② **模板按项目过滤，不按节点路由** —— `TaskTemplate.RootID` 空=全局，`?root=<id>` 返回「全局+本项目」并集。
    - **探测角色打 `/health` 不是 `/api/node-info`**：后者自己在控制面表里，worker 上必然 403，
      会让每个运行节点都被误判成「离线」。`/health` 是公开端点且刻意不在表内。
    - 迁移：`scripts/migrate-control-plane-to-primary.sh`（默认预览，`--apply` 才写，幂等）。
      WebPush 订阅**不能迁**（VAPID 私钥各自独立，密文绑定密钥），要在主节点重新订阅一次。
    - 存量控制面数据冻结在运行节点上**不删**；`session_project_pins` 的碎键**不迁移** ——
      键里含 node id，重写收益不抵风险，且改 role 后新写入即自愈。



## 数据流

```
浏览器 ←WS→ ws.go（单读分发）→ usecase 层 → agent pool → claude/codex session
                                                   ↓
                                          session manager（SQLite WAL）
```

## 开发流程

```bash
make test                   # Go 测试
make build && make install  # 完整构建 + 部署
sudo systemctl restart mindfs   # 本机生效（用户执行）；WSL 用 ssh wsl 'systemctl --user restart mindfs'
```

- 提交用 Conventional Commits（中文描述）。
- 改 ws.go handler 时：检查是否在 goroutine 内派发，避免阻塞读循环。
- 改 agent session 时：Interrupt/Cancel 调用必须有超时，永远不要 `context.Background()` 裸用。
- 前端改完：`make build-web && make install`（不自动 embed）。
