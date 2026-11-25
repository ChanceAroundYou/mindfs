# hasee 边控端：状态可查 · 安装器快捷方式 · 局域网可达 · 自动切换 · 升级

> Scope IDs: G-QV / G-QW（claimed 2026-11-24T11:27Z）

Plan 2026-11-24。用户在 hasee（Windows 边控端，`role=control` + `control_sync.role=edge`，
`:7332`）真机装好并配对成功后提出五条需求，并批准按本节 D1–D6 的推荐执行（原话「全部按你推荐」）。
Scope: G-QV（主节点状态摘要）/ G-QW（安装器快捷方式）。P2 与 P3 的组号在实施时另行 claim：
它们要么是纯运维（防火墙 / nginx / `nodes.json`），要么尚未动工，提前占号会让门禁
断言 3/5 撞上一个没有 files 与测试的空条目。

## 需求与裁定

| # | 用户原话要点 | 裁定 | 落地波次 |
|---|---|---|---|
| ① | 安装器要正式创建开始菜单快捷方式，不要临时脚本 | D1 | P1 |
| ② | 边控端要出现在节点列表里，别的设备也能访问它 | D1 阶段方案：Windows 防火墙入站放行（源限局域网）+ nginx vhost，先解决局域网直连。**主控中转已另行立计划**（见下方 D6 注） | P2 |
| ③ | 主节点上 `/api/control-sync/status` 返回 404 | — | P0（已完成） |
| ④ | 不要手动切地址，在线/离线自动切换 | D2：书签指边缘地址；边缘 `online` 时对**顶层导航** 302 到主控，API / WS 不跳转 | P3 |
| ⑤ | 需要升级，但先给完整方案 | D5 顺序：hasee → 主控端 → 前端；**本机 `sudo systemctl restart mindfs` 仅用户执行** | P4 |

其余裁定：

- **D3 卸载**只删快捷方式 / 计划任务 / 程序文件，**保留 `config.json` 与数据**（配对 token、`control_sync.db`、任务与会话库都不动）。
- **D4 主节点 status 只读摘要**：不带 token 也可读，但**不含任何凭据与记录载荷**（这条端点主节点公网可达，浏览器里没有 token）。
- **D6 中继：** 本计划立案时的口径是「不实现主控中转」，理由是当时没有主→边转发实现。**该口径已被 2026-11-25 的新计划取代**：[边控端反向接入（Edge Relay Tunnel）](2026-11-25-edge-relay-tunnel.md)（Scope: G-RF / G-RG / G-RE）实现了 `/mindfs/relay/<edge-name>/` 反向隧道，relay 侧不加鉴权、完全跟随主控暴露面。本计划的 P2 仍只负责**局域网直连**这一路；中继这一路按新计划推进。两者不冲突：浏览器优先直连（前端节点 URL 机制），不通再走主控 relay。

## 边界（本轮不做）

- 不新建或轮换密钥；不改 ACL / UAC / 正式计划任务权限；不删除已有 release 与数据。
- push / 部署 / 发 release 各自单独授权；本文档只记录到「代码与测试 + 本地提交」。
- 快捷方式「装的时候真的出现了」只能由 hasee 真机跑一次安装器确认；GUI 双击属于人工动作，不把脚本启动当成人工双击的证据。

## P0 主节点 `/api/control-sync/status`（Scope: G-QV，已完成）

**根因（读装配代码取证）**：`startControlSync` 的 primary 分支只填 `pusher / feed / edges / token`，
从不设 `w.service` —— `ModeController` 只在边缘创建。`handleControlSyncStatus` 第一句就是
`if h.ControlSync != nil`，于是主节点上直接 404；路由块同样只在 `h.ControlSync != nil` 时注册。
现象吻合：`/api/control-sync/status` 404，而 `/api/control-sync/edges` 401（已注册、要 token）。

**落地**：

- `server/internal/controlsync/status_primary.go`：`PrimaryStatus` DTO（`role` 恒 `primary`、
  `head_rev` / `schema_version` / `edge_count` / `active_edge_count` / `last_edge_seen_at` /
  `conflict_count` / `checked_at`）与 `(*Store).PrimaryStatus`。三项各查一次库，任一项失败整体失败 ——
  半份摘要比没有摘要更糟。已踢掉的边缘不计入 `active_edge_count`，也不参与 `last_edge_seen_at`。
- `server/internal/api/http_control_sync.go`：新增 `ControlSyncPrimaryStatus` 接口；handler 改为
  边缘优先 → 主节点摘要 → 都没有才 404。
- `server/internal/api/http_routes.go`：`ControlSync == nil && ControlSyncPrimary != nil` 时
  挂同一个 `GET /api/control-sync/status`；`POST /mode` 仍只在边缘存在（主节点没有模式可切）。
- `server/app/control_sync.go`：`controlSyncWiring.primaryStatus` + `primaryStatusSource`（时钟可注入）。

