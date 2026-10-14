# MindFS 规则与存量待办全量落地 Goal

## Goal prompt

```text
Goal：以当前仓库为准，逐项落实 AGENTS.md、docs/agent/rules/ 和唯一有效计划索引中的所有适用规则，并完成 frontend/backend TODO 里所有仍有效的未完成事项。持续工作到所有事项完成，或某项被证据证明不适用并在待办/计划中说明原因；不要停在审阅、拆分方案或一两个示范改动。

先检查 git 状态、分支、HEAD/upstream 基线、根 AGENTS.md、docs/agent/rules 全部 README/规则/TODO/reviewer-checklist、docs/plans/README.md 与唯一当前计划、相关源文件/消费者/测试脚本。保护开始时已有的用户改动；尤其不要纳入或覆盖未跟踪的 package-lock.json。所有行数、路径、消费者和验收基线以这次仓库实测为准，TODO 中旧行数只作线索。

范围：
1. Frontend TODO：services 的 update/agent 簇归位；loading/empty/error 复用组件；Utils/Helpers 大小写、类型和样式文件命名归一；指定 CSS 内联；所有当前仍超硬线的大文件按职责拆分，行数与清单以本次 `wc -l` 实测为准；6 项 logic→view 倒挂；测试栈迁移 Vitest；存量复用件补测。逐项对照当前 TODO，已完成项不得重复施工。
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
- 历史下一项（后续已更新）：继续 FileTree 拆分，清理剩余 view 对 services 的依赖与菜单组合职责，再按实测行数销减硬线清单。
- 已完成：将 FileTree 设置菜单的呈现 JSX 提取到 `FileTreeSettingsMenu.tsx`；目录排序类型、选项与纯算法迁入 `shared/directorySort.ts`，原 preferences service 保留兼容 facade。`FileTree.tsx` 实测降至 702 行，仍超过硬线，继续拆分；没有提前销项。新增排序纯度、排序语义、facade 与依赖方向测试。
- 历史下一项（后续已更新）：继续拆分 FileTree 的 settings/menu 组合与其余 view 逻辑，按实际行数和依赖方向验证，不改变 App.tsx 组合根。
- 已完成：将 bootstrap readiness 订阅、node groups 查询及项目 tab accent 的节点颜色回退从 FileTree/view presenter 收入 colocated hooks；presenter 改收值，保留原始分组顺序与空态语义。专项 service-boundary 守卫已加入。
- 历史下一项（后续已更新）：审计 FileTree 剩余 `services/` 类型导入与职责混合，随后按同一层次规则继续处理 `useActionBar`。
- 已完成：FileTree 和 `fileTreeTypes` 不再从 services 导入类型；agent 数据结构、新项目 meta union 进入 shared，agent/preferences service 保留旧类型导出。字号与快捷键类型也直接从 shared 引用。
- 历史下一项（后续已更新）：按 frontend TODO 继续 FileTree 职责拆分并复核硬线；达到验收条件后销项，再处理 `useActionBar` 和 `TaskBoardView` 的 logic/view 倒挂。
- 已完成：将 FileTree header 菜单及账户、Agent config/lifecycle、session preference overlays presenter 提取到 `FileTreeHeaderOverlays.tsx`，状态与动作仍由 FileTree 持有。`FileTree.tsx` 从 701 行降至 489 行；硬线清单从 30 项更新为 29 项。针对性守卫检查 presenter 接线与保留的回调边界。
- 历史下一项（后续已更新）：继续审计 FileTree 余下 app/view 依赖并拆分 `useActionBar`、`TaskBoardView`；之后推进 Vitest 迁移与 reuse 测试，再处理 backend TODO。
- 已完成：`hexToRgbaApp` 纯算法迁入 shared，`taskIcons.tsx` 保留旧导出 facade，FileTree 删除最后一个 app/ 导入；frontend TODO 将 FileTree 的逻辑/view 审计单独销项。
- 历史下一项（后续已更新）：审计并拆分 `useActionBar` 与 `TaskBoardView` 的 logic/view 倒挂；然后继续 frontend 剩余硬线、Vitest/复用件测试以及 backend TODO。
- 已完成：`useActionBar` 不再从 ModeSelector、TokenEditor、AttachmentsArea 导入类型，也不依赖混合 `styleHelpers`；契约与纯 helper 放 shared，responsive hook 放 hooks；candidate debounce 统一到 shared 并由 appStorage 保留兼容导出。全量逻辑拆分/行数硬线尚待另批处理。
- 已完成：`TaskBoardView` 的响应式读取改用 hooks；任务、workspace、session 展示类型归入 shared，task template controls 使用 task domain view contract；`appStorage` 与 service 类型入口保留兼容再导出。模板菜单/刷新栏拆到 `TaskBoardToolbar`，主视图从 645 行降至 400 行。专项测试检查依赖入口、调用接线和兼容 facade；TaskBoardView 不导入 services/app 状态或逻辑；2026-10-14 进一步将任务图标与任务样式实现在 `components/task/taskIcons.tsx`，app 旧入口只保留兼容 facade，通用颜色调用直接使用 shared helper。
- 已完成：将 `useActionBar` 的 editor change、candidate、发送/取消、键盘、粘贴与新会话 reset handler 集中到 `useActionBarHandlers`。父 hook 保留状态装配和模型同步，`useActionBar.ts` 从 722 行降到 451 行，新 hook 311 行；handler 时序与回调顺序原样保留。专项边界测试及 typecheck 通过，TODO 硬线清单从 29 项降到 28 项。
- 当前下一项：继续将存量 Node 测试逐批迁入 Vitest；随后处理 backend TODO。

- 已完成：审计 `app/taskIcons` 的 13 个消费者与全部导出，将呈现实现迁入 `components/task/taskIcons.tsx`，更新 task/workspace/shell 调用方；`app/taskIcons.tsx` 仅保留兼容 re-export，默认节点色改从 shared 读取，session/file 等通用颜色消费者直接依赖 `shared/hexToRgba`。更新导出清单测试、依赖方向测试和 upstream 登记，TaskBoardView 逻辑/view 待办销项。

- 已完成：把 Vitest 2.1.9 接入 `web/package.json` 与锁文件；`npm test` 先跑 Vitest，再跑带 source-map/TS preload hook 的 120 个旧 Node 测试文件。`deploy-all.sh` 改用统一 `npm test`，ship 文档同步。新增 Vitest 复用件测试，覆盖 `filterAgentModels` 的查询语义与 shared 层纯度、ExitIcon/NoWorktreeIcon 的装饰语义和尺寸。当前 Vitest 5 项通过；旧 Node lane 263 项通过、4 项跳过。旧测试按触及时迁移，未伪称已全量转换。
- 已完成：将定时 Agent 列表 JSX 从 `ScheduledAgentTaskDialog` 提取到 `ScheduledAgentTaskList`，其余表单状态留在 dialog；`ScheduledAgentTask` 类型迁入 shared 并由 service 兼容再导出。将任务卡 worktree 收尾按钮抽到 `TaskCardFinishButton`，动作类型归 task domain。两父组件现分别 370 与 470 行，frontend 硬线清单从 27 减至 25；更新源码逻辑映射和跟随职责边界的源码守卫。`npm run typecheck`、专项测试及全量 `npm test` 通过（旧 Node lane 263 通过、4 跳过；Vitest 5 通过）。
- 下一项：从剩余硬线文件选取 2–3 个，先核对外部消费者、符号和依赖方向，再拆分并验证；持续保持 App.tsx 组合根不变。
- 已完成：将 `ProjectAddPopover` 的模式选择 JSX 提取为 `ProjectAddModePanel`，popover 外观进入同域 `projectAddStyles.ts`，父组件从 509 行降到 404 行。更新逻辑源映射并增加模式回调/标签守卫；TypeScript、项目添加专项测试及规则/计划链接门禁通过。硬线剩余 24 项。
- 下一项：实测并审阅下一组超过 500 行的前端组件，核对调用者与依赖边界后继续拆分。
- 已完成：将 `AgentSelector` 的触发按钮与小徽标展示提取为 `AgentSelectorTrigger`，打开/关闭与 submenu 初始化状态仍由 hook/controller 管理；旋转 keyframes 移到使用它的错误横幅。`AgentSelector.tsx` 从 563 行降至 496 行，新增结构守卫；typecheck 与 AgentSelector 专项测试通过。硬线剩余 23 项。
- 下一项：从余下 23 项中根据当前代码审阅挑选一组职责清楚、风险可控的组件继续拆分。
- 已完成：将 `TaskInlineEditPanel` 中的候选列表呈现提取为 `TaskInlineCandidateMenu`，候选查找/选择状态与输入/编辑行为仍由父面板和 app hook 持有。父面板从 516 行降至 473 行；TypeScript、候选菜单、task 面板接线与 worktree 节点作用域专项测试通过。硬线剩余 22 项。
- 下一项：继续按当前代码审阅剩余前端硬线文件，并开始复核后台 TODO 的真实行数基线。
- 已完成：将 Login 更新说明折叠区提取为 `LoginUpdateNotes`；show/hide 状态、版本更新动作与数据仍由 Login/useLogin 持有。Login 从 513 行降至 463 行，source-map 与切换/文案守卫同步更新；TypeScript、专项和治理链接测试通过。硬线剩余 21 项。
- 下一项：继续前端硬线拆分，并在进入 backend TODO 前按当前树重算后端文件清单及模块边界。
- 已完成：将 `GitStatusPanel` 的分支按钮和分支列表提取到 `GitBranchSelector`；分支列表查询仍在现有 hook，切换 busy/open 状态由父面板持有。父组件从 532 行降至 434 行；TypeScript、Git 分支专项和 commit IME 测试通过。硬线剩余 20 项。
- 下一项：继续剩余前端硬线文件；完成前端事项后，按当前文件系统基线复核 backend TODO。
- 已完成：将 PDF 页面虚拟化渲染及文档预览错误呈现/描述提取到 `PdfPreview`、`DocumentPreviewError`；`DocumentViewer` 保留格式分流、文件获取和 Word/Excel/PowerPoint 预览。原有视口范围、canvas 回收、DPR 上限和错误文案逻辑保持不变。父组件从 537 行降至 318 行，typecheck 与 PDF 专项通过；硬线剩余 19 项。
- 下一项：继续剩余前端硬线，随后按当前实测重算后台文件清单并开始后端单文件拆分。
- 已完成：将 `SessionCard` 的溢出操作入口与菜单抽到 `SessionCardOverflowMenu`；置顶、同步、重命名、归档、删除动作通过父卡片回调，卡片/hook 仍持有 menu、rename 状态。父组件从 642 行降至 455 行；typecheck、session archive、rename 与专项菜单测试通过。硬线剩余 18 项。
- 下一项：继续前端硬线组件拆分；接近清空 frontend TODO 后复核目录/命名/层级规则与 reviewer checklist。
- 已完成：将 `DefaultListViewMenu` 的排序展开按钮与 inherit/显式排序选项提取到 `DirectorySortMenu`，sort 值和弹层开关仍由原父组件持有；`DirectorySortControlValue` 随菜单 view contract 导出。父菜单从 585 行降至 457 行；typecheck、命名门禁及排序专项通过。硬线剩余 17 项。
- 下一项：按当前文件树继续处理前端硬线；后续复核候选名单行数与 reviewer checklist 的门禁覆盖。
- 已完成：将混在 `AgentConfigPopover.tsx` 中的生命周期面板拆到 `AgentLifecyclePopover.tsx`，相关表单样式集中到 `agentConfigStyles.ts`；旧 `AgentConfigPopover` 路径继续导出 lifecycle 组件和 FileTree 使用的三项样式。Lifecycle 面板 167 行，主流程降到 528 行但仍高于 500，未从 TODO 销项；typecheck、header overlay 与兼容出口专项通过。
- 下一项：从 AgentConfigPopover 提取 agent 选择步骤，完成到 500 行以下后再销项；同时守住 FileTree 的旧 facade。
- 已完成：将 agent 选择步骤从 `AgentConfigPopover` 拆为 `AgentConfigAgentStep`，保留 agent 选择、最近配置提示、restart 按钮和 loading/empty 状态；`AgentStatus`、provider 与 backup 类型改从 shared 导入，view 不再直接 import service 类型。`AgentConfigPopover` 降到 450 行，前端硬线剩余 16 项；typecheck、命名、FileTree overlay 与 agent step 专项通过。
- 下一项：从当前前端硬线列表选择下一批易于界定且能减少父职责的目标；之后完成 reviewer-checklist 自动门禁并开始 backend 单文件拆分。
- 当前进度（2026-10-14）：将 `useProjectLifecycle.tsx` 中 worktree 创建/切换 overlay JSX 提取到 `WorktreeLifecycleOverlays`；hook 保持状态、加载与动作，并保留创建/切换两个既有 render 出口。逻辑映射与专项结构守卫同步更新，父 hook 从 885 行降至 731 行，仍在硬线清单内。TypeScript 与专项映射/命名/overlay 测试通过。
- 当前进度（2026-10-14）：目录浏览状态、路径推导、按节点读取目录、选择与添加项目动作归 `useProjectDirectoryBrowser`；浏览类型移入 shared，旧 view 类型出口兼容保留。`useProjectLifecycle.tsx` 降至 571 行，仍在硬线内；TypeScript 与项目添加/路径、layering 类型、命名和 source-map 专项测试通过。
- 历史下一项（后续已完成）：将项目添加 popover 的呈现组合从 `useProjectLifecycle` 移出，达到 500 行以下后销项；不改 App 组合根。
- 已完成：项目添加入口/方式选择/本地目录打开/GitHub 导入动作归 `useProjectAddActions`，popover 组合归同域 `ProjectAddLifecycleOverlay`；目录浏览和 worktree overlay 已在前两批拆出。`useProjectLifecycle.tsx` 从 571 降至 482 行，硬线从 16 减至 15；App 组合层出口与项目添加状态顺序保持不变。
- 已完成：`UseAppSessionLayerParams` 独立到 `useAppSessionLayerTypes.ts`；共享 session key/pending/lifecycle/metadata/multi-project/stream-cache 编排归 `useAppSessionCore`，多项目 300ms reload 与 replying poll 在原调用位置归 `useAppMultiProjectReload`，保留 hook 顺序。父层实测从 876 降到 474 行，硬线从 15 减至 14；新增结构守卫并通过 typecheck 和专项 source-map/命名检查。
- 已完成：`UseAppDomainLayerParams` 移到 `useAppDomainLayerTypes.ts`，原模块保留类型 facade；连续的 root 管理、action handler、bound-session 导航、文件导航、项目删除与项目/worktree 生命周期装配归 `useAppProjectManagement`，调用顺序维持不变。父层降到 437 行，硬线从 14 减至 13；typecheck、source-map、命名和专项边界测试通过。
- 下一项：重新实测并审阅 `useAppStateLayer` 的消费者、状态所有权和职责边界，再选定单一子职责继续拆分。
- 当前进度：`useAppStateLayer` 中命令式 refs 与 session cache 初始化移到 `useAppStateRefs`，并在原 hook 序列位置调用，保留 refs 声明及 cache hook 的顺序。父层实测 816→762 行，仍超过硬线；专项结构守卫、命名门禁和 typecheck 通过。TODO 行数已同步，未提前销项。
- 当前进度：跨节点 root 选择回调与清理逻辑移到 `useAppRootSelection`，父层仍在原位置创建回调；timer 与 Git 状态通过惰性 getter 保持原声明时序，既有节点路由断言改为检查实际逻辑模块。父层实测进一步从 762 降至 714 行；typecheck、全量 web 测试（279 通过、4 跳过）与专项结构测试通过。
- 当前进度：将 `editDraftRequest` 到 update 状态之间连续的文件/工作区状态、Git hook 与 project-tree hook 整体迁入 `useAppFileWorkspaceState`；新 hook 在原调用位置运行，Ref 镜像赋值、localStorage 初始化与 `refreshTaskWorktreeRef` 转发顺序保持。父层从 714 降至 588 行，新增专项守卫。初次全量测试发现 source-map 别名仍指向未合并的父文件，已补齐逻辑模块映射并修正物理行数断言；最终全量 web 测试通过（280 通过、4 跳过），typecheck 与专项测试通过。
- 发布复核发现：source-map alias 测试适配的最后两处物理文件读取未包含在 G-DG 提交，导致 ship 二进制版本标记为 dirty。相关改动仅是测试读取路径，现单独补充登记并提交，然后重新走干净构建和无重启发布确认。
- 当前进度：将会话列表/pending/归档状态、会话选择与 drawer 状态、响应式侧栏/onboarding 状态、root scope 索引与 task template/candidate 初始化分别迁至 `useAppSessionListState`、`useAppSessionSelectionState`、`useAppResponsiveState`、`useAppRootScopeState`、`useAppTaskTemplateState`。各 hook 在原调用位置执行；常量字面量、pin hook、session template 加载和 candidate effect 顺序保持。`useAppStateLayer` 实测 588→498 行，已从硬线清单销项；typecheck、针对性测试与全量 web 测试通过（281 通过、4 跳过）。
- 已完成（2026-10-14）：审阅 `useSessionLifecycle` 的唯一消费者 `useAppSessionCore` 与各 handler 契约；把手动同步（含瞬时尾部保留/去重）抽到 `useSessionSyncAction`，把会话列表头同步、列表分页缓存回填和子会话分页抽到 `useSessionListLoading`。两个 hook 在原组合顺序处运行，App.tsx 未改；生命周期父 hook 实测 740→404 行，硬线销项。TypeScript 与相关专项通过；全量测试首轮发现旧 source-map 测试仍把外部导入刷新与子会话加载当成同一来源，修正读源后全量 web 测试通过（Vitest 5 + Node 兼容链路 282 通过、4 跳过）。
- 已完成（2026-10-14）：审阅 `useManagedRoots` 的 4 个直接消费者及 `useAppProjectManagement` 主组合入口；把近 170 行 root id/session/cache/tree key remap 抽到 `useManagedRootRename`，参数契约移至 `useManagedRootsTypes`。`applyManagedRootRename` 在原回调顺序处创建，ID/显示名分支与缓存键映射保持；父 hook 实测 709→480 行，硬线销项。App.tsx 不变；TypeScript 与专项测试通过；全量 web 测试通过（Vitest 5 + Node 兼容链路 283 通过、4 跳过）。
- 已完成（2026-10-14）：复核 `useSendMessage` 的唯一消费者 `useAppSessionLayer`；会话目标解析/选项归一和 `/status`、`/login` 瞬时命令分别归 `prepareSendMessageTarget` 与 `useSendMessageSlashCommand`，参数契约移到 `useSendMessageTypes` 并由旧模块兼容再导出。父 hook 保留乐观消息、上下文/插件处理、发送和失败回滚顺序，实测 686→422 行；`App.tsx` 不变。专项与 TypeScript 通过；全量 web 测试通过（Vitest 5 + Node 兼容链路 284 通过、4 跳过）。
- 已完成：`UseAppViewCompositionParams` 独立到 `useAppViewCompositionTypes.ts`，工作台筛选/折叠状态、live task 派生、跨节点 board hook 与 refresh ref 桥接归 `useAppWorkspaceBoardComposition`；原 hook 保留组合顺序，工作台刷新仍在原调用点装配，`App.tsx` 不变。父 hook 实测 665→433 行，硬线剩余 8 项；300–500 行软线实测 72 项。TypeScript、freshness/board 及 source-map 专项通过。
- 历史下一项（后续已更新）：继续拆 `useSessionStreamCache` 等前端硬线，再全量完成规则门禁审计和 backend TODO。

- 已完成：`useSessionStreamCache` 的通用 cache upsert、运行时元信息解析及 agent chunk 身份合并归 `useSessionStreamRuntimeMeta`；`SessionRuntimeMeta` 独立到 type contract，原模块保留类型出口。父 hook 实测 645→473 行，硬线剩余 7 项、软线 73 项；其他 exchange merge 与 context-window 更新留在父层。专项 20 项通过、TypeScript 通过，source-map 映射与 upstream 登记完成。
- 已完成：`useActionHandlers` 的 open_dir 目录加载流程归 `openDirectoryAction` 工厂，仍在父 hook 的 useMemo 对象中按原时机创建；完整参数契约和闭包引用、节点/根选择、树缓存、bound-session 恢复及失败状态均保持。父 hook 实测 611→409 行，硬线剩余 6 项、软线 74 项；TypeScript、全量测试（Vitest 5 + legacy 287 通过/4 跳过）与生产构建通过。
- 已完成：`task.updated`、`task.finish_teardown`、`task.deleted` 事件处理归 `realtimeWsTaskHandlers`，在 session.user_message 与 file.changed 处理之间按原事件表位置 spread 注册；task 事件相关 context 字段从父工厂移除。父 hook 实测 570→426 行，硬线剩余 5 项、软线 75 项。TypeScript、全量测试（Vitest 5 + legacy 288 通过/4 跳过）与生产构建通过；task/worktree/source-map 专项守卫通过。
- 已完成：`SessionPane` 主内容状态分流与文件/插件/目录视图组合迁入 `SessionMainContent`，插件信任内容迁入 `PendingPluginTrustView`，属性契约移入 `sessionPaneTypes` 并由原组件路径兼容再导出。父壳 682→58 行，内容组合 458 行，硬线剩余 4 项、软线 76 项；视图显隐与优先级不变。TypeScript、全量测试（Vitest 5 + legacy 289 通过/4 跳过）与生产构建通过。
- 已完成：`SessionViewer` 的跟随滚动状态/回调与滚动位置、视口适配和 stickiness effects 归 `useSessionViewerScroll`，timeline user-index/timestamp 元数据归线性 helper；动画 keyframes 收到 session viewer styles。helper hook 在原状态/效果调用顺序位置运行，物理父组件 646→499 行，硬线剩余 3 项、软线 77 项。行为专项、TypeScript、全量测试（Vitest 5 + legacy 290 通过/4 跳过）与生产构建通过。
- 已完成：受保护图片解析与 blob/object URL 生命周期归 `MarkdownImage`，`useMarkdownViewer` 保留解析、outline、渲染和链接导航编排，hook API 不变。父 hook 实测 527→445 行，硬线剩余 2 项、软线 78 项；TypeScript、全量测试（Vitest 5 + legacy 291 通过/4 跳过）、命名/source-map 专项与生产构建通过。
- 已完成：WS 出站命令构造与发送归 `SessionConnectionOutbound`，manager 在旧 API 上保留薄代理；socket-open 守卫仍先于 context 压缩/pending 写入，pending ack 与 reconnect resend 留在 manager。父类实测 603→472 行，前端硬线剩余 1 项、软线 79 项；TypeScript、全量测试（Vitest 5 + legacy 292 通过/4 跳过）、生产构建及 upstream 门禁（1019 delta / 124 组）通过，提交 `7f32c1e` 已推送并 no-restart 安装。
- 进行中：按正式 500 行硬线继续细拆 `useSessionStream`：React stream 状态订阅与定时器留在原 hook；时间线投影归 `sessionTimeline.ts`（332 行），assistant exchange 投影归 `sessionAssistantTimeline.ts`（184 行），稳定 ID 由 `stableTimelineID.ts` 提供。父 hook 648→128 行；专项测试、typecheck、全量测试（Vitest 5 + legacy 293 通过/4 跳过）和生产构建通过；upstream 门禁（1023 delta / 125 组）通过；提交 `db8d19b`、`c0b87b8` 已推送并 no-restart 安装。
- 已完成（2026-10-14）：继续将 Claude subagent 路由（`session_subagents.go`，315 行）、AnswerQuestion/stream aux（`session_stream_events.go`，202 行）、agent event/path 投影（`session_agent_events.go`，209 行）和 model validation（`session_model_validation.go`，136 行）按职责拆出。`session.go` 从 2,276 降至 1,455 行，低于 1,500 行硬线；取消过期判断注释保留在原方法旁。相关 usecase 测试、全量 `make test`、upstream 门禁（1036 delta / 137 组）与 no-restart ship 均通过；提交 `48f7b8b`，三端安装 `v0.5.5-786-g48f7b8b`，无节点重启。
- 已完成（2026-10-14）：`manager.go` 搜索入口与两个搜索测试文件已审阅；`Search`、搜索打分/排序、块流和旧 JSONL 搜索及 snippet helper 原样迁至 `manager_search.go`（322 行）。私有测试调用保持同包，无消费者/import 变更；session 包专项与全仓 `make test` 通过。父文件从 3,905 降至 3,593 行。upstream 门禁通过（1037 delta / 138 组）；`6224281` 已推送，no-restart ship 完成，三端安装 `v0.5.5-787-g6224281`，bundle 对账一致且未重启节点。
- 已完成（2026-10-14）：主 exchange 增量读取路径及 JSONL 解析、按 seq 合并、游标推进原样迁至 `manager_exchanges.go`（315 行），连同 `exchangeFileCursor` 类型；相关 session 测试、全仓 `make test` 与 upstream 门禁通过（1038 delta / 139 组）。父文件从 3,593 降至 3,289 行；G-BG 的迁移锚点同步修正。提交 `0bbe846` 已推送，no-restart ship 完成，三端安装 `v0.5.5-788-g0bbe846`，bundle 对账一致且未重启节点。
- 已完成（2026-10-14）：aux 增量读取、窗口尾部扫描与全量 fallback 原样迁至 `manager_aux_reads.go`（274 行）；session 包专项与全仓 `make test` 通过，upstream 门禁通过（1039 delta / 140 组），并同步更新 G-AT 实现路径锚点。父文件从 3,289 降至 3,026 行；提交 `4efe558` 已推送，no-restart ship 完成，三端安装 `v0.5.5-789-g4efe558`，bundle 对账一致且未重启节点。
- 已完成（2026-10-14）：`GetCommandShell` 有界尾读、分组和行边界安全 helper 原样迁至 `manager_aux_tail.go`（120 行），session 包/全仓测试及 upstream 门禁（1040 delta / 141 组）通过；G-BG 锚点同步迁移。提交 `9befc49` 已推送，no-restart ship 完成，三端安装 `v0.5.5-790-g9befc49`，bundle 对账一致且未重启节点。
- 已完成（2026-10-14）：`GetFullToolCall`、pending tool-call upsert/answer/clear、tool-call merge 与 clone helper 原样迁至 `manager_pending_aux.go`（192 行）；全仓 `make test` 与 upstream 门禁（1041 delta / 142 组）通过。提交 `af7a400` 已推送，no-restart ship 完成，三端安装 `v0.5.5-791-gaf7a400`，bundle 对账一致且未重启节点。
- 已完成（2026-10-14）：aux JSONL append 与路径校验 helper 原样并入 `manager_aux_reads.go`，相关全仓 `make test` 与 upstream 门禁（1041 delta / 143 组）通过。提交 `f84c304` 已推送，no-restart ship 完成，三端安装 `v0.5.5-792-gf84c304`，bundle 对账一致且未重启节点。
- 已完成（2026-10-14）：agent state/binding upsert、cursor 查询/更新、binding 查找/确保及 SQL scan/upsert helper 迁至 `manager_agent_bindings.go`（258 行）。session 包及全仓 `make test` 通过；upstream 门禁通过（1042 delta / 144 组），G-Q 对空 id 语义的锚点迁至新模块。提交 `4cea8dc` 已推送，no-restart ship 完成，三端安装 `v0.5.5-793-g4cea8dc`，bundle 对账一致且未重启节点。
- 已完成（2026-10-14）：SQLite session DB 初始化/打开、schema migration、历史 alias/fork-link 归一和 DB link helper 迁至 `manager_sqlite.go`（297 行）；session 包及全仓测试通过。提交 `c161357` 已推送，upstream 门禁通过（1043 delta / 145 组），no-restart ship 完成，三端安装 `v0.5.5-794-gc161357`，bundle 对账一致且未重启节点。
- 已完成（2026-10-14）：session 列表/计数/最新活动时间 API、meta 查询、binding 填充及 SQL 条件构造迁至 `manager_session_list.go`（258 行）。Search 调用仍走同包方法；session 包及全仓测试通过，G-BJ 锚点迁到新模块。提交 `62a93fd` 已推送，upstream 门禁通过（1044 delta / 146 组），no-restart ship 完成，三端安装 `v0.5.5-795-g62a93fd`，bundle 对账一致且未重启节点。
- 已完成（2026-10-14）：session alias 与 external-name 持久化、identity 查找和单条/批量 alias 回填迁至 `manager_aliases.go`（188 行）；全仓测试与 upstream 门禁（1045 delta / 147 组）通过。提交 `508d47f` 已推送，no-restart ship 完成，三端安装 `v0.5.5-796-g508d47f`，bundle 对账一致且未重启节点。
- 已完成（2026-10-14）：meta 单行读取、binding 合并、upsert 参数序列化、row scan、归一化与共享 SQL row scanner 迁至 `manager_meta.go`（264 行）；全仓 `make test`、upstream 门禁（1046 delta / 148 组）与 no-restart ship 均通过，提交 `3cf40a1` 已推送并安装至三端（`v0.5.5-797-g3cf40a1`），未重启节点。
- 已完成（2026-10-14）：session related-file 增删、输出文件记录与身份/路径归一 helper 迁至 `manager_related_files.go`（156 行）；`RecordRelatedWorktree`/`ClearRelatedWorktree` 保留原位，不改 repoint/worktree 生命周期。全仓 `make test`、upstream 门禁（1047 delta / 149 组）与 no-restart ship 均通过，提交 `d9f8209` 已推送并安装至三端（`v0.5.5-798-gd9f8209`），未重启节点。
- 已完成（2026-10-14）：exchange turn identity/source/model/token-usage context contract、确定性 MindFS ID helper 与形状校验迁至 `manager_exchange_identity.go`；保留公开 API 和行写入/身份分配时序。全仓 `make test`、upstream 门禁（1048 delta / 150 组）及 no-restart ship 均通过，提交 `f9778ec` 已推送并安装至三端（`v0.5.5-799-gf9778ec`），未重启节点。
- 已完成（2026-10-14）：session Close/Delete/Rename/Archive、session 属性更新、内部 close/delete、DB shutdown 生命周期实现迁至 `manager_lifecycle.go`（256 行）；worktree 归属和 repoint 方法未改。全仓 `make test`、upstream 门禁（1049 delta / 151 组）与 no-restart ship 均通过，提交 `6239722` 已推送并安装至三端（`v0.5.5-800-g6239722`），未重启节点。
- 已完成（2026-10-14）：session window orchestration、冷缓存尾读、字节级记录计数、尾部解析与 seq lower-bound helper 迁至 `manager_window_reads.go`（193 行）；同时修正了长期 source-map 测试中已过期的 SessionViewer、session service、App 与 realtime 模块边界断言。`make test`、ship 门禁（Go、tsc、Vitest 5 + legacy 293）及 upstream 覆盖（1050 delta / 152 组）通过；提交 `dc72506` 已推送，三端安装 `v0.5.5-801-gdc72506`，未重启。manager 主文件实测 749 行。
- 历史阶段记录：G-EX/G-EY/G-EZ 的早期拆分进度与行数基线已由下方验收记录取代；当前必拆清单以本计划末尾的最新实测为准。

- 已完成（2026-10-14）：G-EX 的 `http_git.go` 已通过全仓 Go/TypeScript/web 与 upstream 门禁并 no-restart ship（`2def72a`，三端 `v0.5.5-802-g2def72a`，未重启）。
- 进行中（2026-10-14）：项目文件树/文件操作、上传、目录增删改与 managed-dir response 迁至 `http_files.go`（574 行）；路由和 usecase 调用不变，API 包专项通过。父文件实测 2,036 行，仍高于 1,500 硬线；全仓门禁与发布待执行。
- 下一项：完成 G-EY 后继续将 session handler（规避 repoint handler）、静态资源服务和偏好/项目入口按域拆到父文件低于硬线；再处理其他 6 个大文件、双入口审计与 reviewer checklist 门禁。


- 已完成（2026-10-14）：G-EY 文件/目录 handler 已通过全仓 Go、tsc、Vitest 5 + legacy 293 与 upstream 门禁（1052 delta / 154 组），提交 `9522fd5` 已推送并 no-restart 安装至三端（`v0.5.5-803-g9522fd5`），bundle 对账一致、无节点重启。
- 进行中（2026-10-14）：session 列表/详情/同步/搜索/导入/rename/archive/delete 等 handlers 与 response/shell projection 迁至 `http_sessions.go`（1184 行）；`handleSessionRepoint` 留在 `http.go` 且未改。API 包专项及 session-window、conditional-response、user-message timing web 守卫通过；`http.go` 实测 867 行。全仓门禁、upstream 与 no-restart ship 待执行。
- 下一项：完成 G-EZ 后继续拆 `http.go` 剩余偏好、静态资源、local-dir/import 处理，使主文件成为 route/middleware facade；随后按当前大文件清单处理其余 6 个文件与 cli/server 双入口审计、reviewer checklist 门禁。


- 已完成（2026-10-14）：G-EZ session handler 拆分通过全仓 Go、tsc、Vitest 5 + legacy 293 与 upstream 覆盖门禁（1053 delta / 155 组）；提交 `92ce95f` 已推送并 no-restart 安装至三端（`v0.5.5-804-g92ce95f`），未重启节点。
- 已完成（2026-10-14）：静态资源、index 缺失资源检测、fallback/cache/version/path helpers 迁至 `http_frontend.go`；API 包和 worker-role tests、全仓 Go、TypeScript、Vitest 5 + legacy 293 及 upstream（1054 delta / 156 组）通过。提交 `238939b` 已推送，三端 no-restart 安装 `v0.5.5-805-g238939b`，bundle 对账一致、无重启；`http.go` 实测 658 行。
- 已完成（2026-10-14）：Claude model alias/env/settings/model-list 归一职责迁至 `agent/claude/session_models.go`（406 行）；全仓 `make test`、ship Go/tsc/web 门禁（Vitest 5 + legacy 293）、upstream 覆盖（1055 delta / 157 组）通过。提交 `4eb12a3` 已推送并 no-restart 安装三端 `v0.5.5-806-g4eb12a3`，bundle 对账一致、未重启。

- 已完成（2026-10-14）：G-FC Claude tool-call summary projection 已通过全仓 `make test`、ship Go/tsc/web 门禁（Vitest 5 + legacy 293）和 upstream 覆盖（1056 delta / 158 组）。提交 `b4c44f6` 已推送并 no-restart 安装三端 `v0.5.5-807-gb4c44f6`，bundle 对账一致，未重启。
- 已完成（2026-10-14）：G-FD Claude pending tool-call 生命周期、结果归一及工具结果摘要拆分通过全仓 `make test`、ship Go/tsc/web（Vitest 5 + legacy 293）和 upstream 门禁（1057 delta / 159 组）。提交 `ae4a0b1` 已推送并 no-restart 安装三端 `v0.5.5-808-gae4a0b1`，bundle 对账一致，未重启。
- 已完成（2026-10-14）：G-FE Claude SDK message handlers、ask-user 交互、subagent/task 状态投影迁至 `agent/claude/session_messages.go`（684 行）；事件调用顺序和 session 状态语义保持。Claude package tests、全仓 `make test`、ship Go/tsc/web（Vitest 5 + legacy 293）和 upstream 门禁（1058 delta / 160 组）均通过。提交 `244c5cb` 已推送并 no-restart 安装三端 `v0.5.5-809-g244c5cb`，bundle 对账一致，未重启。
- 已完成（2026-10-14）：G-FG agent API provider JSON/YAML 文件读写、JSON-like 归一、模型配置结构和 managed-block/sequence helpers 迁至 `agent_api_provider_files.go`（449 行）；与 G-GH 一起经全仓 `make test`、ship Go/tsc/web（Vitest 5 + legacy 293）与 upstream 门禁（1060 delta / 162 组）验证。提交 `cb68135`、`c188e37` 已推送，no-restart ship 安装三端 `v0.5.5-811-gc188e37`，bundle 对账一致，未重启。
- 已完成（2026-10-14）：G-GH agent API provider 的通用 apply orchestration、Codex TOML 合并和 Gemini/Qwen/Kimi/OpenCode/OpenClaw/OMP/Pi/Hermes agent config adapters 迁至 `agent_api_provider_apply.go`（769 行）；provider 选择、配置写入顺序和 agent-specific env 语义保持。父文件实测 1,291 行，低于硬线。提交 `c188e37` 已推送，安装事实同 G-FG。
- 已完成（2026-10-14）：G-GI kanban task stage execution、agent stage dispatch、prompt variables 与 run completion/error persistence 迁至 `service_execution.go`（360 行）；执行顺序、stage status/event 和失败记录语义保持，worktree ownership/repoint 方法未动。kanban 包/全仓 Go、ship Go/tsc/web 与 upstream 门禁（1061 delta / 163 组）通过。提交 `115d7a2` 已推送并 no-restart 安装 `v0.5.5-812-g115d7a2`，bundle 对账一致，未重启。
- 已完成（2026-10-14）：G-GJ kanban template CRUD、task creation/list/detail/overview read paths 迁至 `service_tasks.go`（228 行）；template snapshot、任务编号、overview skip-on-error 和排序语义保持。更新后的 Go、ship Go/tsc/web（Vitest 5 + legacy 293）与 upstream 覆盖门禁（1062 delta / 164 组）通过。提交 `156cff8` 已推送并 no-restart 安装三端 `v0.5.5-813-g156cff8`，bundle 对账一致，未重启；两个 legacy source guard 已同步改为检查 facade + task/query 模块。
- 已完成（2026-10-14）：G-GK Codex importer 的 tool-call/input/output 解析、patch 与 wrapped JavaScript 处理迁至 `importer_tool_calls.go`（850 行）；同包函数契约和导入 exchange 输出保持。Codex 包/全仓 Go、ship Go/tsc/web（Vitest 5 + legacy 293）与 upstream 覆盖门禁（1063 delta / 165 组）通过。提交 `2d54da6` 已推送并 no-restart 安装三端 `v0.5.5-814-g2d54da6`，bundle 对账一致、未重启。
- 已完成（2026-10-14）：G-GL Codex session tool item mapping、dynamic/hook/image/collaboration projection 与 tool content/title helpers 迁至 `session_tools.go`（433 行）；调用者和 event shape 保持。Codex 包/全仓 Go、ship Go/tsc/web（Vitest 5 + legacy 293）与 upstream 覆盖门禁（1064 delta / 166 组）通过。提交 `44e643d` 已推送并 no-restart 安装三端 `v0.5.5-815-g44e643d`，bundle 对账一致、未重启。
- 已完成（2026-10-14）：G-GM gitview diff/history/related-file diff 与 batch stat 读取、commit/name-status/numstat/history helpers 迁至 `gitview_history.go`（653 行）；路径映射、历史排序、diff 选择和输出 shape 保持。全仓 Go、ship Go/tsc/web（Vitest 5 + legacy 293）和 upstream 门禁（1065 delta / 167 组）通过。提交 `cafa9e2` 已推送并 no-restart 安装三端 `v0.5.5-816-gcafa9e2`，bundle 对账一致，未重启；所有必拆 Go 文件当前均低于硬线。
- 已完成（2026-10-14）：发布入口与开发入口已核对：`make build` 和 `scripts/build-all.sh` 使用 `./cli/cmd`，`make dev-backend` 使用 `./server/cmd/mindfs-server`；新增 `TestReleaseAndDevelopmentEntrypointsStaySeparated` 锁定入口边界，缺省 startup config 已由既有测试守住。
- 已完成（2026-10-14）：G-GN 双入口审计增加测试守卫；`make test`、upstream（1065 delta / 168 组）及 ship Go/tsc/web（Vitest 5 + legacy 293）通过。提交 `6bc3d6c` 已推送并 no-restart 安装三端 `v0.5.5-817-g6bc3d6c`，bundle 一致、未重启。
- 已完成（2026-10-14）：G-GO 落实 WS 单读 goroutine 规则：`handleWSRequest` 的 ping、plan-mode 与 queue handlers 改为 goroutine 派发；AST 测试锁定全部 handler。全仓 Go、ship Go/tsc/web（Vitest 5 + legacy 293）、upstream（1065 delta / 169 组）通过。提交 `bd0bd3a` 已推送并 no-restart 安装三端 `v0.5.5-818-gbd0bd3a`，bundle 一致，未重启。
- 已完成（2026-10-14）：G-GP 新增规则入口、Active plan 唯一性与本地 Markdown 链接门禁；同时核对 `CLAUDE.md`/`GEMINI.md` 均导入 `AGENTS.md` 及共用索引。G-GP/G-GQ 全仓 Go、tsc、web（Vitest 5 + legacy 298）和 upstream 门禁（1068 delta / 171 组）通过；提交 `d1c3d90`、`ddf5690` 已推送并 no-restart 安装三端 `v0.5.5-820-gddf5690`，bundle 一致、未重启。
- 进行中（2026-10-14）：审阅发现 frontend layering 长期规则仍有 121 条 `components/**/*.tsx -> services/` import 边、涉及 69 个文件（包含 type-only import）；`reviewer-architecture-rules.test.mjs` 以只减不增 manifest 禁止新增，并守住 500 行硬线。后续按域迁移、删减 manifest。
- 进行中（2026-10-14）：补 reviewer checklist 自动覆盖映射；backend 已有 WS、Cancel timeout、AppContext、endpoint role 和文件长度测试；本批为 AppContext 使用面与生产 Go 文件硬线新增 guard。SQLite additive、回滚说明、人工架构判断仍由 reviewer checklist 标明。
