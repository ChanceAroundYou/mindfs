# Agent 设置页统一：配置 / 供应商两入口 + 多 harness 共享同步

Plan 2026-11-11。状态：**Active**（实现中）。

## 目标

1. 把 Agent 设置里「添加配置」「切换重启」两条平行流程收成**一个「Agent 配置」入口**：
   先列 harness（显示当前激活配置、可就地重启），点进某个 harness 后再做 **切换 / 备份 / 同步**。
2. 把 **API 供应商与模型** 拆到**独立入口**，层级相同：先选 harness，再管该 harness 的供应商与模型。
3. 让「设置同步」沿用同一套 UI 与后端，覆盖 Codex 以外的 harness。用户已明确选择
   **「给其他 harness 也补同步后端」**，不是把按钮藏起来。

## 当前状态（为什么要改）

- 菜单里是 `添加 Agent 配置` / `Agent 配置切换重启` 两个按钮，各自是一个 flow；每个 flow 内部又有
  `backup` / `api`（或 `api_provider`）页签，两个 flow 的页签含义还不同（添加流程的 api 页签是
  “新建供应商”，切换流程的是“选择供应商”），同一批状态被两条路径交叉读写。
- `AgentConfigSyncStatus` 只在 `flow === "switch"` 且 `isCodexProfileAgent` 时渲染；后端
  `GET/POST /api/agent-config/sync` 直接调用 `codexsync`，共享仓库的允许路径也只有
  `codex/config.toml`、`codex/profiles/llmux.config.toml`、`codex/providers.json` 三个文件。
- 共享仓库 `~/.agents` 里已有 `claude/`（`settings.shared.json`、`mcp.shared.json`、`CLAUDE.md`、
  `rules/`、`hooks/`、`skills/`，以及 `scripts/render.sh`），但 MindFS 从不校验、同步或渲染它。

## 设计

### 1. 前端层级

菜单（`FileTreeAgentConfigMenuButtons`）只保留三个：

| 按钮 | flow | 第一层 | 点进 harness 后 |
|---|---|---|---|
| **Agent 配置** | `config` | 全部已安装 agent；每行显示 `last_config_selection.name` 与「重启」 | 页签 **切换 / 备份 / 同步** |
| **API 供应商 / 模型** | `provider` | 仅 `supports_api_provider_switch` 的 agent | 供应商列表（选择 / 测试 / 删除 / 新增）+「切换」 |
| **Agent 安装和更新** | lifecycle | 不变 | 不变 |

- `AgentConfigFlow` 从 `"backup" | "switch"` 改为 `"config" | "provider"`；
  `AgentConfigAddTab` / `AgentConfigSwitchTab` 两个状态合并为
  `AgentConfigDetailTab = "switch" | "backup" | "sync"`。
- 配置流程的「同步」页签**对所有 agent 都显示**：后端返回该 harness 的可用性
  （`available` + `message`）。不可用时明写原因（例如「该 harness 还没有共享配置适配器」），
  不伪造同步按钮。
- Codex 的 `switch` 页签仍只列 `source === "shared"` 的共享 profile；其他 agent 列全部本机备份。
- `agentConfigSwitchRequest` 深链（带 `providerIDs`）改为直接打开 `provider` 流程。
- 新增状态 `agentConfigShowProviderForm`，承载「新增供应商」表单；保存后仍关闭浮层。

### 2. 后端：harness 注册表（复用同一套同步引擎）

保留包名 `internal/codexsync`（改名会扩散到 API 层与 CLI），在包内增加 harness 适配器：

```go
type HarnessInfo struct {
    Name      string `json:"name"`
    Available bool   `json:"available"`
    SharedDir string `json:"shared_dir,omitempty"`
    Message   string `json:"message,omitempty"`
}

type harnessAdapter struct {
    name      string // "codex" / "claude"
    sharedDir string // repo 相对路径
    validate  func(repoRoot string) error
    render    func(paths Paths) error
}
```

