# Windows 边缘端安装体验重构

> Scope IDs: G-RN / G-RO / G-RP / G-RQ / G-RR（claimed 2026-11-25T11:25Z）

Plan 2026-11-25。Scope: 见实施记录。工作区 `.worktree/windows-edge-shell`。

## Context

Windows 边缘端当前是「自解压桩 + 隐藏 PowerShell + 裸 exe 开浏览器」：双击闪退、无卸载入口、UI 只能走浏览器、更新是进程内 zip 替换、前端把当前设备硬编码成 `local`、首开弹 11 步漫游向导。

目标：标准 Windows 软件形态——NSIS 安装向导、应用和功能里的卸载、WebView2 套壳窗口、独立更新、去掉 `local` 语义、删除首开向导。

已拍板：WebView2 缺失接受联网下载一次；清理与真机实测放到代码完成后的单独一轮。

## P0 结论（2026-11-25 实测）

- `makensis` 3.10 可免 root：`apt-get download nsis nsis-common` + `dpkg -x`，`NSISDIR=<root>/usr/share/nsis` 直接运行，最小脚本可产出 117KB 安装器。
- `github.com/jchv/go-webview2`（v0.0.0-20260205173254）无 `import "C"`，内嵌 `WebView2Loader.dll`；`CGO_ENABLED=0 GOOS=windows GOARCH=amd64` 构建 3.8MB 通过。
- 不需要回退到「随包 DLL + 最小封装」或 Edge `--app`。

## 设计要点

### 进程模型

一个 `mindfs.exe`，三种入口：

- 无参数 / `--desktop`：套壳。单实例；持有 WebView2 窗口与托盘；健康检查不通时先 `schtasks /run /tn mindfs`，任务不存在则分离子进程起 `--serve`。
- `--serve`：服务。计划任务 `mindfs`（S4U + BootTrigger，Session 0）。
- `--internal-autostart`：保留现有入口。

壳退出只退壳，不停服务。窗口标题 = 节点名；错误页给日志路径、重试、重启服务、浏览器打开。

### 安装器（NSIS）

- payload：`bin/mindfs.exe`、`share/mindfs/{web,agents.json}`、`MicrosoftEdgeWebView2Setup.exe`（构建期从官方 fwlink 取并缓存）。
- 向导：欢迎 → 安装位置 → 进度 → 节点名 → 完成页（勾选启动）。**节点名页在 INSTFILES 之后**：查重要调用刚装好的 `mindfs.exe`，放在拷贝之前只能把整个 exe 再嵌一份。代价是 Back 会重跑安装段，所以该页禁用 Back。
- `RequestExecutionLevel user`；per-user 安装；只有首次注册 S4U 任务用 `ShellExecute(runas)` 提权一次。升级复用已有任务，`/S` 全程无 UAC。
- 升级原地覆盖（不再做 `.new`/`.prev` 交换与回滚）：安装包先 `schtasks /end` 停服务、`taskkill` 兜底桌面壳，再覆盖、再 `schtasks /run` 并轮询进程确认起来了。失败靠 `DetailPrint` 与错误框呈现，没有单独的 `install.log`。
- 卸载注册 HKCU Uninstall 键；停任务/进程 → 删文件/快捷方式/PATH/任务 → 单独一页问是否删除本机数据（默认不删）。
- 节点名查重走随包 `mindfs.exe --check-node-name <name> --out <json>`，退出码 `0/10/11/12`（可用 / 重名 / 格式不合法 / 主控不可达）；主控地址从已有 config 的 `control_sync.primary_url` 读，不在向导里另填。主控不可达时向导确认后继续，重名留给配对时再判 —— **配对本身不强制 `instance_id` 唯一**，这是本节已知边界。

### WebView2

三处注册表 `EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}` 的 `pv`（HKLM WOW6432Node / HKLM / HKCU）。缺失时解出内嵌 Evergreen Bootstrapper，`/silent /install`；失败给重试 / 官方下载页 / 跳过继续安装。壳运行期缺失给原生错误框 + 浏览器打开。

### 更新

复用 `server/internal/update` 的签名清单（ed25519）、SHA256、版本比较、busyProbe、`/api/app/update`。Windows installed 布局改为选 `mindfs_<ver>_windows_amd64-setup.exe`（签名清单已包含），下载校验后落到 `<prefix>\update\mindfs-setup.exe`，再 `schtasks /run /tn mindfs-update` 并交出进程。

**为什么必须绕一圈走旁路任务**：任务实例结束时它拉起的子孙进程一起被终结。服务自己起 PowerShell 等旧进程退出再覆盖文件，那个 PowerShell 是服务的子进程，服务一退出它就死 —— 表现是「更新完服务消失且无日志」。所以安装时随服务任务一起注册伴生任务 `mindfs-update`（同样是 S4U，`/S` 静默执行安装包），安装包不在服务的 job 里，能活到把服务重新拉起来。

**桌面壳不跟着重启**：没有登录任务，静默更新会关掉更新前开着的壳窗口，用户从开始菜单重开即可（服务不受影响）。

### 身份语义

`/api/node-info` 增加本机 `id`/`name`：

- 主控：`home` / 「主控」。
- 边缘：`control_sync.instance_id` / 安装向导写入的节点名。

