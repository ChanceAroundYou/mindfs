# 边控端（Edge Controller）：握手 · 安装包 · 自更新

> Scope IDs: G-QH / G-QK / G-QJ（原取 G-PZ / G-QA / G-QB；2026-11-23 合回 main 时
> 主干已把这三个号分配给配额挂起与飞书入站两组，按「主干已发布优先」由分支侧改号，
> 见 `docs/upstream-customizations.yaml` 的撞号说明）

Plan 2026-11-23。起点是用户三条需求：

> 1. 我希望主控端的同步默认打开 并且可以接受边缘控制端的握手 主动发现和同步
> 2. 边缘机器目前是设定为 windows gui, 能不能出一个独立的安装包？在本机编译，挂到 github 上，配置自动更新 参考安卓客户端
> 3. 我们叫他边控端 edgecontroller

七项裁定（两轮 AskUserQuestion，均已拍板）：

| # | 决策 |
|---|---|
| D1 | 配对凭据 = **共享 token + 每边控端自注册**（单份共享 token；边控端自注册进 allowlist；可逐个踢人） |
| D2 | 发现范围 = **手动填一次 URL，之后自动**（不做 mDNS、不做全局 registry） |
| D3 | 安装包形态 = **自更新 exe，安装器就是托盘壳** |
| D4 | 更新策略 = **静默自动更新** |
| D5 | 批准范围 = **一次做完全部四波** |
| D6 | 托盘实现 = **`x/sys/windows` 手写**（CGO_ENABLED=0） |
| D7 | 本机 config = **Agent 写 config、用户执行 `sudo systemctl restart mindfs`** |

## 模型

两条轴互相独立，别混：

- **能力轴** `control | worker`（`server/internal/nodeinfo`）：这台机器能不能出页面、持不持有控制面。
- **同步轴** `primary | edge`（`controlsync`）：已配好的机器跟随哪个主控端。

「边控端」= `control` + `edge`：自己出页面、自己跑本机 agent、控制面有一份副本，
在线时跟随主控端。它与 `worker` 的区别是能出页面；与主控端的区别是控制面权威在别人手上。

## 波次

| 波 | 内容 | Scope | 状态 |
|---|---|---|---|
| W0 | 取证：本机/四端现状、重启语义、托盘与自更新是否已具备 | — | 完成 |
| W1 | 主控同步默认开 + token 自生成 + 单主控守卫 + `/edges` 名单（注册/列表/踢人/恢复） | G-QH | 完成（`fd48fa1f`、`11498130`） |
| W2 | 边控端握手与自动同步：`POST /api/control-sync/pair` + 未配对档位 + 落盘 | G-QK | 完成 |
| W3 | Windows 独立安装包（自解包 exe + 托盘菜单 + 计划任务）+ 静默自动更新 | G-QJ | 代码完成（`59ad9f32`），真机未验 |
| W4 | 部署与四端对账（需当次授权） | — | 待做 |

### W1（已完成）

- `control_sync` 段不写 = **默认启用**（只对 `control` 节点成立；worker 整段跳过）。
- 主节点没有 token 时在 `token_file` 上**生成一份**（0600、幂等）；生成失败才拒绝启动。
- `guardSinglePrimary`：一台机器只能有一个主控，曾是别人副本的拒绝无提示翻脸；
  出路是显式的 `adopt_primary`。
- `/api/control-sync/edges`：调用方被 `requireSyncToken` 顺手登记（`instance_id` 打标），
  可列出、踢掉（`revoked_at`）、恢复。**这张表是运维索引，不是准入白名单** —— 共享 token
  的语义就是「谁拿到都能进」，踢人只让那台机器的同步立刻 403 并停在本地模式。

### W2（已完成，Scope: G-QK）

三处缺口与处置：

1. **全仓没有 `config.json` 写回函数**。新增 `config.UpdateJSONFile(path, mutate)`：
   原子换名、`0600`、`UseNumber` 防大整数漂移、未知键原样保留、mutate 失败不落盘。
