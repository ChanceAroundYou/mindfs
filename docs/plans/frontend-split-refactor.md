# 前端拆分重构 · 待办与约束

> 本分支（task-35）专门做前端的目录重组与文件拆分。本文记录**拆分方向的待办清单、
> 必须遵守的规则、以及验证手段**。它既是后续接力的入口，也是合上游时判断
> 「这块拆分该不该留」的依据。
>
> 数据截至本文所在提交（合并 main 前）。行数会随后续提交变化，**结构判断以
> `wc -l` 实测为准**。

## 1. 目标与策略

**目标**：`components/`、`services/` 按领域分域；把巨文件拆成职责单一的模块；
单文件尽量 ≤ 1200 行。

**策略**：**机械搬移 + 旧路径 barrel 重导出，零行为变化**。

- 移动文件用 `git mv`；拆分模块时原路径保留 `export * from "./x/index"`，
  对外导出面不变。
- 不改 TS 类型、wire 格式、localStorage 键；不引入新依赖。
- 拆分只搬代码，不改逻辑 —— 一旦发现需要改逻辑，说明拆错了边界，退回重划。

**为什么坚持零行为变化**：本仓库是上游 fork，每次改动都是会被上游合并冲掉的定制；
拆分本身不产生功能，一旦掺入行为改动，合上游时无法区分「拆分没合好」还是
「行为本来就这样」。边界见 `docs/upstream-customizations.md` 的 G-BA 组。

## 2. 已完成（本分支）

| 项 | 内容 | 提交 |
|---|---|---|
| P0 | source-map 统一读层（`web/tests/source-map.mjs` + hook + 测试） | `a83950b` |
| P1 | `components/`、`services/`、`shared/` 按领域重组 | `f5c4e0d` |
| P2 | `services/{tasks,git,file}.ts` → 目录模块 + 旧路径 shim | `7b6d969` |
| P6 | `FileTree.tsx` 图标/小组件 → `components/file/icons.tsx` | `def8b7e` |
| P7 | 门禁登记（G-BA 组）+ 死文件清理 + release notes | `b370038` |
| e2e | Playwright 运行时冒烟夹具（`e2e/`，10 用例）+ 门禁登记 | `1f5b1d4` |
| App-1 | 会话缓存 → `app/useSessionCache.ts`（refs 语义不变） | `2386751` |
| App-2 | 纯函数 → `app/appUtils.ts`（4 个，零闭包） | `0b20717` |
| App-3 | 发送消息主流程 → `app/useSendMessage.ts`（~570 行，36 闭包） | `ccbfe13` |
| App-4 | 文件/目录动作 → `app/useActionHandlers.ts`（~430 行，45 闭包） | `928c71f` |
| A1a | 会话生命周期（8 单元）→ `app/useSessionLifecycle.ts`（1016 行） | `931d7af` |
| A1b | 会话归档/删除 → `app/useSessionArchive.ts`（338 行） | `f510c95` |
| A2+A3 | 多项目会话分组 + 待处理提升 → `app/useMultiProjectSessions.ts`（481 行） | `f510c95` |
| A4 | 托管根（重命名/刷新/载荷）→ `app/useManagedRoots.ts`（709 行） | `f510c95` |
| A6a | 插件加载与处理 → `app/usePluginHandlers.ts`（240 行） | 本次 |
| A6b | 目录树 / 当前文件内容刷新 → `app/useFileRefresh.ts`（180 行） | 本次 |
| A6c | 关联文件 diff 打开 → `app/useRelatedFileDiff.ts`（168 行） | 本次 |

**当前分域目录**：

```
web/src/
├─ app/            App.tsx 抽出的 hooks 与纯函数（20 模块，既有 G-AA）
├─ renderer/       渲染器（4 模块，既有 G-AA）
├─ components/
│  ├─ account/    登录 / 账户 / 节点管理
│  ├─ action/     ActionBar
│  ├─ agent/      Agent 选择 / 图标 / 模型
│  ├─ common/     通用小件（Select / InlineTokenText / …）
│  ├─ editor/     PromptEditor
│  ├─ file/       文件树 / 查看器 / 上传
│  ├─ git/        Git 面板 / diff
│  ├─ root/       根视图内容
│  ├─ session/    会话列表 / 查看器 / 归档
│  ├─ shell/      外壳（DialogHost / PanelShell / Toast / …）
│  ├─ stream/     流式工具卡
│  ├─ task/       看板 / 任务详情 / 阶段
│  └─ workspace/  跨项目工作台
├─ services/
│  ├─ net/        网络 / 节点 / e2ee / 鉴权
│  ├─ platform/   PWA / 原生桥 / 下载 / 剪贴板
│  ├─ prefs/      偏好 / 外观 / 快捷键
│  ├─ task/       看板任务 API（拆分）
│  ├─ git/        Git API（拆分）
│  └─ file/       文件 API（拆分）
└─ shared/        纯工具（scope / overlay / diff 模型 / …）
```

