# 部署 / 发布历史（从 plans 索引移出）

> 原文搬移，未改写事实。plans 索引不再自动注入这段历史；按需读取本文件即可。

## 部署状态（权威，覆盖下方条目里的「未部署」）

**2026-11-24 四端部署 `v0.5.5-1400-25-g74ceff29`**（`origin/main` `f1304dbe..74ceff29`
共 13 个提交，本批内容：G-QR `8f2ebd9c` 折叠 `target_tokens` 并入 `keep_tail_tokens`；
G-QS `e6558be8`/`ccf050d0` 超长会话阈值改「距上次压缩累计活跃 1 小时 + 窗口 70% + 连续
3 个过线回合」并恢复胶囊活跃时间行；G-QT `851c455b`/`a9404d50`/`74ceff29` 上下文溢出压缩
成功后自动重发原消息；G-QJ 三份文档补记）。`bash scripts/deploy-all.sh` 退出 0，门禁：
`go test ./...` 全绿、`tsc --noEmit` clean、Vitest 274 文件 / 968 通过 / 6 跳过、
`make check-upstream` 2692 delta / 398 组全绿。四端版本逐字一致：wsl PID 1568191、
fn PID 1066652，均 `user` 单元 `is-active` + `/mindfs/health` 200 `role=worker` +
`--version` 同串；pc-win 计划任务 PID **28128**、`:7332` LISTENING 由 28128 持有、
health 200 `role=worker`、`bin/mindfs.exe --version` 同串；本机 `make install` 已装同一
二进制（`--version` 同串），7331 服务的 bundle `assets/index-C0rskU_s.js` 与安装目录一致，
**运行进程仍是旧版 `v0.5.5-1300-66-g6d1b174e`（PID 1179794，`/proc/<pid>/exe` 带
`(deleted)`，`/api/node-info` 报旧版本、`/api/app/update` 报 `has_update=true`），
待用户 `sudo systemctl restart mindfs`。**

**2026-11-24 真实发布 `v0.5.5-1500`（GitHub release）**：tag `v0.5.5-1500` →
`d82179c3`，地址 https://github.com/ChanceAroundYou/mindfs/releases/tag/v0.5.5-1500，
公开、非预发布，11 个产物（7 平台二进制 + Windows setup.exe + APK + 签名 manifest +
android-version.json）与本机 `dist/` 的大小与 sha256 逐项一致（APK sha256 `307bdb85…`、
`apksigner` 证书 `CN=MindFS`）。线上 `release-notes.md` 首行已是 `# MindFS v0.5.5-1500`，
`releases/latest/download/android-version.json` 报 `version=v0.5.5-1500` 且下载项指向
`mindfs_v0.5.5-1500_android.apk`。发布说明覆盖 G-QO/G-QP/G-QQ/G-QS/G-QT。**发布不等于
本机运行进程已升级**：本机仍是旧二进制，需用户重启。

**2026-11-24 四端部署 `v0.5.5-1400-12-ge2be3c18`**（`origin/main` `040c9043..e2be3c18` 共 12 个
提交：G-QQ 三条 —— 巡检判活覆盖 Run 与节点会话 `61375412`、显式 Run 的启动对账写 Router 事件
`058be3d6`、会话结算携带绑定 Run 与节点证据 `22b01f25`；G-QO 两条上下文溢出修复
`626a787c`/`e2be3c18`；G-QP 移动端两级弹层避让 `0666db09`；`task-112` 合并与发布记录）。
`bash scripts/deploy-all.sh` 退出 0，门禁：`go test ./...` 全绿、`tsc` clean、Vitest 274 文件 /
967 通过 / 6 跳过、`check-upstream` 绿。四端版本逐字一致：wsl / fn user 单元 `active` + health 200
`role=worker` + `--version` 同串（PID 612031 / 639806）；pc-win 计划任务 PID **10072**、
`:7332` LISTENING 由 10072 持有、进程 `StartTime` 10:14:55 晚于安装、health 200 `role=worker`、
`--version` 同串；本机 `make install` 已装同一二进制且 7331 服务的就是这版 bundle
`assets/index-DsKaVt7P.js`，**运行进程仍是旧版 `v0.5.5-1300-66-g6d1b174e`（PID 1179794，09:56:40
起、exe 已 deleted、md5 `85b40a40…`），需用户再执行一次 `sudo systemctl restart mindfs`** ——
用户 09:56 的那次重启早于本次安装，不算覆盖。
  10:15 独立只读复核：`/mindfs/health` 200 `control`、`/api/app/update` 报
  `current_version=v0.5.5-1300-66-g6d1b174e / latest=0.5.5-1400 / has_update=true`，
  PID 1179794 的 `/proc/<pid>/exe` 仍带 `(deleted)`；wsl PID 612031 / fn PID 639806 报
  `v0.5.5-1400-12-ge2be3c18` 且 health 200 `role=worker`，pc-win PID 10072、`:7332`
  LISTENING、health 200 `role=worker`；7331 服务的 `assets/index-DsKaVt7P.js` sha256
  与安装目录那份逐字一致（`37041f48…`）。