**测试**：`status_primary_test.go` 3 条（汇总口径 / 空库不是错误 / 关库必须报错）、
`http_control_sync_primary_test.go` 6 条（主节点 200、边缘优先且不查主节点、都没有 404、
路由真的挂上且 `/mode` 仍 404、空摘要 200、读库失败 500）；`server/app/control_sync_test.go`
两侧装配断言（主节点必须有 `ControlSyncPrimary`、边缘必须没有）。

**变异验证**（5 处各自打红后还原）：handler 不查 primary / 路由不挂 / 已踢边缘仍计数 /
`last_edge_seen_at` 不比较取最大 / 装配处不赋 `primaryStatus`。

## P1 安装器快捷方式（Scope: G-QW）

`setup.ps1` 与 `install.ps1` 目前**一行快捷方式代码都没有**（已 grep 取证）：装完只能靠人记住
`C:\Users\<user>\Programs\mindfs\bin\mindfs.exe`。用户在 hasee 上找不到入口就是这么来的。

- `scripts/windows-setup/setup.ps1`（**纯 ASCII**）新增 `Install-Shortcuts`：
  `%APPDATA%\Microsoft\Windows\Start Menu\Programs\MindFS.lnk`，target 安装目录的
  `bin\mindfs.exe`，**参数留空**。无参 `mindfs.exe` 的语义是「服务在跑就开页面」；
  带 `--internal-autostart` 会置 `foreground=true` / `autoStart=false`（`cli/cmd/mindfs.go`），
  那正是计划任务用的参数，快捷方式绝不能带。
- `install.ps1 -Uninstall` 同步删除该快捷方式（D3：只删快捷方式 / 计划任务 / 程序文件）。
- `internal/deploy/install_script_test.go` 补顺序锚点（定位 → 拷贝 → **建快捷方式** → 校验 → Done）
  与「`.lnk` 指向 bin\mindfs.exe 且不含 `--internal-autostart`」的源码契约。

**未验证**：hasee 真机上跑一次新安装器、确认开始菜单真的出现条目 —— 需要一次重装（P4 顺带做）。

## P2 节点表条目与局域网可达（纯运维，无仓库代码，实施时再 claim 组号）

**取证**：`nodes.json` 只有四条（local / pc-wsl / fn / pc），**没有 hasee**；本机 / wsl / fn 三台
`curl http://192.168.1.15:7332/mindfs/health` 全部 `curl: (28)` 超时 —— Windows 防火墙默认阻止入站。

- `nodes.json` 增 hasee 条目（`id: hasee`，`remote: true`，地址 `http://192.168.1.15:7332`）。
  加条目本身不构成对外暴露：可达性由下一条决定。
- hasee 防火墙入站放行 7332，**源限局域网**（`192.168.1.0/24`），不做 `any`。
- nginx（Docker 容器 `nginx`，host 网络）加 hasee vhost，抄 `mindfs-win.inc` 范式：
  带 URI 的 `proxy_pass`（后端只认构建前缀 `/mindfs`，剥前缀会 404）、WS `Upgrade`、
  `proxy_buffering off`、长超时。证书 SAN 已含 `*.xiaokubao.space`，**不需要新签**。
- 前端节点表来源是控制面（主节点），所以条目进主节点 `nodes.json` 后各端都能看到。

## P3 边缘在线自动跳转（未动工，实施时再 claim 组号）

书签只留一个边缘地址，在线/离线自动分流：

- 边缘 `online` 时，对**顶层导航**（`GET /` 且 `Sec-Fetch-Mode: navigate`）302 到 `primary_url`；
  离线（`offline-local`）出本机镜像页。
- **API 与 WS 一律不跳转** —— 数据面本来就在本机，跳了就把请求送出局域网。
- 横幅按返回体 `role`（`primary`）与边缘四态分流，不再假设「没有 mode 就是没配置」。

## P4 升级（顺序 D5）

1. hasee：重跑新安装器（含 P1 快捷方式）→ 确认开始菜单条目、版本号、配对状态、`/mindfs/health`。
2. 主控端（本机）：`make install` 装新二进制；**`sudo systemctl restart mindfs` 由用户执行**（会杀所有 claude 子进程）。
3. 前端：`make build-web && make install`；边缘下一次镜像轮询会自动跟上。
4. wsl / fn 可由 Agent 直接 `systemctl --user restart mindfs`；pc-win 走 `scripts/deploy-pcwin.sh` 并核对 PID 与 `:7332` LISTENING。

## 验收

- 定向门禁：`go test -count=1 ./server/internal/controlsync ./server/internal/api ./server/app`、
  `go build ./server/...`；每个护栏做变异验证（改坏必须红）。
- 收尾门禁：`go test -count=1 ./...`、`tsc -b --noEmit`、`npx vitest run`、`make check-upstream`。
- 真机：hasee 开始菜单条目、`/mindfs/health` 200、主节点 `/api/control-sync/status` 200 `role=primary`、
  局域网另一台设备打开 hasee 地址能出页面。
- 不把未验证的内容写成已验证：GUI 人工双击、真机快捷方式出现都留到 P4 再记。