2. **`Config.Normalize()` 让未配对的边缘根本起不来**（cli 侧直接 `return nil, err`）。
   新增 `ControlSyncOptions.PendingPairing`：服务照常起、**只挂配对端点**，不建库、
   不采集、不转发。没有这一档，用户拿到的是一台起不来的服务 —— 连填表单的页面都没有。
3. **没有重启端点，且 `os.Exit(0)` 在两种部署里都不成立**（本机 systemd `Type=simple`
   + `Restart=on-failure`；pc-win 计划任务 `RestartOnFailure` 只认非零退出 + S4U）。
   W2 **不做自重启**：配对返回 `restart_required: true`，重启交给 W3 的托盘菜单与用户。

配对端点的安全边界：**只接受本机回环**（按 `RemoteAddr`，不信 `X-Forwarded-For`）。
配对会改信任锚、写通道密钥、改 `config.json`，三件事凑起来等于在 `0.0.0.0` 上开一个
无鉴权的「重定向整份控制面」入口 —— 而它此时还没有 token 可校验。

配对写入时把 `control_sync.role` 定为 `edge`，显式写着 `role: worker` 的会一并纠正成
`control`：否则配好的是个不注入静态目录的 worker，打开页面是 403。

#### 实施记录（2026-11-23）

已完成并跑通隔离双实例 e2e。落地形态与计划的三处差异：

1. **配对端点不随配对消失**。计划里隐含「配好就没这个入口了」，实际保留：换主控端是
   同一件写配置＋换密钥的事，删掉入口只会让「换主控」变成一次手工改文件事故
   （`server/app/control_sync.go` 的 else 分支显式留着 `w.pairing`）。
   **代价是探针语义必须分开**：`PairingState().pending_pairing` 由构造点决定，
   未配对接线恒真、已配对边缘恒假。
2. **这条 e2e 抓出一个真缺陷**：原先 `controlSyncPairer.PairingState()` 硬编码
   `PendingPairing: true`，而前端 `fetchControlSyncPairing` 只把 `pending_pairing===true`
   当「等配对」，于是**一台正在 online 同步的边控端页面上永远显示配对表单**。
   修法见 `newControlSyncPairer(opts, pending bool)` + `TestPairedEdgePairerDoesNotReportPending`
   （把 `pending: pending` 改回 `pending: true` 即红）。
3. **主节点 token 自生成**（W1 落的）让 e2e 不必预置共享 token：读主节点 `token_file`
   拿到的就是那把 43 字符的 `base64.RawURLEncoding` 串。

e2e 五场景全 PASS（`e2e/test_control_sync_pairing.py`，端口 7470/7471，真实进程 + 自签 CA）：

| # | 场景 | 钉住什么 |
|---|---|---|
| 0 | 未配对边缘起得来 | 服务在、`GET /pair` 200 且 `pending_pairing=true`、回显实例名、不泄露 token 与路径；`GET /status` **404** |
| 1 | `POST /pair` | 200 + `status=paired` + `restart_required=true`；`token_file` 内容 == 提交值且 0600；`config.json` 的 `control_sync` 段写齐、`addr` 未被动、`role != worker`；**重启前 `/status` 仍 404**（落盘 ≠ 生效） |
| 2 | 重启后 | `GET /pair` 仍 200 但 `pending_pairing=false`；`GET /status` 200；模式稳定到 `online` |
| 3 | 主节点名单 | `/edges` 出现该实例、无 `revoked_at`、`last_seen_at` 非空 |
| 4 | 签名踢人 | 200 带 `revoked_at`；边缘 `wait_mode_stable("offline-local", 90s)`；踢后 22 秒 `last_seen_at` **逐字未推进**（基线在踢人**之后**取，否则踢前那一拍会误报） |

### W3（代码完成，Scope: G-QJ）

#### 取证结论（2026-11-23，只读）