## 3. 拆分规则（每拆一块都要做全）

> 本分支的拆分定制组：合并 main 前为 **G-AY**。合并时 main 已占用 G-AY（抽屉 pending
> 对账）与 G-AZ（任务卡视觉件），故重编号为 **G-BA**。下文一律按 G-BA 写。

1. **机械搬移 + barrel shim**：原路径 `export * from "./<dir>/index"`，
   导出面一个不少。用 `web/tests/frontend-split.test.mjs` 的写法加断言。
2. **注册 source-map**：在 `web/tests/source-map.mjs` 的 `MODULES` 里把
   「逻辑路径 → 物理文件列表」补上。**测试与断言都不用动**。
   - 多文件（拆分模块）：`"src/services/x.ts": ["src/services/x/types.ts", …]`
   - 单文件（只搬移）：`"src/components/Old.tsx": ["src/components/<域>/Old.tsx"]`
3. **门禁登记**：`docs/upstream-customizations.yaml` 的 **G-BA** 组补 `files:`；
   `docs/upstream-customizations.md` 补 `### G-BA` 标题（组 id 必须两边一致）。
   锚点写 `path:子串`，`files` 写路径（前缀 `/` 表示目录、`*` 走 `path.Match`）。
4. **提交信息标 `Scope: G-BA`**。
5. **验证**：见第 5 节。

## 4. 待办清单（按优先级）

### 4.1 App.tsx —— 7593 行（最大，收益最高，风险也最高）

- **现状（本次提交后）**：9585 → 7593 行（-1992，-21%）。已抽出 11 个 `app/` 模块
  （`useSessionCache` / `appUtils` / `useSendMessage` / `useActionHandlers` /
  `useSessionLifecycle` / `useSessionArchive` / `useMultiProjectSessions` /
  `useManagedRoots` / `usePluginHandlers` / `useFileRefresh` / `useRelatedFileDiff`）。
- **关键约束（TDZ 窗口）**：抽出的 hook 用「闭包当入参」的方式调用，参数在**渲染期立即求值**，
  所以 hook 调用点必须晚于它收到的每个闭包的 `const` 声明行（TDZ）。判据：
  `max(闭包声明结束行) < min(急切引用行)`，其中「急切」= 依赖数组 / JSX / 渲染期调用，
  「惰性」= `useCallback`/`useMemo`/`useEffect` **函数体内**的引用（回调在渲染后执行，不受 TDZ 约束）。
  工具：`/tmp/window.py` 验窗口，`/tmp/extract.py` 执行抽取（调用点自动落在语句边界上）。
- **目标（2026-10-07 决策）**：**分阶段，先到 ≤4500 行再评估**。
  不直接冲 ≤900 —— 后期边界互锁紧密，风险累积。
- **前置（已解除）**：运行时验证手段 —— `e2e/` Playwright 冒烟（10 用例）已就位。
- **剩余簇（按窗口可拆性排序）**：

  | 簇 | 单元 | 阻碍 |
  |---|---|---|
  | A5 看板/任务 | `handleMoveKanbanTask`(95)、`loadKanbanTasks`(65)、`saveTaskInlineEdit`(60)、`applyTaskDetails`(58) | 首个急切引用在依赖数组里（含 `applyTaskDetails`/`pruneTaskDetails`），需重新切分 |
  | A7 会话元数据 | `attachContextWindowToLatestAssistant`(108)、`updateSessionAgentForKey`(68)、`updateSessionRelatedFilesForKey`(45)、`updateSessionRelatedWorktreeForKey`(45)、`handleSetPlanMode`(45)、`handleRemoveSessionRelatedFile`(43) | 窗口不成立（闭包声明晚于依赖数组引用） |
  | A8 更新/其他 | `handleStartUpdate`、`selectRootNode`、`handleTreeUpload`、`handleForkAgentMessage`、`handleProjectTreeRefresh`、`showBoundSessionOrRootDir`、`selectedSessionRelatedFileGroups`、`rootSessionIndicators` | 同上 |
  | A2b 待处理/队列 | `clearLocalPendingForSession`、`markSessionPending`、`resolvePendingForSession`、`getSessionSnapshot`、`refreshMultiProjectReplyingSessions`、`pruneSubtreeFromSessionState` | 同上 |
  | B 渲染层 | 主 `return` JSX（~900 行）+ 71 个 `useEffect`（~950 行） | 无 TDZ 约束（JSX 在文件末尾），风险低、收益高 |
  | C 其余大文件 | FileTree / SessionViewer / SessionList / useRealtimeEvents / AgentSelector / DefaultListView / ScheduledAgentTaskDialog / MarkdownViewer / ToolCallCard / ActionBar | 独立文件，逐个拆 |
