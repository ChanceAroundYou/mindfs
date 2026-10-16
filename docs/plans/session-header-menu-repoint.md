# 会话标题栏三点菜单（修改目录 / 重命名 / 归档 / 删除）

> 状态：**实施中**。日期：2026-10-16。
> 验收：会话标题栏右侧三点菜单可用；「修改目录」走新建项目那套目录浏览器，默认停在本项目目录，
> 确认后一次做完「搬转录 + 换工作目录 + 解工作树 + 清 related」；目录行太长换行、目录不存在整行转红。
> 门禁：Go 测试 + 前端 typecheck/test/build + `make check-upstream` + fork 三件套（登记 + 针对性测试 + `Scope: G-X`）。

## 一、要解决的问题

会话绑定过工作树、目录后来被删掉时，会话留在原地继续聊会写回主 slug，两边打架（见 AGENTS.md 事实 14）。
已有的 `POST /api/sessions/{key}/repoint` 只能「搬回本项目主 checkout」，且**必须**会话仍绑着工作树，
没有任何 UI 入口 —— 只有 `wt-finish.sh` 脚本在用。

## 二、方案（用户确认版）

会话标题栏（`SessionViewerHeader`，`ViewHeader` 的 children 最右端）加一个 ⋯，布局与文件面板
（`FileTreeHeaderOverlays`）逐字一致；移动端由 `ViewHeader` 自己插侧栏切换按钮，故两端同一份代码。
只在正式 chat 面板显示（`interactionMode !== "drawer"`，抽屉那个形态没有标题栏，不做）。

菜单项自上而下：

| 项 | 行为 |
|---|---|
| 重命名 | 标题就地变输入框，回车存（复用 `handleRenameSession`） |
| 归档 / 取消归档 | 复用 `handleArchiveSession` |
| 删除 | 红色，复用 `handleDeleteSession`（自带确认） |
| ── 分隔线 ── | |
| 修改目录 | 打开目录浏览器（复用 `LocalPanel`），默认停在本项目目录 |
| 灰/红小字 | 会话当前工作目录；太长换行；目录不存在（`/api/local_dirs` 404）整行转红 |

## 三、后端改动

1. `session.Session` 新增 `WorkDir`（JSON `work_dir`，空 = 项目目录）+ `sessions.work_dir` 列（含迁移）。
   为什么不复用 `RelatedWorktree`：它表达「会话属于哪个工作树」，会被任务收尾清掉，
   且 UI 按它显示工作树徽标 —— 拿它承载「用户随手选的目录」会误触发收尾与徽标。
2. 运行时 cwd 解析（`api.sessionRuntimeRootPath` / `usecase.sessionRuntimeRootPathFor`）优先取 `WorkDir`。
3. `Manager.SetWorkDir(ctx, key, dir)`：写/清工作目录。
4. `Manager.RemoveRelatedFilesUnder(ctx, key, dir)`：清掉落在 `dir` 下的 related 条目。
5. `RepointSession` 新增 `TargetPath`（可选，缺省 = 本项目主 checkout）：
   - 校验绝对路径 + 存在 + 是目录（校验函数独立可测）；
   - 目标 = 当前工作目录时直接拒绝（什么都不做，避免无谓 fork）；
   - 去掉「必须绑着工作树」的限制 —— 现在目标是显式给的；
   - 顺序不变：先 `pool.Close` 再动文件、先搬文件再写游标（游标推到底、`agent_ctx_seq` 原样保留）；
   - 收尾：`SetWorkDir`（目标 = 项目目录则清空）+ `ClearRelatedWorktree` + `RemoveRelatedFilesUnder(旧目录)`。
6. `POST /api/sessions/{key}/repoint` 接受 `target_path`。

## 四、前端改动

- `services/session.ts`：`repointSession`。
- `components/session/SessionHeaderMenu.tsx`：⋯ 按钮 + 浮层（样式抄 `FileTreeHeaderOverlays`）。
- `components/session/SessionDirectoryPicker.tsx`：居中弹层，内嵌 `LocalPanel`（复用新建项目的选路径控件），
  自带 `/api/local_dirs` 取数与节点切换；确认后回调 repoint。
- `SessionViewerHeader` 接菜单并渲染；`SessionViewer` / `useAppSessionView` / `useAppViewComposition`
  透传 `handleRenameSession` / `handleArchiveSession` / `handleDeleteSession` 与新回调。

## 五、验收与测试

- Go：`SetWorkDir` 落盘可重载；`RemoveRelatedFilesUnder` 只删目录下的条目；repoint 目标路径校验（绝对/存在/是目录/与当前相同）；
  `sessionRuntimeRootPath` 优先 `WorkDir`。
- 前端：菜单四项渲染与可见性（非 claude 会话「修改目录」置灰）、目录行换行与转红、i18n key 契约。
- 端到端：隔离实例（`XDG_CONFIG_HOME` + 新端口）点一次「修改目录」验证落盘与续聊。
