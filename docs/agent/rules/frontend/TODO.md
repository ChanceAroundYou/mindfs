# 前端规范 · 短期待办（非长期规则，分批还）

> 本文件只收"做完即销"的一次性事项。长期约束见同目录各 `*.md`。

## 目录归位

- [x] `services/` 建 `session/` 子目录，收拢根下 ~20 个 `session*.ts`，根留门面。
- [x] 更新簇（`update.ts`/`appUpdate.ts`/`androidUpdate.ts`）、agent 簇
      （`agentConfig.ts`/`agentMemory.ts`/`agents.ts`/`codexRateLimits.ts`）分别归位到域目录，根保留门面。
- [x] 消除 `components/*` 直调 `app/*` hook 倒挂（`useBackNavigation`/`usePwaInstall`
      → `hooks/`，`useActionHandlers` 死 import 清除）。
- [x] `taskIcons.tsx`：补充导出清单头注释，并由针对性测试校验清单与实际导出一致。

## 复用搬迁

- [x] `menuButtonStyle` / `strategyButtonStyle`（task 域）→ 并入 `shared/styles.ts`。
- [x] 三态 JSX（loading/empty/error 结构）→ 抽 `components/common/` 通用三态组件，并用于项目目录、worktree 与定时任务列表。
- [x] `MarkdownViewer` 搬家到 `components/common/`（连同 useMarkdownViewer → `hooks/`、
      MarkdownCodeBlocks/markdownUtils/DiagramPreview → `common/`），8 处 import 改道。

## 命名归一（存量）

- [x] 审核 12 个 `Utils`/`Helpers` 文件名；将含 JSX 的 `sessionCardUtils.tsx` 改为 `sessionCardHelpers.tsx`，其余名称与 JSX 语义匹配。
- [x] 类型文件后缀归一：9 个目标文件原已符合规则；将组件域唯一裸名 `components/action/types.ts` 改为 `actionTypes.ts`，增加命名门禁。`services/<domain>/types.ts` 与 `i18n/types.ts` 保留为域内/跨域入口。
- [x] 样式文件归一：将混合 JSX 图标的 `composerStyles.tsx` 拆为纯 `.ts` 的 `composerStyles.ts` 与 `composerIcons.tsx`；`fileTreeMenuStyles.ts` 原已符合规则。
- [x] `FileEditor.css`、`DiagramPreview.css` 内联到组件的 `<style>` 字符串；保留伪类、媒体查询、CodeMirror 后代选择器和安全区规则。

## 必拆文件（硬线 10 个，按"每次 2–3 个 + 一次构建"推进）

```
app/ (5): useSendMessage 686 · useAppViewComposition 665
           useSessionStreamCache 645 · useActionHandlers 611 · realtimeWsHandlers 570
components/ (3): SessionPane 682 · SessionViewer 646 · useMarkdownViewer 527
services/ (1): sessionConnectionManager 603
hooks/ (1): useSessionStream 648
```

软线 300–500 共 45 个，记 TODO 触时再拆。

## 职责混淆 6 项

- [x] `FileTree.tsx`：降到 489 行；view 与 props 类型不再依赖 services/app，服务查询由 colocated logic hooks 提供。
- [x] `useActionBar.ts`：类型契约迁入 shared；纯展示 helper 归 shared，响应式 hook 归 hooks；logic 不再导入 view 文件。
- [x] `TaskBoardView.tsx`：task/workspace/session 类型契约归 shared，模板控件有独立 view contract，toolbar 已提取，主视图 400 行；任务呈现簇迁至 `components/task/taskIcons.tsx`，app 旧路径仅保留 facade，shared 颜色工具独立导入。专项测试确认 view 不依赖 services/app。
- [x] `useRealtimeEvents.ts`：项目添加流程类型移入 `shared/projectAddTypes.ts`，实时 logic、事件上下文不再依赖 `ProjectAddPopover`。
- [x] `useSendMessage.ts`：plan 前缀纯逻辑移至 `shared/planCommand.ts`，消除 app hook 对 action view 模块的依赖。
- [x] `useSessionLifecycle.ts`：删除未使用的 `SessionViewer` import；当前 hook 不再依赖 view 模块。

## 测试与链路

- [x] 新建 `web/tests/naming-convention.test.mjs`（文件名=主export、`.ts`无JSX、后缀语义）。
- [x] 前端测试栈引入 Vitest：`npm test` 先跑 Vitest 新测试，再跑带 source-map hooks 的 Node 兼容 lane；`deploy-all.sh` 走统一脚本。存量 `.mjs` 按触及时逐步迁。
- [x] 存量复用件补测：新增模型筛选纯函数与通用图标语义测试；盘点结果记录在 active plan。
