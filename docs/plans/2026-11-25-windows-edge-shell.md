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

- payload：`bin/mindfs.exe`、`share/mindfs/{web,agents.json}`、`MicrosoftEdgeWebview2Setup.exe`（构建期从官方 fwlink 取并缓存）。
- 向导：欢迎 → 安装位置 → 节点名（默认计算机名 + 主控地址，实时查重）→ 进度 → 完成页（勾选启动）。
- `RequestExecutionLevel user`；per-user 安装；只有首次注册 S4U 任务用 `ShellExecute(runas)` 提权一次。升级复用已有任务，`/S` 全程无 UAC。
- 升级固定沿用已有目录；`.new`/`.prev` 交换 + 健康检查失败回滚。
- 卸载注册 HKCU Uninstall 键；停任务/进程 → 删文件/快捷方式/PATH/任务 → 单独一页问是否删除本机数据（默认不删）。
- 失败可见：向导错误页 + `install.log`。
- 节点名查重走随包 `mindfs.exe --check-node-name <name> --primary <url> --out <json>`（退出码 0/2/3），主控不可达只警告；配对/改名时服务端强制查重。

### WebView2

三处注册表 `EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}` 的 `pv`（HKLM WOW6432Node / HKLM / HKCU）。缺失时解出内嵌 Evergreen Bootstrapper，`/silent /install`；失败给重试 / 官方下载页 / 跳过继续安装。壳运行期缺失给原生错误框 + 浏览器打开。

### 更新

复用 `server/internal/update` 的签名清单（ed25519）、SHA256、版本比较、busyProbe、`/api/app/update`。Windows installed 布局改为选 `mindfs_<ver>_windows_amd64-setup.exe`（签名清单已包含），下载校验后分离进程 `/S`；安装器停任务/壳、替换、起任务，并用登录任务 `mindfs-desktop` 把壳拉回交互会话。壳健康恢复后 reload。

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
| P1 | 身份语义（后端 + 前端 + 迁移） | Go/web 单测 |
| P2 | 删除首开向导 | web 测试 |
| P3 | 桌面套壳 | 交叉编译 + 真机开窗 |
| P4 | NSIS 安装器 | makensis 构建 + 真机安装/卸载 |
| P5 | 更新通道 | 单测 + 真机升降级 |
| P6 | 定制登记 / Scope / 计划索引 / 全量门禁 | `make check-upstream` 绿 |

## 明确不做

- 代码签名证书（SmartScreen 仍会提示未知发布者）。
- 清理 pc/hasee 现有安装、pc 真机实测：代码完成后的单独一轮，删除前列完整路径清单逐项确认。
- 主控/边缘控制面同步协议本身（沿用 control_sync）。