**2026-11-24 真实发布 `v0.5.5-1400`（GitHub release；不等于四端部署）**：tag
`v0.5.5-1400` → `040c904325aa30ad6374a024d9bbaf94c5960a45`（`origin/main` 同点），
地址 https://github.com/ChanceAroundYou/mindfs/releases/tag/v0.5.5-1400，公开、非预发布。
9 个产物全部上传，远端与本机 `dist/` 的大小与 sha256 逐项一致（完整清单与校验和见
[边控端计划](../superpowers/plans/2026-11-23-edge-controller-handshake-and-installer.md)）。
本轮**没有**分发到 wsl / fn / pc-win，也没有重启本机；只有 hasee 边控端经过下述真实验证升到
`v0.5.5-1400`：`update.auto_install` 从旧版 `v0.5.5-1300-66-g6d1b174e` 的
`auto_install.skip reason=unsupported`（缺发布公钥），换成内嵌公钥的降级构建
`v0.5.5-1300-71-g040c9043` 后，日志依次出现 `check.result latest=0.5.5-1400 has_update=true` →
`auto_install.begin` → 签名 manifest 与 zip 内 `mindfs.exe` sha256 校验 → `restart.begin`；
新进程 PID 30448 报 `current=v0.5.5-1400`、`has_update=false`，安装文件 sha256 `F2BEEB27…B102`
等于 release zip 内容，控制面仍 `online / base_rev=62 / pending=0 / conflict=0`。另用 release 的
setup.exe 在 hasee 真跑安装器：`installer_exit=0`、PID 668 Session 0、`/mindfs/health` 200
`role=control`、`/api/node-info version=v0.5.5-1400`，config 中 `primary_url` / `token_file` /
`instance_id` 保留，UI bundle `build_version=v0.5.5-1400`（289 文件），计划任务仍 S4U +
BootTrigger；临时文件与备份已清。**非 Windows 自动安装已在隔离 Linux 实例真跑（2026-11-24
10:38）**：独立 `XDG_CONFIG_HOME` + 端口 7483 + 内嵌发布公钥的旧版 `v0.5.5-1300` 二进制，
`config.json` 显式 `auto_install:true`；日志 `check.result latest=0.5.5-1400 has_update=true` →
`auto_install.begin` → `restart.begin` → 新进程 `current=v0.5.5-1400 has_update=false`，health 200、
`/api/node-info version=v0.5.5-1400`，安装文件 sha256 `69ac9aef…1a0cf` 等于 release
`mindfs_v0.5.5-1400_linux_amd64.tar.gz` 内 `mindfs`，`auto_install:true` 安装后保留；
隔离进程 / 端口 / 临时 worktree 已清，生产 7331 全程只读且运行进程仍是旧版。**补验（2026-11-24）**：
busy 闸门用干净 worktree + overlay 注入测试、本地提供真实签名 release 资产、真实
`agent.Pool.HasAnyActiveUse` 作 probe：有会话时 `(false,busy)` / trigger 0，释放后 `(true,install)` /
trigger 1 且真的安装 22,220,962 B；去掉 probe 的变异让测试红，详见
[边控端计划](../superpowers/plans/2026-11-23-edge-controller-handshake-and-installer.md)。
hasee 经用户授权整机重启：PID 668 → 4084（均 Session 0），新进程 StartTime
`2026-11-24T10:58:41.7356307+08:00` 晚于开机，BootTrigger `True`、`:7332` LISTENING 由 4084 持有、
health 200 `role=control`、version `v0.5.5-1400`、同步 `online / base_rev=92 / pending=0 / conflict=0`，
整机重启自启已验。**GUI 结论分级（2026-11-24）**：SSH 直跑脚本落 Session 0、无交互桌面，
不能双击；但 hasee 确有 Session 1 交互桌面，`LogonType Interactive` 的临时任务可进 Session 1
（`UserInteractive=True`、1707×1067）。Highest 交互任务里
`Shell.Application...InvokeVerb('open')` 打开 release setup.exe 实测通过：安装器 PID 3040
启动后正常退出、无失败弹窗，新 mindfs PID 18008 / Session 0，health 200 `role=control`、
version `v0.5.5-1400`；直接 `Start-Process` 的 Highest 重跑也通过（PID 20080）。
Limited token 的 UIA 启动、默认 Explorer 里的真实 `mouse_event` 双击都仍失败在
`setup.ps1 failed: exit status 1`（先后是 `Stop-Process` Session 0 进程被拒与
`schtasks /create /f` 被拒）。**人工桌面双击未验**，不把 shell verb 启动升级成人工双击；
临时任务与文件已清，正式 `mindfs` 任务仍 `Running`、health 200。详见
[边控端计划](../superpowers/plans/2026-11-23-edge-controller-handshake-and-installer.md)。