前端删除 `LOCAL_NODE_ID`，用 node-info 构造 self 节点。迁移键：`mindfs_active_node_id`、scope 前缀 localStorage、服务端 `session_pins.json`、IndexedDB 会话索引、`mindfs-last-root-node`；幂等 + 兼容读一版。

### 首开向导

删除 `web/src/services/prefs/onboarding.ts`、`components/shell/{OnboardingTour.tsx,onboardingSteps.ts,useOnboardingTour.ts}`、`components/app/OnboardingGate.tsx`，清理两处集成、11 处 `data-onboarding` 与 i18n。保留未配对边缘的配对页。

## 实施顺序

| 阶段 | 内容 | 验收 |
|---|---|---|
| P0 | 工具链 spike | ✅ 本文上方 |
| P1 | 身份语义（后端 + 前端 + 迁移） | ✅ Go/web 单测（G-RN） |
| P2 | 删除首开向导 | ✅ web 全套（G-RO） |
| P3 | 桌面套壳 | ✅ 交叉编译 + Go 单测（G-RP）；真机开窗待单独一轮 |
| P4 | NSIS 安装器 | ✅ makensis 构建 + 结构单测（G-RQ）；真机安装/卸载待单独一轮 |
| P5 | 更新通道 | ✅ Go 单测（G-RR）；真机升降级待单独一轮 |
| P6 | 定制登记 / Scope / 计划索引 / 全量门禁 | `make check-upstream` 绿 |

## 明确不做

- 代码签名证书（SmartScreen 仍会提示未知发布者）。
- 清理 pc/hasee 现有安装、pc 真机实测：代码完成后的单独一轮，删除前列完整路径清单逐项确认。
- 主控/边缘控制面同步协议本身（沿用 control_sync）。

## 实施记录

- P1 / G-RN `1337c38a`：`/api/node-info` 自报 id/name，前端 `isSelfNode()` 取代 `local`，作用域键逐段迁移，查重收口 `POST /api/nodes/validate-name`。
- P2 / G-RO `a508d224`：五个向导文件、29 条 `onboarding.*`、全部 `data-onboarding` 锚点删除；e2e 改用 `data-composer-input` / `data-composer-send`。
- P3 / G-RP `3f2bfced`：单 exe 三入口；`desktop.go`（URL / 窗口位置 / 健康等待 / 等待与错误页，跨平台可测）+ `desktop_windows.go`（WebView2、单实例、托盘、WebView2 缺失三选框）+ `desktop_other.go`；托盘从服务进程搬到登录会话（6 项菜单）；关窗进托盘、退出只结束壳；健康断开→恢复自动重载。无 CGO 的 `jchv/go-webview2`，保持 `CGO_ENABLED=0 GOOS=windows` 构建。Linux `go build ./...` / `go vet ./cli/cmd/` / `go test ./cli/cmd/` 绿，Windows 交叉编译 33MB 通过。真机开窗按用户要求放到代码完成后的单独一轮。
- P4 / G-RQ `e48f7fcf`：`scripts/windows-installer/mindfs.nsi`（NSIS 3.10 Unicode / MUI2）+ `scripts/build-windows-installer.sh`（Linux 交叉构建，makensis 在 `~/.local/nsis`）；`build-all` 改调新脚本，旧的 `scripts/build-windows-setup.sh` 不再接进发版链。结构单测 13 条钉住：标准向导页、卸载器与 ARP 条目（含「同时删除本机数据」独立勾选）、`schtasks /end` 停服务 + `taskkill` 兜底、`/run` 后轮询进程（IgnoreNew 会静默吞掉 `/run`）、节点名页在 INSTFILES 之后且禁 Back、WebView2 三处注册表 + 内嵌 Bootstrapper、构建前校验 payload 完整。新增 `cli/cmd/node_name.go`（`--check-node-name` / `--set-node-name`，退出码 0/10/11/12）、`cli/cmd/init_config.go`、`cli/cmd/service_task_{windows,other}.go`（服务任务 + `mindfs-update` 一起注册/注销）；`UpdateJSONFile` 实现移到根 `internal/configfile`，`server/internal/config` 留转发壳（`cli/` 不能 import `server/internal/*`）。makensis 实编译通过（200,361 字节，dummy payload）。真机安装/卸载/重装按用户要求放到代码完成后的单独一轮。
- P5 / G-RR `6a362670`：`server/internal/update/installer_update{,_windows,_other}.go` —— installed 布局选 `-setup.exe`，下载→验签→原子落到 `<prefix>\update\mindfs-setup.exe`→`schtasks /run /tn mindfs-update`→延时交出进程；`release_fetch_helpers.go` 按布局选后缀，`install.go` 在清单查找后分流。`internal/deploy/setup.go` 新增 `SetupUpdateTaskName` / `SetupUpdateStagingPath` / `BuildUpdateTaskXML`（空触发器、`PT1H`、S4U、LeastPrivilege、IgnoreNew）。根因是计划任务的 job 语义：服务自己起的 PowerShell 会随服务退出被终结，所以安装包必须由另一个任务实例执行。单测覆盖布局选型、坏 sha256 不启动、启动失败报出可手动运行的路径、`installUpdate` 端到端分流。旧自解压桩的 `restartInstalledBinary` 路径在 windows installed 布局下不再走到。