- **建议做法**：
  1. 按「一组内聚状态 + 其唯一出口 effect」为单元抽 hook，沿用
     `web/src/app/useXxx.ts` 的既有模式（入参是依赖项，出参是 state + 操作函数）。
  2. 第 1 轮先抽边界最清晰的 7 个单元（见下），每抽一块就注册 source-map
     （`"src/App.tsx": ["src/App.tsx", "src/app/useXxx.ts"]`）并跑全量门禁 + e2e。
  3. 第 2 轮起处理剩余互锁更紧的集群；**一旦发现需要改逻辑，说明拆错了边界，退回重划**。
- **第 1 轮抽取单元**：

- **参考**：`app/useRealtimeEvents.ts`（2019 行）、`app/useProjectLifecycle.tsx`（885 行）
  就是这套模式的产物。

### 4.2 FileTree.tsx —— 3764 行

- **现状**：`FileTreeInner`（主组件）+ 4 个顶层小组件 + 一批样式常量。
  - `AgentConfigLineEditor`（263 行）
  - `AgentConfigPopover`（354–858）
  - `AgentLifecyclePopover`（859–1020）
  - 样式：`agentConfig*Style`（1021 行起，共 10 个）
- **已抽**：`icons.tsx`（13 个图标/小组件，`def8b7e`）。
- **待抽**：`AgentConfigLineEditor` + `AgentConfigPopover` + `AgentLifecyclePopover`
  → `components/file/AgentConfigPopover.tsx`。
- **样式归属（实测）**：
  - 7 个**只被 popover 用**，可整体搬走：`agentConfigFieldStyle`、
    `agentConfigLabelStyle`、`agentConfigInputStyle`、`agentConfigLineEditorStyle`、
    `agentConfigLineTextAreaStyle`、`agentConfigHintStyle`、`agentConfigIconButtonStyle`。
  - 3 个**与 `FileTreeInner` 共用**（各 3 处）：`agentConfigActionRowStyle`、
    `agentConfigPrimaryButtonStyle`、`agentConfigSecondaryButtonStyle`。
    → 搬到新文件后从那里 `export` 回来，或留在 `FileTree.tsx` 当入参传入。
- **收益**：FileTree.tsx 约 3764 → 2900 行。

### 4.3 SessionViewer.tsx —— 3578 行

- **现状**：单文件承载消息列表、工具卡、输入区、滚动/窗口逻辑。
- **待办**：按渲染单元拆子组件（消息行 / 工具卡组 / 输入区 / 用量面板）。
- **注意**：合并 main 时此文件有上游改动（`c04deeb` 的 pending 对账），拆之前先确认。

### 4.4 SessionList.tsx —— 2204 行

- **现状**：列表项、分组、归档、搜索混在一个文件。
- **待办**：拆出 `SessionListItem` / `SessionGroupHeader` / 归档区。
- **注意**：合并 main 时此文件有上游改动（`+11/−26`）。

### 4.5 session.ts —— 2659 行（**被测试锁住，需先解锁**）

- **为什么不能拆**：4 个测试用 `ts.transpileModule` + `vm.runInContext` **执行**
  这个文件的源码，沙箱里的 `require` 只认裸模块名：
  - `web/tests/pins-authority.test.mjs`
  - `web/tests/session-archive.test.mjs`
  - `web/tests/session-window-dedup.test.mjs`
  - `web/tests/session-window.test.mjs`

  拆出的相对值导入（`./merge`、`./sessionCache`）在沙箱里解析不到，测试直接炸。
  **source-map 的源码拼接救不了这条路径**（它只影响读源码做正则的测试，
  影响不到真正执行代码的 VM 测试）。
- **决策（2026-10-07）**：**现在投入解锁，然后拆**。
  解锁条件：先改造这 4 个测试的沙箱 `require`，让它支持相对路径解析
  （把 `MODULES` 映射也喂给沙箱）。解锁后按 4.1 的模式拆。
- **历史**：本分支曾尝试拆分并已回滚（见提交历史），不要在没有解锁条件时重试。

### 4.6 其他 > 600 行的文件（收益递减，按需）