**2026-11-23 四端发布 `v0.5.5-1300`**（新 tag `v0.5.5-1300` → `904c682e`，origin/main 同点；
含 `f79a0e5a..904c682e` 共 13 个提交：G-PX 七条 —— 摘要 600 字截断 `b73d8781`、失败回合不写兜底
文案 `6d0bf607`、错误行后跟长正文仍判失败 `d31f3aae`、`turn_settled` 带失败标记 `76ddd367`、
结算回合不重复渲染进度 `104f89e4`、巡检回合放开 `say/ask_user` `593760db`、OOB 回合跨重启恢复
`fdff1e25`；G-PY 块流读取流式化 `baca9439`；G-PU 每个 agent 子进程单独一个 job object
`3e6b7475`；G-PW 两条文档提交）。`bash scripts/deploy-all.sh` 退出 0。四端版本逐字一致：
wsl `user` 单元 `is-active` + health 200 `role=worker` + `--version` `v0.5.5-1300`
（PID 426242，`/proc/<pid>/exe` 与安装文件同 md5）；fn 同上（PID 2986515，同 md5）；
pc-win 计划任务 PID **34228**（`StartTime` 10:02:08 晚于二进制写入 10:01）、`:7332` LISTENING
由 34228 持有、health 200 `role=worker`、`bin/mindfs.exe --version` `v0.5.5-1300`；本机已由用户
**10:29:32 重启完成**（system 单元 `active`、`NRestarts=0`）：PID **505751**、`/proc/<pid>/exe`
与 `~/.local/bin/mindfs` 同 md5 `882a9aa3…` 且不再带 deleted、`/api/node-info` `version=v0.5.5-1300`、
health 200 `role=control`、bundle `assets/index-zj48uj_q.js` 即安装目录那份、
`/api/router/status` 报 `mode=coordinate` + `supervision_mode=on`。
门禁：`go test ./...` 全绿、`tsc --noEmit` clean、Vitest 271 文件 / 943 通过 / 6 跳过、
`make check-upstream` 2590 delta / 377 组全绿（baseline `9ab95722`）。

**2026-11-23 四端发布 `v0.5.5-1201-220-gf79a0e5a`**（origin/main `f79a0e5a`，含
`0b060ede..f79a0e5a` 全部提交，本批只有 G-PW）。四端版本逐字一致：wsl `is-active` + health 200、
fn `user` 单元 `is-active` + health 200（`/proc/<pid>/exe` 与安装文件 md5 同为 `f4ad9194…`）、
pc-win 部署后经历一次 Windows 更新重启（02:15:10 TrustedInstaller 发起的计划内重启），**BootTrigger
在无快照状态下用新二进制把服务拉起来**（02:15:55 任务事件 118 → 进程 4504、Session 0），随后
02:22:03 有人以交互账户终止任务、02:22:24 重启（事件 330/110 → 100），当前 PID 7072、`:7332`
LISTENING、health 200 `role=worker`、日志 `checker.start current=v0.5.5-1201-220-gf79a0e5a`，
HKCU Run 键与快照仍为 `ABSENT`；本机已安装同一二进制（`--version` `v0.5.5-1201-220-gf79a0e5a`，
`/mindfs/health` 200 `role=control`，bundle `assets/index-BOTUDlWQ.js` 与安装目录一致），
**运行进程仍是旧二进制（`v0.5.5-1201-149-g881a94bd`，22:56:52 起），待用户
`sudo systemctl restart mindfs`**。本批内容只有 G-PW：`prepareAutoStartEnvironment()` 把
「快照文件不存在」从错误降级成 no-op（只放行 `os.ErrNotExist`，坏 JSON / 版本不符仍报错），
消除 pc-win 每次启动必失败的那条环境恢复路径；隔离实例实测 `--internal-autostart` 在无快照的
空 config 目录里起服务、health 200 且 stderr 无 `[mindfs/autostart]` 警告。门禁：
`go test -count=1 ./...` 全绿、`tsc` clean、Vitest 943 通过、`make check-upstream`
2577 delta / 375 组全绿。

