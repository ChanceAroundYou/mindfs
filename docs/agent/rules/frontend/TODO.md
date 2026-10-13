# 前端规范 · 短期待办（非长期规则，分批还）

> 本文件只收"做完即销"的一次性事项。长期约束见同目录各 `*.md`。

## 目录归位

- [x] `services/` 建 `session/` 子目录，收拢根下 ~20 个 `session*.ts`，根留门面。
- [ ] 更新簇（`update.ts`/`appUpdate.ts`/`androidUpdate.ts`）、agent 簇
      （`agentConfig.ts`/`agentMemory.ts`/`agents.ts`/`codexRateLimits.ts`）各收一处。
- [x] 消除 `components/*` 直调 `app/*` hook 倒挂（`useBackNavigation`/`usePwaInstall`
      → `hooks/`，`useActionHandlers` 死 import 清除）。
- [ ] `taskIcons.tsx`：清单型头注释，或图标下移。

## 复用搬迁

- [x] `menuButtonStyle` / `strategyButtonStyle`（task 域）→ 并入 `shared/styles.ts`。
- [ ] 三态 JSX（loading/empty/error 结构）→ 抽 `components/common/` 通用三态组件。
- [x] `MarkdownViewer` 搬家到 `components/common/`（连同 useMarkdownViewer → `hooks/`、
      MarkdownCodeBlocks/markdownUtils/DiagramPreview → `common/`），8 处 import 改道。

## 命名归一（存量）

- [ ] `Utils`/`Helpers` 大小写归一 12 个文件。
- [ ] 类型文件后缀归一（`types.ts` / `xxxTypes.ts` / `sessionCacheTypes.ts`）。
- [ ] 样式文件归一（`composerStyles.tsx`、`fileTreeMenuStyles.ts` → `xxxStyles.ts`）。
- [ ] 2 个 `.css` 内联化（`FileEditor.css`、`DiagramPreview.css`）。

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
