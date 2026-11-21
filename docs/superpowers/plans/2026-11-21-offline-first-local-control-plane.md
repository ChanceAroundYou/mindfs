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
  - **追加（2026-11-21）**：全量门禁并发跑 `controlsync` 与 `kanban` 时，8 路并发迁移里有
    worker 直接拿到 `database is locked (5) (SQLITE_BUSY)`（40 次压测稳定复现）。根因是
    **SQLite 不为 `journal_mode` 切换调用 busy handler**，而它写在 DSN 的 pragma 里、在连接
    建立时执行，没有第二次机会。改为 DSN 只留 `busy_timeout(5000)` 与 `_txlock=immediate`，
    `PRAGMA journal_mode=WAL` 挪进 `Migrate` 首次执行，并对 BUSY 做 60×25 ms 退避重试
    （上限 1.5 s；WAL 落在库头里，之后每次打开仍是 WAL）。新增
    `TestMigrateWaitsForAnotherWriter`：另一连接持写锁时迁移必须等待、释放后 150 ms 内完成；
    把 `migrateAttempts` 改成 1 后该用例变红，还原后复绿。提交 `65baf743`。

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
- 执行记录（2026-11-21）：六个域适配器 + `Registry`，六组共 34 条用例，13 处变异各自打红后
  还原（hash 不入载荷、key 与载荷 id 不符、主账户标记不排最后、主账户墓碑、孤儿 meta 导出、
  版本可覆盖、旧草稿回退、`NextSeq` 不推进等）。提交 `66810078`。
  - **四处在计划基础上按证据收紧**（理由都写进了代码注释）：① `Domain` 接口去掉 `Keys` ——
    对账的 key 集合由 `Snapshot` 直接得出，多一个方法就多一条会漂移的实现路径；② `pins` 不是
    纯文件投影，既有 `RestoreProjectPin` / `RestoreSessionPin` 才有「按时间戳覆写 + 写盘失败
    回滚内存」，适配器只做 key 与载荷编解码；③ `prompts` 的 `PromptStore.SetItem` 在满 50 条
    时**报错**而不是挤掉最旧一条 —— 远端记录不该悄悄顶掉本地数据；④ `users` 的主账户标记记录
    必须排在最后，`SetPrimary` 要求账户已存在，标记先到会失败。
  - **两处 key 规则来自既有教训**：`prompts` / `pins` 的存储按账户分目录，key 必须带账户
    （`<account>/<...>`），否则不同账户下同名提示词、同一个 pin 作用域会互相覆盖（与 G-NR
    「项目身份 = (节点, rootId)」同源）；`nodes` 的 key 是归一化 URL 而非 id —— 两台机器同一个
    URL 是两条记录，同一台机器换 id 仍是同一条。
  - **workflows 的三条不变量**：版本只追加（同 id 同内容幂等，内容不同即
    `ErrWorkflowVersionImmutable`）；草稿不回退（只接受更高 revision）；`NextSeq` 由草稿 revision
    与版本 seq 推上去，否则目标机下一次编辑会退版号或与已有版本撞号。为此补了
    `TestWorkflowsDomainSeqFollowsDraftWithoutVersions`：只有草稿、没有版本的 workflow 也必须推进
    seq（第一版变异只靠版本记录推进，被这条用例打红）。
  - **store 侧新增五个入口**（提交 `f1e4c85d`）：`auth.ExportUsers/ApplyUser/RemoveUserByName`、
    `pins.RestoreProjectPin/RestoreSessionPin`、`preferences.Fields/SetField`（磁盘上本版本不认识
    的字段原样保留并参与同步）、`usecase.PromptStore.SetItem`、`kanban.WorkflowStore` 的五个同步
    方法。理由是不绕开既有 store 的锁与缓存直接写 JSON —— 否则内存缓存与磁盘不一致，下一次读取
    就回退。auth 3 条 + kanban 3 条用例，3 处变异各自打红。
  - **未覆盖**：六个适配器的 `Apply` 尚未接变更抑制（T9）、未接 HTTP 转发（T10）；`users` 域只
    按用户名对齐，账户改名不在本轮范围。

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
- 执行记录（2026-11-21）：`capture.go` + `capture_test.go`，7 条用例 + `-race` 全绿。提交
  `f619c97d`。
  - **四条机制各自的落点**：基线存在内存 + `records` 表（启动从索引恢复，不重发整库）；
    `WriteCapture` 中间件只在非 GET 成功（2xx）后按路径白名单映射域并采集，失败不改写响应、
    留给 `Run(ctx, 15s)` 兜底；一次 diff 走新增的 `Store.WriteRecords` **单事务整批**落库；
    `Suppress` 在应用远端记录期间暂停采集，并在成功后**采纳基线**。
  - **「采纳基线」是抑制的关键**：只跳过当下采集不够 —— 远端 Apply 改的是同一份权威存储，
    快照上看不出是谁写的，下一次兜底扫描仍会把它当本地改动回传，两侧互相回传即成死循环。
    用例先按「只跳过」写，去掉采纳后立刻红（采集出 2 条自激变更）。
  - **两条语义细节**：墓碑携带**最后一次内容 hash**（否则「我删的」与「远端改完再删」无法
    区分）；`Suppress` 的 `fn` 失败时**不**采纳基线 —— 远端记录可能只落一半，宁可多一次
    同 hash 的幂等回声，也不能吞掉真实的本地改动。
  - **路径映射的取舍**：只映射 `/api/{users,preferences,nodes,prompts,pins,workflows}`；
    `/api/auth/login` 会动 users.json 的登录时间，但它是「登录」不是「账户变更」，算进 users
    域会让每次登录都产生同步记录，故不映射。`/api/node-info`、`/api/web-push`、
    `/api/app/update` 属本机状态，不参与同步。
  - **变异四处**（各自打红后还原）：去掉基线采纳 → 自激用例红；去掉兜底扫描 →
    非 HTTP 写入漏采用例红（5 s 超时）；删除不写墓碑 → 墓碑断言红；
    GET 也算写 → 中间件 GET 子用例红。实现前把 `capture.go` 移走确认三条用例因缺符号编译失败。
  - **未覆盖**：中间件与 `Run` 都还没接进 `server/app`（P3 接线时再挂）；`Suppress` 的使用方
    （T14 边缘合并）尚未实现，本任务只保证语义与用例。

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
- 执行记录（2026-11-21）：`controlproxy.go` + `controlproxy_test.go`，5 条用例全绿（api 包
  全量 60.4 s 绿）。提交 `189b1e6b`。
  - **实现取舍**：没用 `httputil.ReverseProxy` —— 它默认复制全部请求头（本机头会外泄）、
    自己改写 `X-Forwarded-For`，而这里要的是「只带 Cookie / Authorization / Content-Type、
    补一个 `X-MindFS-Forwarded-By`」的白名单语义，手写 30 行比配置 ReverseProxy 更直白。
  - **两段超时**：`ControlProxyTimeout`（10 s）只覆盖「等到响应头」；拿到响应后停掉定时器，
    body 按 `ControlProxyStreamTimeout`（10 min）逐块 `Flush` 透传。把总时长压在一个 ctx 上
    会把正常的长响应半路掐断。
  - **3xx 原样回传**（`CheckRedirect` 返回 `ErrUseLastResponse`）：主节点的路由决策不该被
    边缘改写。
  - **变异三处**（各自打红后还原）：转发成功后仍调用本地 next（模拟失败回落本地）→
    双写用例红；`ShouldForward` 去掉本机域排除 → web-push / app update / node-info 三处红；
    头白名单改成复制全部 → 本机头外泄断言红。
  - **未覆盖**：转发器还没挂进 `server/app` 的 mux（P3 接线时按模式包装）；`primary_url`
    的配置解析落在 T12 的状态机与配置结构里，本任务只保证构造级校验。

