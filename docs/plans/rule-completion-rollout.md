# MindFS 规则与存量待办全量落地 Goal

## Goal prompt

```text
Goal：以当前仓库为准，逐项落实 AGENTS.md、docs/agent/rules/ 和唯一有效计划索引中的所有适用规则，并完成 frontend/backend TODO 里所有仍有效的未完成事项。持续工作到所有事项完成，或某项被证据证明不适用并在待办/计划中说明原因；不要停在审阅、拆分方案或一两个示范改动。

先检查 git 状态、分支、HEAD/upstream 基线、根 AGENTS.md、docs/agent/rules 全部 README/规则/TODO/reviewer-checklist、docs/plans/README.md 与唯一当前计划、相关源文件/消费者/测试脚本。保护开始时已有的用户改动；尤其不要纳入或覆盖未跟踪的 package-lock.json。所有行数、路径、消费者和验收基线以这次仓库实测为准，TODO 中旧行数只作线索。

范围：
1. Frontend TODO：services 的 update/agent 簇归位；loading/empty/error 复用组件；Utils/Helpers 大小写、类型和样式文件命名归一；指定 CSS 内联；30 个硬线大文件按职责拆分；6 项 logic→view 倒挂；测试栈迁移 Vitest；存量复用件补测。逐项对照当前 TODO，已完成项不得重复施工。
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
- 已完成：将 `FileEditor.css` 与 `DiagramPreview.css` 的 CSS 原文内联到对应组件 `<style>`，保留全局主题、媒体查询、伪元素、CodeMirror 后代选择器和安全区声明；删除独立 CSS 文件，source-map 旧逻辑路径改映射至组件，并增加针对性守卫。
- 已完成：将 `useRealtimeEvents.ts` 的文件变更批处理与单文件事件（缓存失效、插件/Git/目录刷新）抽为 `realtimeWsFileChangedHandlers.ts`，更新旧逻辑路径映射并加专项守卫。原 31 项硬线清单中，useRealtimeEvents 现为 392 行并从清单销项，剩余 30 项；components 子项实际 17 个，最初“33”与“14”均为计数错误。
- 已完成：将 `useRealtimeEvents.ts` 的文件变更事件处理抽为 `realtimeWsFileChangedHandlers.ts`（G-BV），并将 slash-command chunk/login/error/done 处理抽为 `realtimeWsSlashCommandHandlers.ts`（G-BW）；更新旧逻辑路径映射及两组专项守卫。hook 从 1003 行逐步降至 392 行，低于 500 行硬线。
- 已完成：将主要 session stream 分支（节点/会话归属、活动时间、pending attach、消息块分发、compact 与结束/error 处理）抽为 `realtimeWsSessionStream.ts`；回合收尾与 replay 编排仍留在 hook，避免跨边界改变重锚定顺序。源码映射与针对性守卫同步更新，hook 从 1003 行逐步降至 392 行，低于 500 行硬线。
- 已完成：`useSendMessage.ts` 的 plan 前缀逻辑迁入 `shared/planCommand.ts`，app hook 不再导入 action view helper；专项测试覆盖前缀检测、兼容返回值、剥离与拼装。`useSessionLifecycle.ts` 的未使用 `SessionViewer` import 已删除；TODO 将这两项依赖方向问题销项。
- 已完成：`ProjectAddMode` 与 `GitHubImportState` 移入 `shared/projectAddTypes.ts`，`useRealtimeEvents`、其上下文与 WS handler 改为依赖共享类型；组件保留 type re-export 兼容现有 UI 消费者。专项测试守住依赖方向与 facade。
- 已完成：把「新项目 meta 位置」偏好更新从 `FileTree` JSX 移入 `useFileTreePreferences`，保留乐观更新、失败回滚和 busy 状态；增加针对性守卫，FileTree 仍有其它 services/import 与职责需继续拆分。
- 已完成：FileTree 外观模式从 colocate `useFileTreeAppearance` 读取并持久化；view 只把选项交给 `onChange`。`AppearanceMode` 类型移到 `shared/appearanceTypes.ts` 并由 service facade 再导出，外观选项配置不再依赖 service 类型路径。
- 已完成：agent config 流程 hook 直接依赖 agent 查询 service，`FileTree` 不再将 fetch 函数作为 context 透传；同时删除 `FileTree` 内陈旧的 agent config/WebPush/preferences service value imports。
- 已完成：font size 的纯 region 类型、边界常量和步进算法迁入 `shared/fontSize.ts`；`fontSize` service 保留存储职责并再导出原 API，FileTree presenter 改用 shared 函数。
- 已完成：发送快捷键类型、键盘事件解析、匹配与显示格式化迁入 `shared/sendShortcut.ts`；service 只保留 localStorage 读写并兼容再导出，FileTree view 不再依赖 `sendShortcut` service。
- 当前下一项：继续 FileTree 拆分，清理剩余 view 对 services 的依赖与菜单组合职责，再按实测行数销减硬线清单。
- 已完成：将 FileTree 设置菜单的呈现 JSX 提取到 `FileTreeSettingsMenu.tsx`；目录排序类型、选项与纯算法迁入 `shared/directorySort.ts`，原 preferences service 保留兼容 facade。`FileTree.tsx` 实测降至 702 行，仍超过硬线，继续拆分；没有提前销项。新增排序纯度、排序语义、facade 与依赖方向测试。
- 当前下一项：继续拆分 FileTree 的 settings/menu 组合与其余 view 逻辑，按实际行数和依赖方向验证，不改变 App.tsx 组合根。
- 已完成：将 bootstrap readiness 订阅、node groups 查询及项目 tab accent 的节点颜色回退从 FileTree/view presenter 收入 colocated hooks；presenter 改收值，保留原始分组顺序与空态语义。专项 service-boundary 守卫已加入。
- 当前下一项：审计 FileTree 剩余 `services/` 类型导入与职责混合，随后按同一层次规则继续处理 `useActionBar`。
- 已完成：FileTree 和 `fileTreeTypes` 不再从 services 导入类型；agent 数据结构、新项目 meta union 进入 shared，agent/preferences service 保留旧类型导出。字号与快捷键类型也直接从 shared 引用。
- 当前下一项：按 frontend TODO 继续 FileTree 职责拆分并复核硬线；达到验收条件后销项，再处理 `useActionBar` 和 `TaskBoardView` 的 logic/view 倒挂。
- 已完成：将 FileTree header 菜单及账户、Agent config/lifecycle、session preference overlays presenter 提取到 `FileTreeHeaderOverlays.tsx`，状态与动作仍由 FileTree 持有。`FileTree.tsx` 从 701 行降至 489 行；硬线清单从 30 项更新为 29 项。针对性守卫检查 presenter 接线与保留的回调边界。
- 当前下一项：继续审计 FileTree 余下 app/view 依赖并拆分 `useActionBar`、`TaskBoardView`；之后推进 Vitest 迁移与 reuse 测试，再处理 backend TODO。
- 已完成：`hexToRgbaApp` 纯算法迁入 shared，`taskIcons.tsx` 保留旧导出 facade，FileTree 删除最后一个 app/ 导入；frontend TODO 将 FileTree 的逻辑/view 审计单独销项。
- 当前下一项：审计并拆分 `useActionBar` 与 `TaskBoardView` 的 logic/view 倒挂；然后继续 frontend 剩余硬线、Vitest/复用件测试以及 backend TODO。
- 已完成：`useActionBar` 不再从 ModeSelector、TokenEditor、AttachmentsArea 导入类型，也不依赖混合 `styleHelpers`；契约与纯 helper 放 shared，responsive hook 放 hooks；candidate debounce 统一到 shared 并由 appStorage 保留兼容导出。全量逻辑拆分/行数硬线尚待另批处理。
- 已完成：`TaskBoardView` 的响应式读取改用 hooks，任务模板筛选 sentinel 移入 shared 并由 appStorage 兼容再导出。专项测试守住这两项依赖改道；审计发现 view 仍有 type-only app/service 契约和历史 `app/taskIcons` 呈现入口，故未勾掉完整逻辑/view 待办。TaskBoardView 实测 645 行，硬线拆分仍待单独处理。
- 已完成：将 `useActionBar` 的 editor change、candidate、发送/取消、键盘、粘贴与新会话 reset handler 集中到 `useActionBarHandlers`。父 hook 保留状态装配和模型同步，`useActionBar.ts` 从 722 行降到 451 行，新 hook 308 行；handler 时序与回调顺序原样保留。专项边界测试及 typecheck 通过，TODO 硬线清单从 29 项降到 28 项。
- 当前下一项：把 TaskBoardView 的 type-only 契约迁出 app/services 并拆分 645 行视图；继续 Vitest、复用件和 backend TODO。
