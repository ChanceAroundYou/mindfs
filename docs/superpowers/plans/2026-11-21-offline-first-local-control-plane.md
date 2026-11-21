# 离线优先的本地控制面：执行计划

Plan 2026-11-21。设计见 [spec](../specs/2026-11-21-offline-first-local-control-plane-design.md)。
本文把设计拆成可逐条验收的任务；**每条都先有会失败的用例，再写实现，再做变异验证**。
未在本文列出的波次不执行。

已确认口径（2026-11-21，用户拍板）：**Q1 边缘出页面并转发控制面请求；Q2 复制口令哈希；
Q3 同步通道一律 https；Q4 冲突按域冻结**。其余按 spec §12：D1 只同步控制面、D5 在线禁止
本地直写 —— 这两条是架构分水岭，改则重开设计评审。D8 已作废（账户 id 由用户名确定性派生，
跨机天然一致，不需要改名搬目录）。

Scope: G-PF。

## 前置约束

- 只动 `server/`、`cli/`、`web/`、`scripts/`、`docs/`；不碰 `agents.json`、`config.json`、凭据。
- 本机 `sudo systemctl restart mindfs` 仅用户执行；WSL/fn 由 Agent 重启；部署走 `ship` 之外
  的常规 `deploy-all` 前必须先过全量门禁。
- 新增 Go 文件 ≤500 行、函数 ≤50 行、嵌套 ≤4 层；新包必须有包注释说明边界。
- 每条提交信息带 `Scope: G-PF`；新增/改动的文件登记进 `docs/upstream-customizations.yaml`
  对应组，并补「针对性测试」表。
- 不引入第三方同步库；只用 stdlib（`crypto/hmac`、`encoding/json`、`archive/tar`、`compress/gzip`）。

## 波次总览

| 波 | 主题 | 依赖 | 可否单独上线 |
|---|---|---|---|
| P0 | 基线取证与契约 | 无 | 是（纯测试/文档） |
| P1 | 本机 control + 初始前端包 | P0 | 是（验证既有能力，少量文档/脚本） |
| P2 | 同步内核与域适配器 | P0 | 否（未接线，默认关闭） |
| P3 | 在线控制面转发 | P2 | 是（`enabled=false` 时行为不变） |
| P4 | 离线本地模式 | P3 | 是（默认关闭） |
| P5 | 恢复合并与冲突 | P3、P4 | 是（默认关闭） |
| P6 | 前端模式/冲突/节点可达性 | P5 | 是 |
| P7 | 前端资源镜像 | P2 | 是 |
| P8 | 端到端验收与上线 | 全部 | 是 |

---

## P0 基线取证与契约

### T1 钉住「双 control 各自为政」的现状

- 目标：用测试把当前行为固定下来，避免后续误以为已有同步。
- 改动：新增 `server/app/control_plane_isolation_test.go`。
- 用例：
  1. 两个临时 configDir 各起一份 `auth.Store`，同用户名不同密码互不可见；
  2. 两个 `nodes.Store` 各写一条，互不出现；
  3. `workflow.Store` 两份目录的 `workflows.json` 互不影响。
- 验收：`go test ./server/app/ -run ControlPlaneIsolation -count=1`。
- 变异：把测试里的两份 store 改成共享一份目录，用例必须红。
- 执行记录（2026-11-21）：3 条用例绿；三处变异（auth / nodes / workflows 各把两份 store
  指向同一目录）各自打红后还原。构造顺序是契约：B 必须在 A 写完后再建，否则内存缓存会假绿。

### T2 钉住控制面路由表

- 目标：转发/同步的域必须与 `nodeinfo` 的前缀表同源，不能各写一份。
- 改动：`server/internal/nodeinfo/role_test.go` 增加「前缀表快照」用例，
  断言 `ControlPlanePrefixes()` 的集合等于当前 11 条（显式列出）。