**2026-11-22 四端发布 `v0.5.5-1201-156-g9f62b54b`**（origin/main `9f62b54b`，含
`28266b98..9f62b54b` 全部提交）。四端版本逐字一致：wsl / fn 的 user 单元 `is-active` + health 200
（`role=worker`）；pc-win 计划任务 PID 8128、`:7332` LISTENING、health 200（`role=worker`）；
本机已安装同一二进制（`/mindfs/health` 200 `role=control`，bundle `assets/index-C_qTgGGB.js` 已由
7331 服务），**运行进程仍是旧二进制，待用户 `sudo systemctl restart mindfs`**。本批上线的组：
G-PU（Agent 子进程随 MindFS 生命周期退出：Windows 共享 Job Object + kill-on-close、Unix pidfd
显式收尾 + `KillMode=control-group`；pc-win 已实测 `schtasks /end` 后 codex 子进程同时消失、
重启后 PID 可查、health 200）、G-PP（Claude 默认 200k，只有 `[1m]` 才给 1M）、G-PK（Codex 跨
provider 续接丢弃旧 provider 加密 reasoning）、G-PG/`881a94bd`（压缩后写回适配器上下文窗口）、
G-PT（任务开始/终止动作与「待处理」文案）。门禁：`go test -count=1 ./...` 全绿、`tsc` clean、
Vitest 891 通过、`make check-upstream` 2465 delta / 373 组全绿。

**2026-11-22 四端发布 `v0.5.5-1201-57-g0cc70c6e`**（origin/main `0cc70c6e`，含 `6159f483..0cc70c6e`
全部提交）。四端版本逐字一致：本机 system 单元由用户重启，运行进程与安装文件同 md5
`540a0e8f…`、`/mindfs/health` 200 `role=control`；wsl / fn 的 user 单元 `is-active` + health 200
（`role=worker`）；pc-win 计划任务 PID 在跑、`:7332` LISTENING、health 200。本批上线的组：G-PE
（W2/W3 收口 + 固化 stale 守卫 `0cc70c6e` + 两份隔离验收脚本）、G-PG（压缩按钮跟随服务端
`compact_pending`、压缩后上下文与胶囊累计口径）、G-MS（直连回复也补结算压缩总结）、G-BC（详情
菜单动作与卡片同源）、G-MN（胶囊不再提示缺失用量样本）、G-NW（飞书幂等键 / 投递退避）、G-NY
（胶囊回到流内）。

**发布后线上只读验收 11/11 PASS**（`/tmp` 一次性脚本，未入库）：线上 bundle == 本机安装的那份；
四个节点数据面（逐节点 `/api/dirs`）全可达；HTML 预览 `web/public/offline.html` 在 iframe 里真的
渲染出「当前离线」且无报错提示条；图片 `web/public/pwa-512.png` 原始尺寸加载、放大到 150%、重置回
100%、全屏灯箱打开且 Escape 收起；文件树菜单 →「Agent 配置」浮层列出 claude / codex。另用 CDP
合成双指手势在移动视口实测缩放 100% → 200%。

**2026-11-22 追加四端发布 `v0.5.5-1201-67-ga48e3684`**（origin/main `a48e3684`，含
`0cc70c6e..a48e3684` 全部提交；上一批记为「仍未部署」的 `045680dc`（G-PG 流尾异常）、
`bc508be4`（G-PI Windows Codex 桌面端 CLI）、`48d2074a`（G-NY 详情面板固定尺寸）一并上线）。
四端版本逐字一致：wsl / fn user 单元 `is-active` + health 200（`role=worker`）；pc-win 计划任务
PID 56792、`:7332` LISTENING、health 200；本机已安装同一二进制（`/mindfs/health` 200
`role=control`；该批发布时本机曾暂由旧进程应答，用户已在 21:01:37 重启完成，见下一条），静态资源
`assets/index-ChCKKhIO.js` 已由 7331 服务。本批内容：`0dd1abb5`（线上 bundle / 面板几何探针）、
`b32cd5c3`（按 500 行硬线拆四个超长文件，纯移动无行为改动）、`a48e3684`（拆分文件的上游定制
锚点跟随）。门禁在干净 worktree 上跑：`go test ./...` 全绿、`tsc --noEmit` clean、
Vitest 263 文件 / 877 通过 / 6 跳过。

**2026-11-22 四端发布 `v0.5.5-1201-112-g4a039bdb`**（发布构建 `4a039bdb`；核对记录
`28266b98` 已在 origin/main）。用户重启本机后已完成核对：四端版本逐字一致，本机 PID `3316496`
（21:01:37 起）、`/proc/3316496/exe` 指向 `~/.local/bin/mindfs`、两者 md5 同为
`5ab087de057ae5bc79b6c3f00e852bc2`，`/mindfs/health` 200 `role=control`；wsl / fn 的 user 单元
health 200 `role=worker`；pc-win 进程 PID `4276`、`:7332` LISTENING、health 200 `role=worker`。
本批上线 G-MD「会话流式记录 A+」（provisional 块流 + 中断恢复 + 前端 interrupted 失败态）。
门禁：`go test` 全绿（kanban 295.410s）、`tsc` clean、Vitest 266 文件 / 889 通过 / 6 跳过、
`make check-upstream` 2419 delta / 368 组全绿。