### T11 边缘副本追平

- 目标：转发期间本地副本随主节点变更流更新（读路径仍走转发，本阶段不改）。
- 文件：`server/internal/controlsync/replicator.go`、`replicator_test.go`。
- 逻辑：每 5 秒 `GET /changes?since=base_rev`，逐条 `Apply` 到本地 Store；
  成功后推进 `base_rev`；失败只记日志并保留水位。
- 用例：远端新增/删除/修改各追平；中途失败不推进水位；重复拉取幂等。
- 验收：`go test ./server/internal/controlsync/ -count=1`。

执行记录（2026-11-21）：

- 新增 `Replicator` 与线上类型 `RemoteChange`（`GET /api/control-sync/changes` 的响应形状；
  主节点 handler 在 T13 落地时复用同一个类型，避免两端各写一份 JSON 结构）。
  水位语义按设计 §6.2 落成 `since=base_rev+1`，不是 `since=base_rev` —— 服务端按
  `rev >= since` 取页，两者必须成对，否则每次都会重复取到已追平的那一条。
- `Store.ApplyRemoteChanges` 一页一个事务：`rev` 沿用主节点序号（它是水位不是本地自增）、
  `rev <= base_rev` 的记录跳过（重放旧页不得把副本倒回旧内容）、`records` 与 `state.base_rev`
  同生共死。**一个事务而不是逐条写**：半个 diff 提交出去，边缘就会永久跳过没落地的记录。
