# MindFS — AI Agent Remote Access Gateway · Result Visualization

Go 后端 + React/TS 前端，Web 静态资源由后端 HTTP 服务。
四个实例（本机 / wsl / fn / pc），重启权限一律见[节点表](#节点表别叫错)，别凭印象写。

## Agent 指令入口与规范

`AGENTS.md` 是**唯一**的项目指令入口；只有规则索引由它内联加载，执行计划按需读取：

@docs/agent/rules/README.md

- 不要新增 `CLAUDE.md` / `GEMINI.md` 这类空壳——存在时会遮住本文件的自动发现（`web/tests/agent-docs.test.mjs` 钉住）。
- 代码规范：[docs/agent/rules/README.md](docs/agent/rules/README.md)；执行计划：[docs/plans/README.md](docs/plans/README.md)，只有标为 Active 的可执行；发布 / 部署历史：[docs/plans/DEPLOY_HISTORY.md](docs/plans/DEPLOY_HISTORY.md)，计划执行记录：[docs/plans/PLAN_HISTORY.md](docs/plans/PLAN_HISTORY.md)，均按需读取。
- `@import` 会被 Claude CLI 在 `session_start` 全部展开；plans 索引随每次发版增长，内联会把本地预检预算顶爆。因此这里只保留小体积规则索引，plans / 部署史用上面的链接按需读取，不写 `@` 行。
- 若规则冲突：平台/用户要求优先；安全、权限、节点/部署边界以本文件为准；语言/模块约束以 `docs/agent/rules/` 为准；任务范围以 Active 计划为准。

## 节点表（别叫错）

| 代号 | 实际是 | 主机名 | 节点名 | 单元 | 监听 | 重启 |
|---|---|---|---|---|---|---|
| **本机** | Proxmox VM Ubuntu 26.04 | `home.xiaokubao.space` (192.168.1.6) / `xiaokubao` | `home` | system `/etc/systemd/system/mindfs.service` | `127.0.0.1:7331` | `sudo systemctl restart mindfs`，**仅用户执行** |
| **wsl** | WSL Ubuntu 24.04 | `pc.xiaokubao.space` / `nnb` | `pc` | user `~/.config/systemd/user/mindfs.service` | `127.0.0.1:7331` | `ssh wsl 'systemctl --user restart mindfs'`，Agent 可执行 |
| **fn** | fnOS | `fn.xiaokubao.space` / `xiaokubao` | `fn` | user `~/.config/systemd/user/mindfs.service` | `0.0.0.0:7331` | `ssh fn 'systemctl --user restart mindfs'`，Agent 可执行 |
| **pc** | 同台物理 PC 的 Windows 边控端（edge controller，旧称 pc-win） | `ssh pc` → 宿主 / `小哭包` (`C:\Users\10178`) | `pc`（共享节点表里 `remote: true`、旧直连前缀 `/mindfs-win`；`role=control`、`control_sync.role=edge`） | 计划任务 `\mindfs`（S4U + BootTrigger，Session 0） | `0.0.0.0:7332` | `ssh pc "schtasks /run /tn mindfs"`；详见 memory `windows-worker-on-pc` |

- **pc 是边控端，不是 worker**：`/api/node-info` 报 `role=control`，控制面请求经配对通道到主控；但分发路径仍与两个 worker 相同（本机编译产物 + ssh 安装），Windows 上没有源码库。
- **本机不叫 PC**。「PC」= WSL 所在的 Windows 物理机（`ssh pc` → Windows 宿主；`ssh wsl` → WSL）。
- 本机没有 user bus：`systemctl --user ...` 报错不代表服务不存在，查 `/proc/$(pgrep -x mindfs)/cgroup`。
- `/home/nnb/...` 是 WSL 家目录，本机是 `/home/xiaokubao`。
- **WSL 与 fn 没有源码库**，永远别在 worker 上编译。唯一分发路径是 `bash scripts/deploy-all.sh`（tar + ssh 安装），**不推 `web/dist`**（worker 是 `role=worker`，`GET /` 403）。
- pc「装上了」与「在跑」是两件事，`deploy-pcwin.sh` 取不到 PID 就明写「未确认」；自己核对：`ssh pc "netstat -ano | findstr :7332"`。
- 判断**本机**是否真的跑着刚装的二进制：`/proc/$(pgrep -x mindfs)/exe` 与 `~/.local/bin/mindfs` 同 inode，且 `GET /mindfs/api/app/update` 的 `current_version` 等于安装版本。只比 `--version` 不够（那是磁盘上的文件）。

## 构建与部署

```bash
make build          # web dist + go binary
make install        # 安装到 ~/.local/bin/mindfs + ~/.local/share/mindfs/web/
make build-web      # 仅前端
make test           # Go 测试
make dev            # go run 后端 + Vite 前端
bash scripts/deploy-all.sh  # 全流程收尾：门禁 → 提交推送 → 本机装 → 分发 WSL/fn/pc → 对账
```

**构建/重启分工（必守）**：编译与安装由 Agent 自行执行；本机 `sudo systemctl restart mindfs` 仅用户执行（会杀所有 claude 子进程）；WSL 与 fn 的 user 单元 Agent 可直接重启。

**代码变更收尾（必守）**：走 `ship` skill（见 `.claude/skills/ship/SKILL.md`）；不要手搓 make/ssh 序列；报告成功前独立核对进程在跑。

Go 工具链在 `$HOME/.local/share/go`（1.26.5），裸跑 `go` / `make test` 即可，永远不要加 `export PATH=...`。

## 关键架构约束（必守）

1. **ws.go 单读 goroutine**：所有 handler 必须 `go h.handleXxx(...)` 异步派发，同步调用会冻结整个连接。
2. **Interrupt 必须加超时**：SDK 内部用 `context.Background()`，调用方必须包超时。
3. **静态资源不 embed**：`make install` 拷到 `~/.local/share/mindfs/web/`，改前端需 `make build-web && make install`。
4. **多账户是配置档，不是安全边界**：API 层不鉴权（用户决策）；`user=` 只分项目/会话/任务库，其余全共享；共享必须用同一份实例（不是同一文件两份实例）；`MetaDir()` 账户私有 vs `SharedMetaDir()` 不看账户。见 `docs/multi-user-prd.md`。
5. **控制面只在主节点**：账户表/偏好/节点表等在主节点；数据面（项目/会话/任务/文件/agent 池）各机器自有。前端控制面请求用 `controlPath`（不接受 nodeId），数据面用 `appPath/appURL + nodeId`。见 `docs/multi-node-control-plane.md`。
6. **旁路状态逐节点拉，合并只能增量**：`appPath` 不传 `nodeId` 静默打激活节点；合并不能整体替换（节点失败会误清他人状态）。见 `web/tests/cross-node-replying-state.test.mjs`。
7. **看板任务是对话模型**：新建任务只含一条用户留言，每轮由 Router 决定走 workflow 还是直连 `RunDirectActivity`；阶段/模板概念已删除，冻结列是只读证据。见 `docs/migration/stages-to-conversation.md`。
8. **worktree 会话 repoint**：转录在 `--worktree-*` slug 下才走 `POST /api/sessions/{key}/repoint`；已在主 slug 时只清 `related_worktree`。四步顺序不可换：Close → 搬文件 → 换绑定+推进游标（事务） → 清归属。
9. **配置文件读取**：`make build` 装的是 `./cli/cmd`，修复必须落在这里（不是 `server/cmd/mindfs-server`）；`config.json` 缺省路径 `<config-dir>/config.json`，flag 优先于文件。

## 数据流

```
浏览器 ←WS→ ws.go（单读分发）→ usecase 层 → agent pool → claude/codex session
                                                   ↓
                                          session manager（SQLite WAL）
```

## 开发注意

- 提交用 Conventional Commits（中文描述）。
- 改 ws.go handler：确认在 goroutine 内派发。
- 改 agent session：Interrupt/Cancel 必须有超时，不能裸用 `context.Background()`。
- 前端改完：`make build-web && make install`。
- 发版走 `apk-release` skill；`GH_REPO` 必须显式指定，私钥只走 `MINDFS_RELEASE_PRIVATE_KEY_FILE`。
- 验线上行为用 `e2e/live/`（凭证在 `.env`），不能用 `e2e/`（隔离实例）替代。

## fork 定制与上游合并（必守）

每次改动三件事缺一不可：
1. **登记 `docs/upstream-customizations.yaml`**：写清「哪个文件、改了什么、为什么必须保留、可见症状」。
2. **留针对性测试**，并在该组「针对性测试」表里列出「测试 → 钉住什么」。
3. **提交信息标 `Scope: G-X`**，与清单组号对应。

三件齐了再跑 `make check-upstream`（只看已提交文件；提交前红 = 清单没写全；提交后红 = 定制被覆盖）。

合上游后跑全量门禁；测试红了按清单「为什么保留」逐条恢复，不按上游重写。
