# 过期任务提示与 root 错误可诊断性修复

> Scope IDs: G-QL（claimed 2026-11-23T17:57Z）

Plan 2026-11-24。承只读诊断结论：agent 在 wsl（worker）为「冰晶智能」建任务组/模板任务时，
收到 `POST /mindfs/api/tasks?user=local → 400 {"error":"root not found"}`，随后误判为 root/session
路由或 Unicode 问题。证据（codex rollout `rollout-2026-11-23T09-21-28-...jsonl`、wsl journal、
`.mindfs/sessions/1795063654-f9d13a7e5d29.*`）：

1. `server/internal/api/usecase/session_prompt.go:30` 仍向每个会话注入
   `mindfs -orchestration` / 任务组 / 模板任务；这套能力已在 Phase 3（G-LJ）删除，
   同转录里 `mindfs -orchestration` 实测报 `flag provided but not defined`。
2. `?user=local` 在 worker 上经 `auth.DeriveAccountID` 解析为**空工作区**（产品既定语义，
   `server/app/unknown_user_test.go` 钉住），所以 `root_id=冰晶智能` 查不到时只回一句
   `root not found`，既没说是哪个账户、也没说这台机器有没有这个项目。
3. wsl 安装目录里的 `web/` 是 10 月旧版（含已删的模板 API 文案），但 worker 对 `/mindfs/`
   返回 403，本身不服务页面；agent 是**读安装目录**才被误导。

## 范围（用户已授权「那你动手吧」的代码修复）

- **A**：删掉 `session_prompt.go:30` 的过期能力注入，保留 root_id/session_key/task_id 上下文；
  加内容守卫测试，防回流。
- **B**：任务创建接口在 root 查不到时回带账户与节点提示的错误（保留 `root not found` 子串，
  不改 400 语义），让调用方当场知道该核对 `user=` 与目标节点。

## 明确不做（有证据，不是遗漏）

- **不改 worker 的未知账户语义**：空工作区是 control/worker 共用的既定设计
  （`server/app/unknown_user_test.go:130-148`），改成 `unknown_user` 会破坏跨机器可见性与
  新账户在 worker 上首次建数据的能力。
- **不在本轮清理 wsl 安装目录的旧 `web/`**：那是对生产节点的删除操作，另外授权。
- **不 push、不部署、不重启、不改任何节点的配置或数据库**。

## 执行结果（2026-11-24）

- 两处 RED 测试先写后改，各自变异（复原旧提示文案 / 关掉错误增强）打红、还原复绿。
- `go test ./server/internal/api/... -count=1` 全绿（api 72.1s、usecase 52.0s）；
  `make check-upstream` 2674 delta / 390 组全绿。
- 本组改动已提交（Scope: G-QL），只含本组 7 个文件；**未 push、未部署、未重启、
  未改任何节点配置或数据库**。共享工作区里另一条会话的
  `server/internal/kanban/{activity_route_apply.go,router_inspection_turn_test.go}` 未动。

## 针对性测试

| 测试 | 钉住什么 |
|---|---|
| `TestBuildPromptDoesNotRecommendRemovedTaskFeatures` | 提示不再出现 `-orchestration` / task group / template task，同时保留 root_id 与 session_key 上下文；复原旧文案即红 |
| `TestTaskCreateRootNotFoundNamesAccountAndNode` | 严格 root provider 下 400 错误含 root_id / user / 目标节点提示；去掉文案增强即红 |

## 验收

- 新增 RED 测试先行：prompt 禁词守卫、task create 的 root 错误文案。
- 定向 `go test ./server/internal/api/usecase ./server/internal/api`；`make check-upstream`；
  Conventional Commits（中文、`Scope: G-X`、只用 `git commit -- <pathspec>`）。