- 墓碑带最后一次内容的 hash 并在副本里保留（`payload` 为 NULL），这样合并阶段能区分
  「主节点删了」与「主节点改完又删」；`ApplyRemoteChanges` 因此要求每条都有 hash。
- 逐页提交 + 空页即停：进程死在两页之间，重启从已提交水位续拉，不重放整段。
  失败只经 `OnError` 上报（`Run` 里），`SyncOnce` 返回错误但不动水位。
- TLS 与 `api.ControlProxy` 刻意同源：https 强制、TLS 1.2 起步、系统根 + 可选 `ca_file`、
  禁重定向；任何一边松掉等于两边一起松。`newSyncHTTPClient` 留在 controlsync 供 T13 的
  push 侧复用。
- 用例 6 条：新增/修改/删除各追平（含墓碑 hash 断言）、失败不推水位且恢复后从 `since=1` 续传、
  重复拉取只问一次 `since=base+1`、分页序列 `1,3,5,6`、重放旧页不倒退、明文 primary_url 被拒。
  变异 4 处各自打红后还原：去掉 stale-rev 守卫（重放用例红）、不推 `base_rev`（追平用例
  超时红）、`since` 退回 `base_rev`（分页序列变成 `0,2,4,5` 红）、去掉 https 校验（明文用例红）。
- **未接通**：`Replicator` 尚未接进 `server/app`（与 T9/T10 一致，P3 统一接线）；把远端记录
  应用到权威存储、以及 pending/冲突判定属于 T14 的三方合并 —— 在线阶段按 D5 不在本地写，
  pending 恒为空，本阶段只维护副本。

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

执行记录（2026-11-21）：

- `ModeController` 持有四态（`online` / `offline-local` / `reconciling` / `conflict-review`），
  阈值按 spec §7 落成常量：探测 10 s、超时 3 s、连续 3 次失败降级、连续 2 次成功升级 ——
  3 × 10 s 正压在「断开后 ≤30 s 进本地模式」的预算内。`reconciling` 期间再连续失败 3 次
  退回 `offline-local`；**`conflict-review` 不受探测影响**（主节点掉线不会让冲突消失，
  它只能由裁决清掉）。
- 持久化走 `state` 表（`SetMode` / `RecordProbe` / `LoadState`，新 `store_state.go`）：
  重启后必须读回仍在 `offline-local`，否则会以 online 身份启动、把控制面写请求全转给一台
  仍不可达的主节点。`RecordProbe` 失败时**不覆盖 `last_primary_at`** —— 断线排查最需要的
  正是「上次成功连上是什么时候」。持久化里出现未知模式值时构造器直接报错（fail closed），
  不猜一个默认值糊过去。
- 探活用 `GET <primary>/api/control-sync/head`，**只判 2xx、不解析响应体**：把 head 解析
  压进探活，会让一次字段变更表现成「主节点挂了」。`NewHTTPProber` 与 `NewReplicator`
  共用 `parsePrimaryURL` + `newSyncHTTPClient`（https 强制、TLS 1.2、系统根 + `ca_file`）。