- 用例：新增前缀必须让用例红，迫使同步计划同步更新。
- 验收：`go test ./server/internal/nodeinfo/ -count=1`。
- 执行记录（2026-11-21）：`TestControlPlanePrefixesSnapshot` 已存在于 `role_test.go`（显式
  列出 11 条），复核通过，本轮不重复新增。

### T3 记录「失败不等于空」的基线

- 目标：离线时远端节点失败的 UI 行为有回归锚点。
- 改动：`web/tests/node-unreachable-baseline.test.mjs`，对 `useManagedRoots` 与
  会话列表的源码契约做断言：**当前**失败路径仍是返回空/部分结果（记录缺陷）。
- 验收：`npx vitest run web/tests/node-unreachable-baseline.test.mjs`。
- 说明：本用例在 P6 会被改写成「必须显式不可达」，届时红→绿即证明修好了。
- 执行记录（2026-11-21）：取证纠正了本任务的假设 —— 单节点失败**已经**是「弹提示 +
  保留失败节点的条目」（`useManagedRoots.ts` 的失败分支与 `preserved` 判据），不是缺陷。
  用例改为钉住这两条已做对的锚点，另加两条仍缺的：节点表同步失败被 `catch(() => {})`
  吞掉、零项目分支不区分「真空」与「拉不到」。4 条用例绿。

---

## P1 本机 control + 初始前端包

### T4 验证 Windows 上 `role=control` 可以发页面

- 目标：确认「hasee 当控制端」的最小前提，不改代码。
- 步骤（在 hasee 上，由用户执行或 Agent 远程）：
  1. 安装包安装到 `C:\Users\<user>\AppData\Local\MindFS`；
  2. `config.json` 写 `"role": "control"`（或删掉 role 走缺省）；
  3. 启动后 `GET http://127.0.0.1:7332/` 返回 200 且含 `index.html`；
  4. `GET /api/node-info` 返回 `role=control`；`GET /api/auth/status` 200。
- 验收证据：三条 curl/Invoke-WebRequest 输出 + 版本号，记入本文「执行记录」。
- 若失败：只允许改 `scripts/install.ps1` / `scripts/build-all.sh` 的路径，
  不改 `server_helpers.go` 的查找顺序（那是上游行为）。

### T5 安装包内置初始前端 + 离线自检

- 目标：首次安装不依赖 home 也能打开页面。
- 改动：
  - `scripts/install.ps1`：安装后校验 `share\mindfs\web\index.html` 与 `favicon.svg` 存在，
    缺失则安装失败（现在只是拷贝，失败会静默留下半套）；
  - `scripts/build-all.sh`：发布包内写 `web/BUILD_VERSION`（值取构建版本），供 T20 对账；
  - `docs/ios-shell.md` 之外的部署文档补一节「离线 control 安装」。
- 用例：`scripts/install_test.go`（若已有安装器测试则扩展）断言缺 `index.html` 时返回非零。
- 验收：`go test ./scripts/...`；Windows 上断网启动页面 200。

---

## P2 同步内核与域适配器

### T6 新建 `server/internal/controlsync` 包与 Record 模型

- 目标：纯数据结构与 hash/规范化，不接线。
- 文件：
  - `record.go`：`Record{Domain,Key,Hash,Rev,UpdatedAt,Origin,Deleted,Payload}`；
    `CanonicalJSON`（键排序、去空白）、`HashRecord`、`RecordKey` 校验（非空、≤256 字节、无控制字符）。
  - `record_test.go`：同一对象不同键序/空白 → 同 hash；`deleted` 不改 payload hash；
    非法 key 拒绝。
