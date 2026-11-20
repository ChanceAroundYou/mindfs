# 任务侧输入框：@ 候选与粘贴截图（看齐会话输入框）

Plan 2026-11-20，用户直接提出：「创建任务和任务详情页的输入框需要加上 @ 和复制图片功能，看齐会话输入框」。

## 目标

两个任务侧输入框补上会话输入框已有的两项能力：

1. `@` 文件 / `#` 提示词 / `/` 命令候选：输入触发词拉候选，↑↓ 高亮、Tab/Enter 选中、Esc 关闭；
2. 粘贴（截图 / 复制图片）直接进附件条，与「+」按钮同一条上传链路；只有附件、没有正文时也可发送。

## 现状差异（改前）

- 新建任务面板（`TaskInlineEditPanel`）已有候选菜单与候选拉取，但 `PromptEditor` 不向外暴露「触发词变化 / 键盘 / 粘贴」三件事，输入 `@` 只能看到文字，候选菜单永远不出现；粘贴图片完全没接。
- 任务详情输入框（`ActivityInput`）只有纯文本编辑器，连候选菜单都没挂。
- `canSendActivity` 要求正文非空，只贴一张图时发送键不可用；会话输入框早已放行「只有附件」。

## 实现

- `PromptEditor` 新增三个可选回调：`onActiveTokenChange`（触发词变化，清空给 null）、`onKeyDown`（候选键盘）、`onPaste`（图片交给调用方 preventDefault）；`onEnter` 改为「候选先消费、返回 false 才走发送」。
- 候选键盘契约抽成 `components/editor/useCandidateKeyboard`（↑↓ / Tab / Esc / Enter，输入法组合回车与 Shift+Enter 不抢），会话、新建面板、详情输入框各入口行为一致。
- 候选拉取抽成 `hooks/useComposerCandidates`（file→file、prompt→prompt、slash→skill 带 agent、command→command，带防抖与 AbortController），视图层不碰 services。
- 图片提取抽成 `pasteSupport.getClipboardImageFiles`，会话输入框同步改用它（行为不变），任务侧两处共用。
- 任务详情输入框按任务所属项目/节点拉候选：`ConversationView` 把 `rootId`/`nodeId` 传给 `ActivityInput`，避免跨节点任务拿到激活节点的候选。
- 粘贴附件统一走 `buildPendingAttachment`（与「+」按钮同一份），补 `pasted-image-*` 文件名；`canSendActivity` 增加 `hasAttachments`，两处调用点传入附件数量。

## 验证

- 单元/源码契约：`web/tests/task-composer-mention-image.test.mjs` 8 条，覆盖候选类型映射、键盘契约、两处接线、`hasAttachments` 与附件构造。
- 变异验证（改完已还原）：
  - 去掉 `ActivityInput` 的 `onActiveTokenChange` → 详情用例在候选菜单处超时失败；
  - 同时去掉两处 `onPaste` → 两个用例都在附件条处超时失败。
- 浏览器端到端：`e2e/test_task_composer_mentions.py`（隔离后端 + 真实 Playwright）两条用例 PASS —— 详情输入框与新建面板都完成「`@` 候选 → Tab 落 token → 粘贴图片 → 附件缩略图」，且详情输入框「只有附件」时发送键可用。
- 门禁（干净 worktree，`087a93a7` + 本组改动）：`tsc -b --noEmit` clean；`vitest run` 249 文件 / 768 通过 / 6 跳过。
  共享工作区里另有两处红（`task-cache-eviction`、`task-ghost-card`）属并发会话在飞的看板改动，与本组无关，干净 worktree 上全绿。

## 边界

- 不改会话输入框的候选与上传语义（仅把图片提取提成公共函数）。
- 不改后端：候选接口与附件上传接口都复用会话侧既有实现。
- `/` 命令候选在任务侧固定用 `DEFAULT_TASK_AGENT`；任务本身不绑定 harness。

## 追加轮：候选菜单被面板裁掉（2026-11-20，Scope: G-NL）

用户报「@ 候选菜单在「新建任务」面板里会被验证层/其他元素遮挡，移动端（窄屏）几乎看不见」，
要求先复现定位根因、再修，桌面与移动视口都要重跑复现路径。

**取证（隔离实例 + 真浏览器，两次视口各一次，均为修复前现场）**

| 视口 | 菜单矩形 | `.mindfs-panel-body` 顶端 | 4 条候选的命中测试 |
|---|---|---|---|
| 1440×900 | `top 319 / bottom 456 / h 137` | `top 416`（`overflow:auto`） | 仅最后一条命中菜单；其余落在 `.mindfs-panel-overlay`、面板 `HEADER` |
| 375×667 | `top -16 → 121`（越过视口顶端） | `top 81` | 两条命中 header、一条命中 overlay、仅一条命中菜单 |

**根因**：菜单是 `position:absolute; bottom:calc(100% + 6px); z-index:2` 挂在包住编辑器的
`position:relative` 容器里。祖先是 `.mindfs-panel-body`（`overflow:auto`）与
`.mindfs-panel-dialog`（`overflow:hidden`），而面板正文顶端到编辑器顶端只剩约 40px，
`z-index:2` 还被困在面板遮罩（`z-index:90`）的层叠上下文里。被裁掉的部分既不可见、
`document.elementFromPoint` 也接不到 —— 「看不见」与「点不到」是同一个原因。

**修法**：`TaskInlineCandidateMenu` 改为 `createPortal(..., document.body)` + `position:fixed`，
按锚点矩形定位：优先上方，上方不足 `MENU_PREFERRED_SPACE=200` 就翻到下方，两侧都不够取更宽裕的一侧；
高度夹在 `[96,260]`，`z-index:200`；`window.resize`、祖先任意层滚动（捕获阶段）、
`window.visualViewport` 的 `resize`/`scroll` 都重算（软键盘只改视觉视口，不改布局视口）。
两处调用点（新建面板、任务详情）各自在编辑器外层挂 `composerAnchorRef` 并作为 `anchorRef` 传入，
`isMobile` 属性随之删除。

**验证**

- 修复后同一探针：桌面 `top 319 / bottom 456`、4 行全部 `hit: menu`；移动端 `top 179 / bottom 316`、
  `data-placement=below`、4 行全部 `hit: menu`，整体在 375×667 内。
- `e2e/test_task_composer_mentions.py` 新增 `_assert_menu_visible`（菜单矩形必须整体落在
  `visualViewport` 内、`position` 必须是 `fixed`、未被压进内部滚动、每行 `elementFromPoint` 命中菜单），
  并接进 `_type_mention`；新增 375×667 用例覆盖新建面板与任务详情两处，结束恢复 1440×900。
  三条用例全 PASS（54.5 s）。
- 变异验证（均已还原）：`position: "fixed"` → `"absolute"` → vitest 契约用例与 e2e 同时打红；
  给菜单加 `pointerEvents: "none"` → 命中测试断言打红（`hit: INPUT.`）。
- 源码契约：`web/tests/task-inline-candidate-menu.test.mjs` 新增「必须 portal 到 body / fixed / 读 visualViewport /
  两处调用点传锚点」一组断言。
- `npx tsc -b --noEmit` clean；`VITE_MIND_FS_BASE=/mindfs npm run build` 成功。