- 用户动作走 `POST /api/control-sync/mode {action}`：`local` 立即切本地模式不等计数；
  `retry` 当场探一次，通就直接进 `reconciling`、不通留在本地模式。**retry 探不通仍回
  200 + 最新 status**（`last_error` 写明原因）—— 用 5xx 表达「主节点不通」会让前端把
  「按钮动作失败」和「页面请求失败」混成一件事。
- `GET /api/control-sync/status` 两种模式都可用（转发器已把 `/api/control-sync` 列为本机域），
  载荷 `{mode, base_rev, last_primary_at, last_error}`。端点只在 `HTTPHandler.ControlSync`
  非空时注册，且 handler 内部再判一次 nil —— 两层**故意冗余**（验证过：单改任一层仍是
  404/正常，两层同时去掉才 panic）；未启用机器保持改造前行为（两条路径 404）。
- 模式翻转回调 `OnModeChange(from,to)` 已就位，供接线方落「可见系统事件」与前端横幅；
  本任务不接通知通道。
- 用例：`controlsync` 9 条（降级阈值、升级阈值、冲突态收尾、手动切本地、retry 成功/失败、
  reconciling 掉线回退、status 字段、Run 循环、HTTP 探活含部署前缀与 https 拒绝）、
  `api` 6 条（status 载荷、两种动作分发、retry 失败 200、非法动作 400、未配置 404、
  读库失败 500）；`controlsync` 另跑 `-race` 绿。
- 变异 4 处：去掉模式持久化（重启读回用例红）、retry 不看探活结果（失败用例红）、
  去掉 nil 守卫与条件注册（panic 红）、`Observe` 忘写回 `m.mode`（开发期即被 Run/回退
  用例打红，属真实缺陷而非变异）。
- **未接通**：`control_sync` 配置段（`enabled` / `primary_url` / `ca_file` / `token`）与
  `server/app` 装配尚未落地 —— 因此**转发器还不会按模式停转**（在线判定要等装配时把
  `ModeController.Mode()` 接进 `ControlProxy`），`OnModeChange` 也还没接到系统事件通道。
  这两项与 T10/T11 的接线一起在 P5 收尾前后完成；本轮未改配置、未部署、未重启任何节点。

---

## P5 恢复合并与冲突

### T13 推送与幂等

- 目标：按 spec §6.1 实现 `POST /push`。
- 文件：`merge.go`、`merge_test.go`、`http_control_sync_push_test.go`。
- 用例：base_hash 命中 → 应用并分配新 rev；重复 change_id → 只应用一次；
  同 hash → 收敛；三方不同 → conflict 且记录未变；删除幂等。
- 变异：冲突时仍然写 → 覆盖用例红；不查 `applied_changes` → 重放用例红。
- 验收：`go test ./server/internal/controlsync/ -run Push -count=1`。

执行记录（2026-11-21）：

- `Pusher.Push` 逐条 `pushOne`，判定顺序按 spec §6.1 固定为 **幂等 → `base_hash` 命中
  → 收敛 → 冲突**。**先查 `applied_changes` 再比 hash** 是这一节最容易写错的地方：主节点在
  两次推送之间又改过同一条记录时，只比 hash 会把一条早已应用过的变更误判成冲突，边缘就会
  弹一张没有冲突的冲突卡。这条专门补了 `TestPushReplayAfterPrimaryEditStaysApplied`。
- 应用路径 `ApplyPushedChange` 一个事务四步：分配 rev（`MAX(rev)+1`）→ upsert `records`
  → append `changes` → 记 `applied_changes`。少写 `changes` 其他节点永远看不到这次变更；
  少写 `applied_changes` 边缘重连后重复推进 rev。
- 写权威存储走 `applyToDomain`，有 `Capturer` 时包在 `Suppress` 里 —— 否则这次远端应用
  会被采集器当成一条新的本地改动回声进变更流。
- 删除单独一支：目标不存在 → 幂等成功且**不造墓碑**（凭空造墓碑会让其他节点以为这条记录
  曾经存在）；已删 → `already deleted`；命中 `base_hash` → 落墓碑并**保留最后一次内容
  hash**；否则冲突。空 payload 的删除不进正常 hash 比对。
- 冲突落 `conflicts` 表（id 取推送方 `change_id`，`ON CONFLICT DO NOTHING` 保证重放不
  多出行）；**权威存储与 `changes` 一字不动** —— 冲突不是「先写再回滚」，用户还没裁决。