| # | 事实 | 证据 |
|---|---|---|
| E1 | **静默自动更新在代码里完全不存在**。`update.Service.Start` 只做 `CheckNow`（标 `Status="available"`），装更新的唯一入口是 `TriggerUpdate`，只由 `POST /api/app/update` 与 CLI `-update` 调用；全仓 grep 不到 `auto_install` | `server/internal/update/release_fetch.go:42,136`、`server/internal/api/http.go:245`、`cli/cmd/mindfs.go:79` |
| E2 | `canAutoUpdate()` 四道闸，其中发布公钥靠 `-X mindfs/server/internal/update.releaseManifestPublicKey=` 注入 | `server/internal/update/install.go:163`、`scripts/build-all.sh` 读 `MINDFS_RELEASE_PUBLIC_KEY` |
| E3 | **Unix 静默自重启会把服务停死**：本机 systemd 是 `Type=simple` + `Restart=on-failure`，而替换路径是 `restartInstalledBinary` → `startReplacementProcess` + `os.Exit(0)`，且 `Setsid` 让替换进程脱离 cgroup | `/etc/systemd/system/mindfs.service`、`server/internal/update/service_unix.go`、`install.go` 的 `restartInstalledBinary` |
| E4 | Windows 侧 `handleUpdateCommand` 在服务运行时**硬拒绝**（要求先 `-stop`）；静默更新必须走进程内 `TriggerUpdate`，不能走 CLI | `cli/cmd/service_control.go:98-125` |
| E5 | 「机器是否忙」有现成信号：`ListReplyingSessions()` 返回 `Active \|\| AwaitingAnswer` 的会话 | `server/internal/api/stream_hub.go:174` |
| E6 | 真机计划任务形状已取到：S4U + `BootTrigger` + `LogonTrigger` + `--internal-autostart --addr 0.0.0.0:7332`；而 `scripts/install.ps1` **不注册计划任务** | `schtasks /query /xml` 实测（pc-win）、`scripts/install.ps1`（243 行） |
| E7 | 托盘当前只有「退出」（`menuQuitID = 101`），唯一调用点 `cli/cmd/mindfs.go:485-492` | `cli/cmd/tray.go`、`tray_windows.go` |
| E8 | 尺寸：`mindfs` 二进制 31 MB、`web/dist` 13 MB ⇒ 自解包 exe 约 45 MB；`RELEASE_ARTIFACTS` 用 `mindfs_$(TAG)_*.zip/*.tar.gz` 通配 | 实测、`Makefile` |

#### 待做清单

1. **`update.auto_install`（代码默认 `false`）** —— `Start()` 的 ticker 里，`CheckNow` 之后若
   `hasUpdate && autoInstall && 空闲`，调 `TriggerUpdate`。
   **默认必须 `false` 且只能由 Windows 安装器写 `true`**：按 E3，Unix 侧静默重启会停死服务；
   且本机/worker 被静默重启会杀掉正在跑的 claude 子进程。
   **已完成**：判据抽成纯函数 `autoInstallDecision`（`server/internal/update/auto_install.go`），
   六条分支 —— 未开 / 安装布局不支持 / 无新版本 / 已有轮次在跑 / 有会话在回复 / 放行。
   配置链 `config.json` 的 `update.auto_install` → `cli/cmd` → `app.StartOptions.UpdateAutoInstall`
   → `updateSvc.SetAutoInstall`；读取侧另有 `TestUpdateAutoInstallDefaultsOff`（4 条：没写 / 只写段 /
   显式关 / 显式开）与 `TestServerOptionsCarryUpdateAutoInstall`（源码契约，装配漏接即红）。
   触发失败只打日志不 panic —— 下一拍 ticker 会重试，这里没必要让整个进程带病。
2. **空闲闸门** —— `update.Service` 加 busy 探针（由 `server/app` 注入）；有会话在回复就只标
   `available`，下一拍再试。
   **落地时偏离本条一处**：探针用新加的 `Pool.HasAnyActiveUse()`（遍历共享 Pool 全部会话的
   `activeUses > 0`），不按账户枚举 `ListReplyingSessions()` —— 共享 Pool 是全局一份，
   `activeUses > 0` 正是 repoint 掐进程的同一个判据；按账户枚举会漏掉节点会话与委派会话。
