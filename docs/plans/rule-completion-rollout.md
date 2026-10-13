# MindFS 规则与存量待办全量落地 Goal

## Goal prompt

```text
Goal：以当前仓库为准，逐项落实 AGENTS.md、docs/agent/rules/ 和唯一有效计划索引中的所有适用规则，并完成 frontend/backend TODO 里所有仍有效的未完成事项。持续工作到所有事项完成，或某项被证据证明不适用并在待办/计划中说明原因；不要停在审阅、拆分方案或一两个示范改动。

先检查 git 状态、分支、HEAD/upstream 基线、根 AGENTS.md、docs/agent/rules 全部 README/规则/TODO/reviewer-checklist、docs/plans/README.md 与唯一当前计划、相关源文件/消费者/测试脚本。保护开始时已有的用户改动；尤其不要纳入或覆盖未跟踪的 package-lock.json。所有行数、路径、消费者和验收基线以这次仓库实测为准，TODO 中旧行数只作线索。

范围：
1. Frontend TODO：services 的 update/agent 簇归位；loading/empty/error 复用组件；Utils/Helpers 大小写、类型和样式文件命名归一；指定 CSS 内联；33 个硬线大文件按职责拆分；6 项 logic→view 倒挂；测试栈迁移 Vitest；存量复用件补测。逐项对照当前 TODO，已完成项不得重复施工。
2. Backend TODO：对超过 1500 行的非测试 Go 文件逐个按职责拆分，先从当前最大且风险可控者开始；落实 session.go、api/http.go 的拆分约束；审计 cli/cmd 与 server/cmd/mindfs-server 双入口，只在正确的发布入口修启动逻辑。
3. 全量审阅 reviewer checklist 和长期规范：把能机械验证的长期规则加入现有门禁/针对性测试；逐条验证架构规则，不为打勾而改动正确实现。架构事实以 AGENTS.md 为准。

执行方式：
- 把工作拆成可独立审查的小批次；每批只处理一个紧密相关职责，迁移/拆分前先列外部消费者、符号、依赖方向和行为契约。
- 不做批量正则改路径；改名/搬迁逐项更新 import、测试、文档引用与 source-map hook 映射。
- 行为保持不变；需要行为改变时先证明现有行为违反哪条规则，并用回归测试钉住修复。
- 大文件按规则约束：前端每批 2–3 个，后端每次 1 个。拆分后父文件只留清晰组合/门面；不要为了行数制造无意义的小文件。
- 每批运行匹配的测试与类型检查，核对 Markdown 链接、规则入口、计划状态、upstream-customizations 三件套、git diff 和 App.tsx 组合根未被改动；不过门禁不得继续叠加风险改动。
- 每个 fork 定制按 docs/agent/rules/common/commit-and-customization.md 登记：更新 yaml 与 md 分组、症状/理由/边界/锚点、针对性测试，并在 Conventional Commit 中标 `Scope: G-X`。运行 `make check-upstream`。
- 更新 docs/agent/rules/frontend/TODO.md 或 backend/TODO.md 时，只在验收完成后勾选；计划结果与 docs/plans/README.md 在同一变更更新。已过期/不适用事项要留下简短证据，不静默删除。
- 遵守 AGENTS.md 的架构、账户隔离、控制面/数据面、WebSocket 单读 goroutine、Agent 超时、构建和权限规则。不得改 App.tsx 组合根；不得在 worker 编译；本机 systemd 永远由用户重启。
- 本 Goal 明确不处理 task-38 repoint；不等待其重启或以其为前置条件。部署按用户当前要求使用 `bash scripts/deploy-all.sh --no-restart`，禁止重启任何节点；如脚本行为与此选项不符，停在部署步骤并记录，不手工替代。
- 按仓库 ship skill 完成每个可交付里程碑：门禁、提交、推送、本机 build/install、worker 产物安装（不重启）及对账。用户已有显式“不重启”授权持续有效。

完成条件：所有适用 TODO 与规则门禁均落地；没有未解释的过期基线；frontend/backend 全部门禁、make check-upstream 和规则/计划链接校验通过；所有变更按 Scope 分批提交并推送；未重启任何节点；最终报告逐项列出完成/排除事项、测试与部署事实、提交、剩余用户动作。不得把未通过项说成通过，也不得仅凭大量文件已改就宣称完成。
```

## 执行记录

- 2026-10-14：用户明确停止关注 task-38 repoint。本计划不包含该事项。
- 起始工作区以 `main` 与 `origin/main` 实际状态核验；保留未跟踪的 `package-lock.json`。
- 已完成：frontend TODO `taskIcons.tsx` 导出职责清单，提交 `dce4b54`，专项测试与 upstream 门禁通过。
- 已完成：update 与 agent service 实现归入 `services/update/`、`services/agent/`，7 个根入口保留 facade；source-map 映射与 facade 专项测试同步更新。`npm run typecheck`、全量 `npm test`（232 通过、4 跳过）通过。
- 已完成：新增 `AsyncStateMessage` 通用状态消息组件，迁移项目目录浏览、worktree 列表和定时任务列表的三态显示；保留每处原有布局和状态优先级。专项测试与 TypeScript 检查通过。
- 已完成：核对 12 个 Utils/Helpers 文件的实际 JSX 与命名，将唯一违规的 `sessionCardUtils.tsx` 改为 `sessionCardHelpers.tsx`，同步三个消费者、复用文档和命名门禁。
- 已完成：审计 11 个类型文件与消费者；组件域唯一裸名 `components/action/types.ts` 改为 `actionTypes.ts`。目录隔离的 service 类型、i18n 跨域入口和其余 `xxxTypes.ts` 均按规则保留，并增加测试门禁。
- 已完成：将混合图标 JSX 的 `composerStyles.tsx` 拆为纯样式 `composerStyles.ts` 与 `composerIcons.tsx`，更新消费者、源码逻辑映射和命名门禁；其余样式模块已使用 `*Styles.ts`。
- 当前下一项：审计 FileEditor/DiagramPreview CSS 选择器和级联依赖，完成两处内联化并保留主题行为。