- HTTP 侧 `POST /api/control-sync/push`（8 MiB 上限、坏体 400、Pusher 失败 500），
  与 status/mode 同样只在字段非空时注册路由。
- 用例：`controlsync` 8 条（base_hash 命中并分配新 rev、`change_id` 幂等不推 rev、
  两侧都改报冲突且记录不变、同 hash 收敛且记 `applied_changes`、删除三种幂等分支、
  重放不覆盖主节点后续改动、混合批逐条结论、未知域 rejected）、
  `api` 4 条（载荷逐字透传、坏体 400、Pusher 失败 500、未配置 404）。
- 变异 4 处：① 冲突后仍写权威存储 → 冲突用例红；② 不查 `applied_changes` → 重放用例红
  （第一版变异被收敛分支掩盖，这正是补 `TestPushReplayAfterPrimaryEditStaysApplied` 的原因）；
  ③ 路由条件与 handler nil 守卫**同时**去掉 → 未配置用例 panic 红；单去任一层仍绿（实测两次），
  两层是有意的冗余，只有同时缺失才违反契约；④ `pushDelete` 给不存在的记录造墓碑 →
  第一版**没红**：用例只断言状态是 applied，没查副本行；补上「不存在的记录不能留下副本行」
  断言后转红，还原复绿。
- **未接通**：`Pusher` 尚未接进 `server/app`；边缘侧从 `pending` 生成 `PushRequest`、
  推送后按结论清 pending、以及失败重试节奏属于 T14；`control_sync` 配置段与 token 校验
  仍未落地（HTTP 层目前没有认证，随装配一并补）。未改配置、未部署、未重启任何节点。

### T14 边缘合并与冲突落库

- 目标：拉取时按 `pending` 三方比对，冲突两边都保留。
- 文件：`reconcile.go`、`reconcile_test.go`。
- 用例：spec §6.2 的四种分支各一条；批量按页推进；中途 kill 后从 `base_rev` 续传；
  冲突期间 `pending` 不清空。
- 变异：冲突时清 pending → 数据丢失用例红；拉取先推进水位再应用 → 断点用例红。
- 验收：`go test ./server/internal/controlsync/ -run Reconcile -count=1`、`-race`。

执行记录（2026-11-21）：

- `Reconciler.Reconcile` 循环 `base := BaseRev()` → `FetchPage(since=base+1)` → 判页 →
  整页提交；`*Replicator` 实现 `PageSource`（同一套 https 客户端与分页大小），测试用
  内存分页源。
- 判定（spec §6.2 + 两条边界）：无 pending → `apply`；`pending.Deleted && remote.Deleted`
  → `converged`；载荷 hash 相同 → `converged`；`remote.Hash == pending.BaseHash` →
  `kept`（主节点离线期间没动过这条，本地改动仍是相对祖先的唯一改动，等推送）；
  否则 `conflict`。`rev <= base_rev` 的旧 rev 一律 `skipped`。
- **顺序**：先 `Apply` 权威存储，再提交 `records` + 清 pending + 冲突 + 水位。反过来
  一旦在两步之间死掉，会留下「水位已过、JSON 还是旧值」且那条变更再也不会被拉到 ——
  `TestReconcileKeepsWatermarkWhenApplyFails` 钉住这条。
- 冲突 id 用 `pull:<domain>:<key>`（`PullConflictID`），重复拉取只刷新「主节点现在
  是什么」那一侧；按 rev 生成 id 会让一次断网重连堆出一串指向同一对值的冲突卡。
- pending 表访问器（`store_pending.go`）：同一 `(domain,key)` 离线期间改多次只保留
  最后一份载荷，但 `base_hash` 与 `change_id` 保持第一次的值；删除的 pending 没有载荷，
  三角比对里它的「本地 hash」用祖先值（与墓碑口径一致）。
- 无进展守卫：整页都是旧 rev 时停下。第一版没有这条，`ignoreSince` 的测试直接把
  `Reconcile` 卡成死循环（122s 被超时杀掉）—— 这是写测试时暴露的真缺陷，不是构造出来的。