3. **公钥注入** —— `deploy-pcwin.sh` 与日常交叉编译补 `MINDFS_RELEASE_PUBLIC_KEY`，否则
   `auto_update_supported=false`，静默更新永远不可用（E2）。
   **已由 `be463453`（Scope: G-QH）落地**：`scripts/deploy-pcwin.sh:26-28` 与
   `scripts/build-all.sh:38-39` 都注入了 `-X mindfs/server/internal/update.releaseManifestPublicKey=`，
   `internal/deploy/release_key_test.go::TestDeployScriptsInjectReleaseManifestKey` 钉住。
   该条**不在 W3 新增范围内**，只是 W3 的前置条件已满足。
4. **`scripts/build-windows-setup.sh`** —— 产出自解包 exe（内嵌 `mindfs.exe` + `web/` + `agents.json`
   + `config.json` 模板 + 纯 ASCII `setup.ps1`）：解包到 `%LOCALAPPDATA%\Programs\mindfs`、
   按 E6 的 XML 形状用 `schtasks /create /xml` 注册 `\mindfs`、注册用户 PATH。
   安装器写 `control_sync.role = "edge"`（配 W2 的 `PendingPairing` 口径）与 `update.auto_install: true`。
   **已完成（`59ad9f32`）**：形态是 `[Go 桩][载荷 zip][8 字节大端长度 + 4 字节魔数 "MFSS"]`
   （PE 加载器对尾部多余字节透明）。拆三块 —— `internal/deploy`（跨平台纯逻辑：`SetupPathsFor` /
   `ApplySetupConfig` / `BuildSetupTaskXML` / `EncodeUTF16LEFile` / `MergeUserPath` /
   `BuildSetupPayload` / `VerifySetupBundle` / `AppendSetupPayload` / `ReadSetupPayload` /
   `ExtractSetupPayloadToDir`，外加 `archive.go` 的 zip 安全读写）、
   `scripts/windows-setup/main.go`（Linux 侧拼装器）、`server/cmd/mindfs-setup-stub/main.go`
   （windows-only 桩：解包 → 写 config → 求 SID → 生成 UTF-16LE 任务 XML → 跑 `setup.ps1`）。
   桩程序不在 `scripts/` 是硬约束：Go 的 internal 规则不允许 `scripts/` 树导入
   `mindfs/server/internal/config`，而它必须写 `config.json`。
   `setup.ps1` 全部路径从参数接收（`TestWindowsSetupScriptTakesPathsFromStub` 禁它自己拼
   `%LOCALAPPDATA%\Programs`），五步顺序 stop → install → task → PATH → start，
   挑战 XML 绝不二次编码，`.ps1` 纯 ASCII。生成器在**编译前**核对包完整性
   （`mindfs.exe` / `agents.json` / `web/index.html` / `web/favicon.svg`），缺一件直接
   `exit 1` —— 装出一个「二进制在、页面 404」的包比没有包更糟。
   26 处变异全部 RED（M16「web 锚点缺失没有失败」与 M20「先编译再校验」首轮不红，
   经查是**测试自身的缺陷**并已修好，不是被测代码没问题）。真实端到端：假包 →
   `build-windows-setup.sh` → 2,851,807 B 的 setup exe → 解包得 6 个文件齐全。
5. **托盘菜单扩项** —— 现在只有「退出」。至少「打开页面」「重启以应用配对」（W2 把重启交给这里）、
   「退出」（E7）。
   **已完成**：`trayMenuItems()` 三项（`menuOpenPageID=102` / `menuRestartID=103` /
   `menuQuitID=101`，退出 ID 不动）。「重启以应用配对」走 `trayRestartScript`
   （纯函数，Linux 上可测）：后台 powershell 轮询本 PID 消失 → `schtasks /run /tn mindfs`
   → 失败用原命令行兜底拉起；本进程随后走与「退出」同一条优雅收尾。
   不用 `schtasks /end`（`TerminateProcess` 会砍掉写盘中的收尾）、不自重启（E3）。
   等待循环是必需项：真机任务 `MultipleInstancesPolicy=IgnoreNew`，早一步 `/run` 被静默吞掉。
   变异四处各自打红（菜单退回一项 / 删等待循环 / `/run`→`/end` / 引号改双引号）。
