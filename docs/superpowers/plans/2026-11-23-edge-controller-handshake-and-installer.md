# 边控端（Edge Controller）：握手 · 安装包 · 自更新

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
| W1 | 主控同步默认开 + token 自生成 + 单主控守卫 + `/edges` 名单（注册/列表/踢人/恢复） | G-PZ | 完成（`fd48fa1f`、`11498130`） |
| W2 | 边控端握手与自动同步：`POST /api/control-sync/pair` + 未配对档位 + 落盘 | G-QA | 进行中 |
| W3 | Windows 独立安装包（自解包 exe + 托盘菜单 + 计划任务）+ 静默自动更新 | G-QB | 待做 |
| W4 | 部署与四端对账（需当次授权） | — | 待做 |

### W1（已完成）

- `control_sync` 段不写 = **默认启用**（只对 `control` 节点成立；worker 整段跳过）。
- 主节点没有 token 时在 `token_file` 上**生成一份**（0600、幂等）；生成失败才拒绝启动。
- `guardSinglePrimary`：一台机器只能有一个主控，曾是别人副本的拒绝无提示翻脸；
  出路是显式的 `adopt_primary`。
- `/api/control-sync/edges`：调用方被 `requireSyncToken` 顺手登记（`instance_id` 打标），
  可列出、踢掉（`revoked_at`）、恢复。**这张表是运维索引，不是准入白名单** —— 共享 token
  的语义就是「谁拿到都能进」，踢人只让那台机器的同步立刻 403 并停在本地模式。

### W2（进行中，Scope: G-QA）

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

## 验收

- 单测：`config.UpdateJSONFile` 7 条（未知键/大整数/0600/沿用 mode/坏 JSON/顶层数组/mutate 失败不落盘）、
  `controlsync.Pair` 5 条（401、worker 主控、schema、明文 http 四种失败都不留痕；prefix 保留）、
  配对端点 7 条（404/非回环 403/回环放行/坏体 400/502/其余 400/转发头不算本机）、
  cli 3 条（缺 token 与空 token 都进「等配对」、显式 `-config` 优先）、装配 4 条。
- 变异：去掉回环守卫、去掉 502 映射、edge 缺 token 仍拒启、未配对也起同步、配对不写 `role`
  五处各自打红后还原。
- e2e：隔离双实例（两个 `XDG_CONFIG_HOME`，端口 7470/7471，不碰 7331）跑「首次配对 →
  重启 → 追平 → 踢人后停在本地」。
- 门禁：`go test ./...`、`tsc --noEmit`、Vitest 全绿、`make check-upstream` 绿。

## 边界

- 不做自重启（理由见上）；不做 mDNS / 全局 registry；不做代码签名（默认）。
- token 仍 **只从 `token_file`** 读，不进 `config.json`、不进命令行、不进日志。
- 同步通道一律 https，可选 `ca_file`，**没有** insecure-skip-verify 开关。
- 本机 `sudo systemctl restart mindfs` 仅用户执行。