- 用例 9 条：四分支各一条、删除三分支、分页 `since` 序列 `1,3,5,6`、断线续传（第 3 页
  失败后从 `since=3` 补第 3 条）、应用失败不推进水位、旧 rev 跳过且不死循环、
  删除冲突带祖先 hash。
- 变异 4 处：① 冲突时也清 pending → 冲突与删除两条用例红（数据丢失）；② 应用失败
  仍继续并提交（等价于「先推进水位再应用」）→ 断点用例红；③ 去掉「远端 == 祖先」
  分支 → `kept` 与删除「远端还是祖先」两条红；④ 去掉无进展守卫 → 用例不结束
  （25s 超时被杀）。各自还原后复绿；`-race` 全绿。
- **未接通**：`Reconciler` 尚未接进 `server/app`；离线写入落到 `pending` 的那一段
  （采集器按模式分流）还没做，本轮的 pending 由测试直接写入。未改配置、未部署、
  未重启任何节点。

### T15 冲突查询与裁决

- 目标：`GET /conflicts`、`POST /conflicts/{id}/resolve`；裁决后写回并追平。
- 文件：`conflicts.go`、`conflicts_test.go`、`http_control_sync_conflicts_test.go`。
- 用例：`local`/`primary`/`payload` 三种选择；重复裁决 409；并发裁决只有一个生效；
  裁决后 `pending` 清空、水位推进；`users` 域响应不含 password hash。
- 变异：裁决写错方向 → 两端一致性用例红；响应带 hash → 安全用例红。
- 验收：`go test ./server/internal/controlsync/ -run Conflict -count=1`。

**执行记录（2026-11-21，已提交 `936ade82`，未部署、未重启任何节点）**

落地：`controlsync/conflicts.go`（`Resolver.List/Resolve`、`ConflictView`、
`ErrConflictNotFound/Resolved/Choice`）、`store_merge.go`（`Conflict` 增
`LocalDeleted/RemoteDeleted/CreatedAt`，新增 `LoadConflict` / `ListOpenConflicts` /
`ClaimConflict` / `ReleaseConflict`）、`store_pending.go`（`RebasePending`）、
`store.go`（`conflicts` 增两列 + `ensureConflictDeleteColumns` 幂等迁移，旧库
`ALTER TABLE ADD COLUMN` 补齐）、`api/http_control_sync.go`（`ControlSyncConflicts`
端口 + 两个 handler，404/409/400/500 映射）、`api/http_routes.go`（两条路由，
nil 时不注册）、`api/http.go`（字段）。

三种选择的后续状态：`local` → 保留 pending 并把 `base_hash` 重定到 `remote_hash`；
`primary` → 落远端值、清 pending；`payload` → 规范化用户合并值、pending 换成它并
同样重定祖先。裁决顺序是「先 CAS 占坑（`resolved_at` 由空改时间，0 行即已裁决）→
再写权威存储 → 最后改 pending」，副作用失败时退坑让用户重试。

用例 9 条（`conflicts_test.go`）：三种选择各 1 条（含「选 local 后真的推得上去、
主节点零新增冲突」的两端一致性）、本地删除落墓碑、非法请求矩阵（未知 id / 非法
选择 / payload 缺失 / null / 非 json）+ 重复裁决 409、并发裁决唯一赢家、
reconcile 冲突 → 裁决 → 推送落地全链路、旧库补列迁移；HTTP 4 条
（列表脱敏、裁决透传 id/choice/payload、四种状态码、未配置 404）。

变异 5 处各自打红：① 裁决方向对调 → 一致性/主节点/payload/删除/并发 5 条红；
② 去掉 `users` 脱敏 → 安全用例红；③ `ClaimConflict` 去掉 `AND resolved_at=''`
→ 并发用例红；④ `RebasePending` 不写 `base_hash` → 三条选择用例红；
⑤ 迁移不补列 → 旧库用例 `no such column` 红。

说明两处：① 「水位推进」在 T14 已经落定（冲突也把 `base_rev` 推过该 rev），
本轮不再重复断言；② 选择 `payload` 不接受表达删除（删除走 `local`/`primary`
两侧），这是刻意的 —— 让「用户合并值」只有一个形状。

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