- 变异：`CanonicalJSON` 不排序 → 用例红。
- 验收：`go test ./server/internal/controlsync/ -count=1`。
- 执行记录（2026-11-21）：6 条用例先红后绿（`record.go` + `record_test.go`）。变异「去掉
  重排与 `UseNumber`、直接返回 `json.Marshal` 的字节」让「键序无关」与「struct/map 同 hash」
  两条同时红后还原。两处实测结论写进代码注释：hash 只覆盖 `Payload`（墓碑保留最后内容 hash），
  `CanonicalJSON` 必须走 `UseNumber`，否则 2^53 以上的整数会被 float64 舍入、同一份数据在
  不同机器上算出不同 hash。

### T7 SQLite schema 与迁移

- 目标：主节点与边缘共用一份 DDL（两处建表必须逐字一致，学 `EnsureOutboxTables` 的教训）。
- 文件：`store.go`（`Open(path)`、`Migrate(ctx)`、`Close`）、`store_test.go`。
- 表：`records`、`changes`、`applied_changes`、`pending`、`state`、`conflicts`（见 spec §4）。
- 用例：空库迁移两次幂等；并发 Open 同一路径只有一个成功（SQLite busy 处理）；
  事务回滚不留半条 change。
- 变异：迁移去掉 `IF NOT EXISTS` → 二次迁移红。
- 执行记录（2026-11-21）：`store.go` + `store_test.go`，4 条用例先红后绿，`-race` 绿，
  `-v` 下 T6/T7 共 10 条全过。
  - **一处与计划不符（按证据改口径）**：「并发 Open 同一路径只有一个成功」在当前实现下
    不可能成立 —— `sql.Open` 是惰性的，同一进程内多个句柄指向同一文件本来就都能打开，
    要「只有一个成功」得引入跨平台文件锁（`flock` / `LockFileEx`），而 Windows 无 `flock`、
    崩溃残留的锁文件又会让服务起不来。改为钉住真正要保的性质：**8 个 goroutine 并发
    `Open` + `Migrate` + 写记录全部成功，且分配到的 `rev` 互不重复**。单实例由部署层保证
    （每台机器一个 configDir、一个 systemd 单元）；**残留风险**：同机起两个实例会交错分配
    `rev`，不报错也不隔离，需要 P4 的 `state.instance_id` 上线后补一道显式检测。
  - 为了这条性质，DSN 带 `_txlock=immediate`：`rev` 用 `MAX(rev)+1` 分配，延迟事务会让两个
    并发事务读到同一个 `MAX`，随后在 `changes.rev` 上撞车。三处变异各自打红后还原 ——
    去掉 `IF NOT EXISTS`（二次迁移红）、去掉 `_txlock=immediate`（并发迁移 `SQLITE_BUSY`）、
    `rev` 改成常量 1（并发与单调两条用例同时红）。
  - **表名合并**：spec §4.3 的 edge 表 `replica` 与 §4.2 的 `records` 列完全相同，这里只用
    `records` 一张，避免两份逐字重复的 DDL 各自漂移；角色差异只体现在用哪几张表。

### T8 域适配器接口与六个实现

- 目标：把「文件/Store ↔ Record」的读写收敛到一处。
- 接口：

```go
type Domain interface {
    Name() string
    Snapshot(ctx context.Context) ([]Record, error)
    Apply(ctx context.Context, rec Record) error // 必须抑制变更记录
    Keys(ctx context.Context) ([]string, error)  // 用于对账
}
```

- 实现文件（每个 ≤300 行）：
  - `domain_users.go`：按 `username` 归一，含 `PrimaryUserID` 标记记录；
  - `domain_preferences.go`：顶层字段，`agents` 等结构体整体作为一个字段值；
  - `domain_nodes.go`：key = `nodeBaseKey(url)`，id 只进 payload；
  - `domain_prompts.go`：每账户一个适配器，key = `sha256(text)`；
  - `domain_pins.go`：每账户一个适配器；
  - `domain_workflows.go`：`wf:<id>:meta|draft|version:<n>`，版本不可变。
- 用例（每域至少）：Snapshot→Apply→Snapshot 往返 hash 不变；删除产生 tombstone；
  未知字段保留；空库不 panic。