- `Result` 增加 `Harnesses []HarnessInfo`（附加字段，旧前端忽略即可）。
- `HarnessStatuses()` 解析 paths 并探测每个适配器的共享目录/关键文件是否存在，供
  `GET /api/agent-config/sync` 与 `POST` 响应附带。
- `RunAt`：
  - `ModeCheck` / `ModeApply` / `ModeSync` 的共享校验改为**遍历全部适配器**；
  - 仓库同步完成后渲染**全部可用 harness**；
  - `invalid` 的消息带上失败的 harness 名，说明「运行时文件未改」。
- `IsAllowedRepoPath` 从三个文件扩展为共享仓库的目录白名单：
  `codex/`、`claude/`、`dsh/`、`skills/`、`scripts/` + 根级 `README.md`、`.gitignore`、`.skill-lock.json`。
  仍然拒绝绝对路径、`..`、反斜杠与任何白名单外的路径。
- 新增 `harness_claude.go`：
  - `validateClaudeShared`：解析 `settings.shared.json` / `mcp.shared.json`；拒绝「键名像凭据且值不是
    `${...}` / 空值」的条目，并拒绝形如 `sk-…` / `ghp_…` 的字面量 token。凭据只允许留在设备本地的
    `settings.local.json`（与 `render.sh` 的 adopt 语义一致）。
  - `renderClaudeRuntime`：移植 `~/.agents/claude/scripts/render.sh` 的语义：
    - `settings.json = deepMerge(settings.shared.json, settings.local.json)`，数组整体替换，
      `permissions.deny` 取各层并集（去重排序，单调收紧）；
    - `.mcp.json = deepMerge(mcp.shared.json, {mcpServers: local.mcpServers})`，值为 `null` 的
      服务器视为「本机停用」并删除，展开 `${HOME}`；
    - 若 `~/.claude.json` 存在，只合并其 `mcpServers` 键（保留其余设备本地键），并删除本机停用的名字；
    - 写入前做**语义比较**（解析后深比较），内容等价就不落盘；写入用原子替换并 `chmod 0600`；
      现有文件不是合法 JSON 时**拒绝覆盖**并报错。
- 其他 harness（`dsh` 等）暂不提供适配器：`HarnessInfo` 返回 `available=false` 与原因。
  新增一个 harness = 一个适配器文件 + 渲染/校验测试，不改同步引擎。

### 3. 安全边界

- 不共享：Codex 的 `auth.json`、API Key、OAuth token、项目路径、信任状态；Claude 的
  `settings.local.json`、`~/.claude.json` 除 `mcpServers` 外的任何键、`.credentials.json`。
- `.agents` 之外的路径永不进入 git 暂存；渲染只写本机运行时文件。
- `render.sh` 仍是 dotfiles 的权威渲染器；Go 实现必须与其语义一致，并用测试钉住
  （merge / deny 并集 / null 停用 / `${HOME}` / 语义等价不重写）。

## 验收

- 菜单只剩三个按钮；「Agent 配置」第一层每个 harness 都能看到激活配置并重启；
  点进后有切换 / 备份 / 同步三个页签。
- 「API 供应商 / 模型」第一层只列支持供应商切换的 harness，点进后可选择、测试、删除、新增供应商并切换。
- `GET /api/agent-config/sync` 返回 `harnesses`；Codex 与 Claude 为 `available=true`，
  其他 agent 为 `false` 且有原因；同步页签按可用性渲染。
- `go test ./internal/codexsync/... ./server/internal/api/...`、`make build-web`、全量 web 测试通过。
- `make check-upstream` 在提交后为绿：G-KP 扩展到多 harness（新文件、锚点、测试）。

## 测试清单