- **决策（2026-10-07）**：**只做头部 6 个组件**；i18n 语言文件排除。
- **排除**：`i18n/locales/en-US.ts`（1016）、`zh-CN.ts`（1014）—— 纯数据
  （翻译 map），拆分价值低、漏 key 会导致未翻译 UI，记为已知偏差。
  同样排除 `main.tsx`（入口）、`services/net/e2ee.ts`（服务）。
- **要做（头部 6 个组件）**：

  | 行数 | 文件 | 备注 |
  |---|---|---|
  | 1477 | `components/agent/AgentSelector.tsx` | 选择器 + 列表 + 记忆指示器 |
  | 1296 | `components/file/DefaultListView.tsx` | 默认列表视图 |
  | 1098 | `components/task/ScheduledAgentTaskDialog.tsx` | 定时任务弹窗 |
  | 1082 | `components/file/MarkdownViewer.tsx` | Markdown 渲染 |
  | 1016 | `components/stream/ToolCallCard.tsx` | 工具卡 |
  | 1002 | `components/action/ActionBar.tsx` | 输入栏 |

- **已是 hook，内部可再拆（低优先）**：`app/useRealtimeEvents.ts`（2019）、
  `app/useProjectLifecycle.tsx`（885）、`hooks/useSessionStream.ts`（664）。

## 5. 验证手段

```bash
# 类型
cd web && npm run typecheck

# 前端测试（含 source-map 读层、barrel 重导出、无导入环断言）
cd web && npm test

# 构建（vite；注意它不做类型检查）
make build-web

# fork 定制门禁（delta 覆盖 / 幽灵条目 / 组自洽 / yaml-md 组号一致）
go run ./scripts/check-upstream
```

**当前基线**（本文所在提交）：typecheck 0 错；209 测试、208 过、1 skip、0 失败；
构建成功；门禁 559 个 delta 文件被 48 组完整覆盖；dist 体积相对拆分前 +11 字节
（+0.0001%，目标 ≤ 2%）。

### 5.1 source-map 读层（拆分的配套基础设施）

`web/tests/source-map.mjs` 是「逻辑路径 → 物理文件列表」的**单一映射**：

- `MODULES`：登记被移动/拆分过的逻辑模块；未登记按原路径读。
- `readSource(logical)`：返回该模块全部物理文件的拼接 —— 拆分后，
  按旧逻辑路径写的源码正则断言仍能命中。
- 消费方两个：`source-map-hook.mjs`（预加载 patch `fs.readFileSync`，
  覆盖 `fs.readFileSync` / 具名 `readFileSync` / `new URL` 等所有读法）、
  `ts-module-hook.mjs`（让 `await import("../src/…")` 解析到移动后的物理文件）。
- **维护规则：文件移动/拆分时只改 `MODULES`，测试不动。**

> 实现细节坑：必须用 `createRequire` 取 `node:fs`（ESM 具名导入会在
> `node:fs` facade 建立时快照，之后再 patch CJS 导出覆盖不到具名导入）。

## 6. 风险与前置条件

| 风险 | 说明 | 缓解 |
|---|---|---|
| 运行时行为回归 | 现有测试是源码正则 + 类型检查，**不覆盖运行时** | 4.1 的前置：先补运行时验证 |
| VM 测试锁死 `session.ts` | 沙箱 `require` 只认裸模块名 | 先解锁（4.5）再拆 |
| 与 main 的重命名冲突 | main 会继续改旧路径的文件 | 尽早合并 main；拆分提交保持「纯搬移」便于 git 跟随重命名 |
| 组号撞车 | main 也会新增 G-* 组 | 合并 main 后重编号本分支的组（本次：G-AY → G-BA） |
| 门禁变红 | 新 delta 文件未登记 | 每拆一块就补 G-BA 的 `files`/`tests`，并跑 `check-upstream` |

## 7. 决策记录

| 日期 | 决策 | 理由 |
|---|---|---|
| 2026-10-07 | App.tsx **分阶段，先到 ~4000–5000 再评估**，不直接冲 ≤900 | 后期边界互锁紧密，风险累积；每步可验证更稳 |
| 2026-10-07 | session.ts **现在投入解锁 VM 测试，然后拆** | 解锁后收益大；4 个测试的沙箱 `require` 改造是明确的前置 |
| 2026-10-07 | i18n 语言文件（en-US.ts / zh-CN.ts）**排除，不拆** | 纯数据，拆分价值低、漏 key 导致未翻译 UI |
| 2026-10-07 | 「其他 >600 行」**只做头部 6 个组件** | 收益递减；跳过 i18n / main.tsx / e2ee.ts 等特殊文件 |