- 变异：`nodes` key 改用 id → 「两台机器同一 URL」用例红；`preferences` 整文件一个 key →
  字段级合并用例红。
- 验收：`go test ./server/internal/controlsync/ -count=1` 全绿。

### T9 变更采集

- 目标：控制面写入后产生 record delta，且**不靠改每个 Store**。
- 文件：`capture.go`、`capture_test.go`。
- 机制：
  1. 每个域保存上次快照 hash 表（内存 + `records` 表）；
  2. 控制面非 GET 请求成功后（HTTP 中间件，按路径映射域）触发对应域 `Snapshot` 并 diff；
  3. 每 15 秒兜底全量 diff 一次，捕捉非 HTTP 写入（启动迁移、定时任务）；
  4. diff 在 `records`/`changes` 同事务里落库并分配 `rev`。
- 用例：新增/修改/删除各产生一条 change；无变化不产生 change（防抖）；
  应用远端记录时不产生 change（抑制）；同一个 rev 不重复分配。
- 变异：去掉抑制 → 「apply 后自激」用例红；去掉兜底扫描 → 非 HTTP 写入漏采用例红。
- 验收：`go test ./server/internal/controlsync/ -count=1`；`-race`。

---

## P3 在线控制面转发

### T10 转发器

- 目标：`role=control` 且 `control_sync.enabled` 时，控制面请求转发到 `primary_url`。
- 文件：`server/internal/api/controlproxy.go`、`controlproxy_test.go`。
- 规则：
  - 只转发 `nodeinfo` 控制面前缀，且排除本地域（`/api/web-push`、`/api/app/update`、
    `/api/node-info`、`/api/control-sync/*`）；
  - 保留方法、query、body、`Content-Type`；回传状态码与响应体；
  - 不转发 `Cookie`/`Authorization` 之外的本地头；补 `X-MindFS-Forwarded-By`；
  - 超时 10 秒；失败返回 `503 primary_unreachable`，**不在本地执行**；
  - SSE/流式响应按时长上限透传；
  - **`primary_url` 必须是 `https://`**（用户裁定 Q3）：解析配置时校验，非 https 视为
    配置错误、同步不启动；TLS 信任根只认系统根 + `ca_file`，不提供跳过校验的开关。
- 用例：httptest 主节点记录收到的请求；状态码/头/体逐字；失败时本地 Store 未被写；
  本地域请求不转发；`http://` 的 `primary_url` 被拒绝。
- 变异：把转发改成「失败回落本地」→ 双写用例红。
- 验收：`go test ./server/internal/api/ -run ControlProxy -count=1`。

### T11 边缘副本追平

- 目标：转发期间本地副本随主节点变更流更新（读路径仍走转发，本阶段不改）。
- 文件：`server/internal/controlsync/replicator.go`、`replicator_test.go`。
- 逻辑：每 5 秒 `GET /changes?since=base_rev`，逐条 `Apply` 到本地 Store；
  成功后推进 `base_rev`；失败只记日志并保留水位。
- 用例：远端新增/删除/修改各追平；中途失败不推进水位；重复拉取幂等。
- 验收：`go test ./server/internal/controlsync/ -count=1`。

---

## P4 离线本地模式

### T12 状态机与模式开关

- 目标：`online / offline-local / reconciling / conflict-review` 由服务端持有并持久化。
- 文件：`server/internal/controlsync/mode.go`、`mode_test.go`、
  `server/internal/api/http_control_sync.go`、`http_control_sync_test.go`。
- 规则：
  - 探测 10 秒一次、3 秒超时；3 次失败降级，2 次成功升级；
  - 状态落 `state` 表；`GET /api/control-sync/status` 两种模式都可用；
  - `POST /api/control-sync/mode` 支持 `{action:"local"|"retry"}`；
  - 模式变化写一条可见系统事件（复用现有通知通道）。