- Go：`internal/codexsync/harnesses_test.go`、`harness_claude_test.go`（merge / deny / ${HOME} /
  null 停用 / 幂等 / 非法现有文件拒绝覆盖 / 凭据拒绝）、`run_test.go` 扩充 allowlist 与多 harness 渲染。
- API：`agent_config_sync_handlers_test.go` 增 `harnesses` 字段断言。
- Web：`agent-settings-unified-menu.test.mjs`（菜单、层级、页签、provider 过滤、harness 可用性）、
  更新 `file-tree-agent-config-flow.test.mjs`、`agent-config-custom-codex.test.ts`、
  `agent-config-agent-step.test.mjs`。

## 风险与回滚

- **Claude 运行时文件被错误渲染**：渲染前校验、语义比较、原子写、非法文件拒绝覆盖；
  如出现异常，`settings.local.json` 与 `~/.claude.json` 原文件不受影响（只在语义变化时改写）。
- **pc-win 无 jq**：因此用 Go 渲染而不是调用 `render.sh`；Windows 上 chmod 为 best-effort。
- 回滚：前端与后端同一提交；同步引擎新增字段向后兼容，旧 worker 只是没有 `harnesses`，
  前端退化为「仅 Codex 可同步」。

## 执行记录

- 2026-11-11：计划落盘，开始实现。
- 2026-11-11：实现完成，门禁全绿。
  - 后端：`internal/codexsync/harnesses.go`（适配器注册表、`HarnessStatusesAt`、
    `validateAllSharedSources`、`renderSharedHarnessRuntimes`、`normalizeRepoPath`）、
    `harness_claude.go`（`validateClaudeShared` / `renderClaudeRuntime`，移植 `render.sh` 语义）、
    `profiles.go` 允许路径扩为目录白名单、`run.go` 在多 harness 上校验并在同步后渲染、
    `server/internal/api/agent_config_sync_handlers.go` 在响应里附 `harnesses`。
  - 前端：`AgentConfigFlow = "config" | "provider"`、`AgentConfigDetailTab = "switch" | "backup" | "sync"`；
    菜单三按钮（Agent 配置 / API 供应商 / 安装更新）；配置详情为切换/备份/同步三页签，
    同步页签按 harness 可用性渲染并显示不可用原因；供应商流程先选 harness 再管供应商与模型，
    「新增供应商」改为行内表单（`agentConfigShowProviderForm`），保存后留在列表。
    `agentConfigConfirmAction` 区分备份覆盖与切换确认，避免确认按钮执行错动作。
  - 验证：`go test ./internal/codexsync/... ./server/internal/api/...` 通过；
    `npx tsc --noEmit` 通过；全量 vitest 212 文件 / 492 测试通过（6 skipped）；
    `make build-web` 通过。另用临时测试把真实 `~/.agents/claude` + 本机运行时文件渲染到临时目录，
    与 `render.sh` 结果语义一致且真实文件零写入（测试文件已删除）。
  - 上游登记：G-KP 扩展为多 harness（新增文件、测试与锚点）；提交信息标 `Scope: G-KP`。
- 2026-11-11：`bash scripts/deploy-all.sh` 完成。门禁（Go / tsc / Vitest 492 项）通过；
  `aea2d182` 已推送主线；三端产物版本 `v0.5.5-1118-14-gaea2d182` 逐字一致：
  - WSL：`systemctl --user is-active` = active，`mindfs --version` = 该版本。
  - fn：同上，active 且版本一致。
  - pc-win：计划任务拉起，PID 179868，`curl http://127.0.0.1:7332/mindfs/health` →
    `{"ok":true,"role":"worker"}`。
  - 本机：二进制已安装为该版本，静态 bundle 与安装目录一致（脚本对账通过）；
    **system 单元仍待用户执行 `sudo systemctl restart mindfs`** 后进程才换成新二进制。
- 仍 Active 的唯一原因：本机 system 服务待用户重启（Agent 不得代劳），
  以及页面上的入口层级需在重启/刷新后做一次真机点验。