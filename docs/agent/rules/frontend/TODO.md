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

## 必拆文件（硬线 33 个，按"每次 2–3 个 + 一次构建"推进）

```
app/ (12): useRealtimeEvents 1003 · useProjectLifecycle 885 · useAppSessionLayer 874
           useAppDomainLayer 861 · useAppStateLayer 816 · useSessionLifecycle 741
           useManagedRoots 709 · useSendMessage 686 · useAppViewComposition 661
           useSessionStreamCache 645 · useActionHandlers 611 · realtimeWsHandlers 570
components/ (14): FileTree 965 · AgentConfigPopover 788 · useActionBar 722 · SessionPane 682
           SessionViewer 646 · TaskBoardView 645 · SessionCard 642 · DefaultListViewMenu 585
           ScheduledAgentTaskDialog 572 · AgentSelector 563 · DocumentViewer 537
           GitStatusPanel 532 · useMarkdownViewer 527 · ProjectAddPopover 526
           TaskInlineEditPanel 516 · Login 513 · TaskCardRows 504
services/ (1): sessionConnectionManager 603
hooks/ (1): useSessionStream 648
```

软线 300–500 共 45 个，记 TODO 触时再拆。

## 职责混淆 6 项

- [ ] `FileTree.tsx`、`useRealtimeEvents.ts`、`useActionBar.ts`、
      `useSendMessage.ts`、`TaskBoardView.tsx`、`useSessionLifecycle.ts`（logic 依赖 view 倒挂）。

## 测试与链路

- [x] 新建 `web/tests/naming-convention.test.mjs`（文件名=主export、`.ts`无JSX、后缀语义）。
- [ ] 前端测试栈切 Vitest（引依赖 + 改链路，独立项）。
- [ ] 存量复用件补测。