- 用例：假主节点 httptest 控制成功/失败计数；降级后转发停止；恢复后进入 `reconciling`；
  重启后模式保持；显式切换立即生效。
- 变异：降级后仍转发 → 离线用例红；模式只存内存 → 重启用例红。
- 验收：`go test ./server/internal/controlsync/ ./server/internal/api/ -run ControlSync -count=1`。

---

## P5 恢复合并与冲突

### T13 推送与幂等

- 目标：按 spec §6.1 实现 `POST /push`。
- 文件：`merge.go`、`merge_test.go`、`http_control_sync_push_test.go`。
- 用例：base_hash 命中 → 应用并分配新 rev；重复 change_id → 只应用一次；
  同 hash → 收敛；三方不同 → conflict 且记录未变；删除幂等。
- 变异：冲突时仍然写 → 覆盖用例红；不查 `applied_changes` → 重放用例红。
- 验收：`go test ./server/internal/controlsync/ -run Push -count=1`。

### T14 边缘合并与冲突落库

- 目标：拉取时按 `pending` 三方比对，冲突两边都保留。
- 文件：`reconcile.go`、`reconcile_test.go`。
- 用例：spec §6.2 的四种分支各一条；批量按页推进；中途 kill 后从 `base_rev` 续传；
  冲突期间 `pending` 不清空。
- 变异：冲突时清 pending → 数据丢失用例红；拉取先推进水位再应用 → 断点用例红。
- 验收：`go test ./server/internal/controlsync/ -run Reconcile -count=1`、`-race`。

### T15 冲突查询与裁决

- 目标：`GET /conflicts`、`POST /conflicts/{id}/resolve`；裁决后写回并追平。
- 文件：`conflicts.go`、`conflicts_test.go`、`http_control_sync_conflicts_test.go`。
- 用例：`local`/`primary`/`payload` 三种选择；重复裁决 409；并发裁决只有一个生效；
  裁决后 `pending` 清空、水位推进；`users` 域响应不含 password hash。
- 变异：裁决写错方向 → 两端一致性用例红；响应带 hash → 安全用例红。
- 验收：`go test ./server/internal/controlsync/ -run Conflict -count=1`。

---

## P6 前端模式、冲突与节点可达性

### T16 模式横幅与切换

- 目标：四态横幅 + 手动切换 + 状态轮询。
- 改动：`web/src/services/net/controlSync.ts`（新）、`web/src/components/app/ControlModeBanner.tsx`（新）、
  App 接线、i18n zh/en 各一套键。
- 用例：`web/tests/control-mode-banner.test.mjs`（源码契约 + 纯函数状态映射）。
- 验收：`npx vitest run web/tests/control-mode-banner.test.mjs`、`tsc -b --noEmit`。

### T17 冲突面板

- 目标：按域分组、逐条选择、批量二次确认、裁决后刷新。
- 改动：`web/src/components/app/SyncConflictPanel.tsx`（新）、`web/src/services/net/controlSync.ts`。
- 用例：`web/tests/sync-conflict-panel.test.mjs`：分组顺序、条数、确认文案、
  批量只发一次请求、`users` 域不展示 hash。
- 验收：同 T16。

### T18 节点可达性

- 目标：不可达节点显式标注，绝不返回空列表。
- 改动：`web/src/services/net/nodeHealth.ts`（新）、`useManagedRoots`、会话列表、
  文件树、节点切换器。
- 用例：改写 P0 的 `node-unreachable-baseline.test.mjs` 为正向断言：
  探测失败 → 节点状态 `offline` → 加载器返回 `{unreachable:true}` 而不是 `[]`；
  操作按钮禁用。
- 变异：把 `offline` 当空列表 → 用例红。
- 验收：`npx vitest run web/tests/node-unreachable-baseline.test.mjs`。

---

## P7 前端资源镜像

### T19 主节点清单与打包