### P3/P4/P5 装配：配置段 + server/app 接线（2026-11-22）

用户裁定「P3/P4 接线（推荐）」后完成。**默认关闭、未部署、未重启任何节点。**

落地：

- `cli/cmd` 新增 `control_sync` 段：`enabled` / `role`（primary 默认 | edge）/ `instance_id` /
  `primary_url` / `ca_file` / `token_file`。**token 只走 `token_file`**，没有内联字段、没有 flag
  —— 同步通道搬的是含口令 hash 的整份控制面。关闭时不读文件；打开而读不到 / 读到空文件
  一律拒绝启动（`cli/cmd/control_sync.go`）。
- `server/app/control_sync.go`：`startControlSync` 组装 Store（`<config-dir>/control_sync.db`
  + `Migrate`）、六域 Registry（users / preferences / nodes / workflows / prompts / pins）、
  `Capturer`、`NewRuntime`；缺任何一份权威存储直接启动失败，不做「半份副本」。
  - `nodeinfo.RoleWorker` 上启用同步直接拒绝：边缘要出页面、要转发，worker 不是那个角色。
  - **primary 缺 token 也拒绝启动**：没有 token 的 primary 不注册 `/push /head /changes`，
    边缘永远同步不了 —— 这不是可选配置缺失，是通道根本没通。
  - 角色分工：primary 取 `Pusher`/`Feed`/token；edge 取 `ModeController`（`/status` `/mode`）
    与 `Resolver`（冲突查询/裁决），并构造 `ControlProxy`，`Mode` 指向状态机、
    `BlockedDomain` 查未裁决冲突（查库失败按冻结处理，fail closed）。
- `server/app/server.go`：`Start` 在建完四个共享 store 之后装配同步；`workspaces.SetControlSync`
  把能力装进每个按账户构建的 `HTTPHandler`；`controlSync.runtime.Run(ctx)` 起后台循环
  （primary 只有兜底采集，edge 另有探活/追平/交接/推送）。
- 中间件顺序 **采集在内、转发在外**（`controlSyncWiring.wrapRoot`）：转发出去的控制面请求
  不在本机留下记录；edge 在线时采集器本身也被 `CaptureWhen` 关掉，两道防线互补。
- 默认关闭时零行为变化：不建库、不起 goroutine、端点 404、请求直通。

验证：

- `cli/cmd` 6 条（关闭不碰文件、未配置为 nil、token 去空白、缺文件/空文件报错、无
  token_file 留空交给运行时判定）。
- `server/app` 9 条（关闭是 no-op 且不建库、worker 拒绝、primary 缺 token 拒绝、
  primary 暴露 push/feed/token、edge 接上 mode/resolver/proxy 且不暴露主节点端点、
  转发范围白名单、edge 坏配置三种、六域齐全与缺存储失败、按域冻结只挡冲突域且查库失败
  fail closed、wrapRoot 采集本机写进变更流且数据面不推进水位、wrapRoot 在线转发控制面 /
  数据面留本机 / 离线写落本机）。
- 变异 11 处各自打红后还原：去掉 `BlockedDomain` 接线、去掉 primary token 闸门、去掉
  worker 闸门、`applyTo` 不写 token、去掉 `enabled` 判定、去掉缺存储闸门、查库失败改成
  fail open、CLI 不读 token、`boolValue` 恒假、`wrapRoot` 去掉采集、去掉转发包装。
- 门禁：`go test -count=1 ./cli/... ./server/app/... ./server/internal/api/...
  ./server/internal/controlsync/...` 全绿（api 60.1 s）、`go vet` 与 `gofmt -l` 干净、
  `go build ./...` 成功。

未做 / 未覆盖：

- 真双进程端到端仍是 T21（两条独立 `XDG_CONFIG_HOME` + 端口 7441/7442）；本轮只用
  `httptest` 的自签 CA 验证了转发路径，没有跑真实二进制。
- `OnModeChange` 仍未接系统事件 / 前端横幅（T16）。
- `server/cmd/mindfs-server` 这份备用入口不解析 `control_sync`（`make build` 装的是
  `cli/cmd`，与项目约定一致）。
- 未改任何节点配置、未部署、未重启。