6. **发版链** —— `Makefile` / `RELEASE_ARTIFACTS` 纳入 setup exe；`make release` 一并上传。
   **已完成（`59ad9f32`）**：三处缺一不可且都不报错，只是 GitHub release 里少了文件 ——
   `Makefile:151` 的 `RELEASE_WINDOWS_SETUP ?= 1` 与 `RELEASE_ARTIFACTS` 追加、
   `build-all` 目标里调 `scripts/build-windows-setup.sh`、`scripts/sign-release-manifest.go`
   的 `artifactPaths` 枚举 `mindfs_%s_*setup.exe`（漏了它 = 客户端拿到一个不在签名清单里的
   安装包，静默自动更新永远用不上安装器）。`TestReleaseChainShipsTheWindowsInstaller` 三条一起钉。

代码签名按计划**不做**。

## 验收

- 单测：`config.UpdateJSONFile` 7 条（未知键/大整数/0600/沿用 mode/坏 JSON/顶层数组/mutate 失败不落盘）、
  `controlsync.Pair` 5 条（401、worker 主控、schema、明文 http 四种失败都不留痕；prefix 保留）、
  配对端点 7 条（404/非回环 403/回环放行/坏体 400/502/其余 400/转发头不算本机）、
  cli 3 条（缺 token 与空 token 都进「等配对」、显式 `-config` 优先）、装配 4 条。
- 安装器单测：`internal/deploy` 4 个文件 26 条 —— `setup_test.go` 11 条（载荷往返、坏文件、
  安装布局判据、两个根都必需、任务 XML 真机形状/必填/转义、UTF-16LE、PATH 合并、
  `ApplySetupConfig` 两个分支）、`setup_extract_test.go` 5 条（载荷端到端往返、缺 `setup.ps1`
  要点名、普通二进制必须拒、超大长度用**正数**越界、区间恰好是载荷）、
  `setup_script_test.go` 11 条（纯 ASCII、路径全由桩传入、五步顺序、半装必须失败、
  XML 不二次编码、PATH 安全合并、先停再拷、生成器结构、桩程序位置与依赖、发版链三处）、
  `archive_test.go` 4 条（zip 解包写内容、拒符号链接条目、目标逃逸、绝对名）。
  变异 **26 处全部 RED**，其中 M16 / M20 首轮不红是测试自身的缺陷（扫到文件尾、
  变异没真搬块），已修好测试后复验转红 —— 这两条正是「测试通过 ≠ 守得住」的例子。
- 变异：去掉回环守卫、去掉 502 映射、edge 缺 token 仍拒启、未配对也起同步、配对不写 `role`
  五处各自打红后还原。
- e2e：隔离双实例（两个 `XDG_CONFIG_HOME`，端口 7470/7471，不碰 7331）跑「首次配对 →
  重启 → 追平 → 踢人后停在本地」。
- 门禁：`go test ./...`、`tsc --noEmit`、Vitest 全绿、`make check-upstream` 绿。
  本段实测：`gofmt -l` 干净（另有两条 upstream 既有文件，与本组无关）、`go vet ./...` 退出 0、
  `go vet`（`GOOS=windows` 桩程序）退出 0、`go build ./...` 退出 0、
  `go test ./internal/deploy/` ok、`go run ./scripts/check-upstream` →
  `2626 个 delta 文件被 380 组完整覆盖`。

## 边界

- 不做自重启（理由见上）；不做 mDNS / 全局 registry；不做代码签名（默认）。
- token 仍 **只从 `token_file`** 读，不进 `config.json`、不进命令行、不进日志。
- 同步通道一律 https，可选 `ca_file`，**没有** insecure-skip-verify 开关。
- 本机 `sudo systemctl restart mindfs` 仅用户执行。