- 目标：`make build-web` 产出 `ui-manifest.json`；`GET /api/control-sync/ui/{manifest,bundle}`。
- 改动：`web/vite.config.ts`（生成清单）、`scripts/build-all.sh`、
  `server/internal/api/http_control_sync_ui.go`、`http_control_sync_ui_test.go`。
- 用例：清单含每个产物的 sha256；bundle 是 tar.gz；无 token 404；路径穿越 400；
  超过 200 MB 拒绝。
- 验收：`go test ./server/internal/api/ -run ControlSyncUI -count=1`。

### T20 边缘镜像与切换

- 目标：下载 → 校验 → `<version>.tmp` → rename → `.complete` → 指针切换；保留 2 版。
- 改动：`server/internal/controlsync/ui_mirror.go`、`ui_mirror_test.go`、
  `server/app/server_helpers.go`（静态目录解析插入镜像路径）。
- 用例：完整校验通过才切换；坏 hash 保留旧版；`min_server_build` 不满足不激活；
  Windows 无符号链接也能切换（只测指针文件）；并发切换只有一个赢。
- 变异：不校验 hash → 坏包用例红；指针指向未完成目录 → 白屏用例红。
- 验收：`go test ./server/internal/controlsync/ -run UIMirror -count=1`。

---

## P8 端到端验收与上线

### T21 双实例隔离验收

- 目标：真实进程验证 spec §13 的 A1–A5。
- 做法：沿用 `[[mindfs-isolated-verify-instance]]`：两个独立 `XDG_CONFIG_HOME` 与端口
  （如 7470 主 / 7471 边缘），真实二进制，不碰 7331。
- 脚本：`e2e/test_control_sync_offline.py`（新），覆盖：
  在线转发、断网降级、离线写、重启保持、恢复无冲突、恢复有冲突。
- 验收：脚本退出 0，输出每场景 PASS；失败必须贴原始断言。

### T22 前端镜像与安全验收

- 脚本：`e2e/test_control_sync_ui_and_auth.py`：真浏览器打开边缘页面、
  断言横幅、冲突面板、节点不可达；curl 断言无 token 404 / 错签 401 / 重放 401。

### T23 全量门禁与上线

- 命令：
  ```bash
  go test -count=1 ./...
  go build ./...
  cd web && npx tsc -b --noEmit && npx vitest run
  make build
  make check-upstream
  ```
- 上线顺序：hasee（用户安装/重启）→ 主节点（用户重启）→ WSL/fn（Agent 可重启）。
- `control_sync.enabled` 首次上线保持 `false`；先只用前端镜像与只读副本，
  观察 24 小时无异常后再开 `true`。开关在 `config.json`，不改代码。

---

## 风险与回滚

| 风险 | 表现 | 处置 |
|---|---|---|
| 同步回环自激 | 两台机器 rev 无限增长 | `Apply` 抑制 + 变异用例；发现即关 `control_sync.enabled` |
| 账户 id 漂移 | 离线登录后看到空项目 | T8 按用户名映射；首次加入前备份 `baseDir`，冲突即中止 |
| 冲突面板误操作 | 覆盖主节点账号 | 批量二次确认 + `users` 域单独警示 + 服务端审计行 |
| 前端镜像坏包 | 页面白屏 | 校验失败不切换；`current` 只指向 `.complete` |
| 在线转发断流 | 主节点抖动导致控制面 503 | 探活提前降级；前端提示重试；不本地双写 |
| 数据面被误当控制面 | 复制任务库导致双跑 | 域清单白名单，只允许 §5 的六个域 |

回滚：`control_sync.enabled=false` 即恢复今天的行为（转发与镜像都停，本地 Store 不动）；
镜像路径不在 `MINDFS_STATIC_DIR` 里时，删 `current` 指针即回到安装目录的 `web/`。

## 执行记录

（随任务逐条追加：任务号、提交、门禁命令与输出、变异验证结果、未覆盖项。）
