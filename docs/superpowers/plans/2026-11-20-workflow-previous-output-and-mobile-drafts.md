# 工作流节点间传递 {previous_output} + 节点级 Plan 模式 + 手机草稿抽屉

- 日期：2026-11-20
- 授权：用户直接提出四项需求（「移动端也可以修改工作流」「请把工作流列表做成浮动的左侧栏…隐藏右侧的滚动条…第一个模块是干嘛的？是不是浪费了…应该有一个 plan mode 的开关」「1. 去掉 input 节点+增加完整的 {previous_output} 功能 2. a」）
- 定制组：G-NK（`docs/upstream-customizations.yaml`）
- 状态：**已实现，门禁绿（唯一红项属并发会话的未提交改动），未部署、未重启**

## 问题

1. 内置工作流 `workflow-new-feature` / `workflow-debug` 的第一个节点 prompt 就是 `{previous_input}`，等于把用户留言原样丢给一个 Agent 再交给下游 —— 一整个模型回合什么也没做。
2. 占位符只有 `previous_input`（用户留言），**没有「上游节点产出」通道**。设计节点的方案传不到实现节点，实现只能从会话历史里自己猜（「闷头空做」）。
3. `params.plan_mode` 后端 `WorkflowNodeAgentProfileOf` 早已支持，属性面板没有入口，设计类节点只能靠 prompt 里口头约定。
4. 手机上草稿列表横在画布上方、挤掉画布宽度；编辑器页右缘浏览器滚动条碍事；描述被压成单行省略号。

## 方案

### 1. `{previous_output}`（后端）

- 新文件 `server/internal/kanban/workflow_node_previous_output.go`：`workflowPreviousOutputPrompt` 解析上游节点产出。
  - 数据源优先 `run.NodeOutputs` 投影（与 human_gate 审批卡的 `upstreamOutputSummaries` 同源），投影缺行时回落 `workflow_node_runs` 行（崩溃恢复场景）。
  - Loop 内部用 `workflowLoopGraph` + `workflowLoopIterationRun` 取**当前 iteration** 的子投影。
  - 单上游直接给摘要；多上游按节点名分节（`「<name>」的产出：`）；单父 12000 字节 / 合计 24000 字节截断。
  - 无入边 → 明确报错「没有上游（起始节点请用 {previous_input}）」；有入边但上游还没产出 → 「上游节点 %s 还没有产出」。两者都 fail-closed，不塞空串。
- `workflow_node_executor.go`：`WorkflowPromptPlaceholders` 加 `previous_output`，`prepareWorkflowNodeAt` 里**只有 prompt 真的含该占位符**才解析（没用到不查库、不因上游空而失败）；错误文案按占位符区分（`workflowPromptPlaceholderReason`）。
- `workflow_authoring.go` + `http_workflow_authoring.go`：空白草稿基线节点带 `prompt: {previous_input}`（无 prompt 的 agent 节点执行期是硬错误）；生成提示词里补充两个占位符的语义与 `plan_mode` 用法。

### 2. 内置工作流重写（数据，走 HTTP API）

- `workflow-new-feature` r3 → r4：`plan`（方案设计，`plan_mode: true`，`{previous_input}`）→ `implement`（代码实现，`{previous_output}`），发布 v4。
- `workflow-debug` r1 → r2：`locate`（定位根因，`plan_mode: true`，`{previous_input}`）→ `fix`（修复验证，`{previous_output}`），发布 v2。
- `workflow-blank` 不动。

### 3. 节点级 Plan 模式（前端）

- `workflowModel.ts`：`workflowNodeUsesPlanMode`（只认布尔 `true`）/ `setWorkflowNodeUsesPlanMode`（勾选写 `params.plan_mode=true`，取消删键）。
- `WorkflowNodeInspector.tsx`：写节点 `params`，不写全局配置。
- i18n `task.workflowCanvas.planMode` / `planModeHint`；`task.promptInfo` 补 `{previous_output}` 说明。

### 4. 手机草稿抽屉 + 滚动条 + 描述

- `workflowEditorLayout.ts` 新增纯函数 `draftPanelFrame`：桌面 190px 固定竖栏；手机 absolute 覆盖层 `min(240px, 72vw)`，收起 `translateX(-110%)` + `visibility: hidden`，不挤压画布宽度。
- `WorkflowDraftEditor.tsx`：收起后只剩左边缘悬浮把手（`workflow-draft-panel-toggle`，portal-free 但位于相对定位容器内）；选中草稿后手机自动收起；编辑器打开期间给 `body` 打 `data-workflow-editor-mobile`，用 `<style>` 藏掉 `.mindfs-main-scroll` 的滚动条；头部在手机上改纵向、描述折两行。
- `DefaultListView.tsx`：主区滚动容器加 `mindfs-main-scroll` 类名供上面那条规则命中。

## 验证

- 后端：`workflow_node_previous_output_test.go` 7 条（投影解析 / 无产出 fail-closed 且 runner 零调用 / 无入边 / 多上游命名拼接 / NodeRun 行回落 / Loop 当前 iteration / 未使用不解析）。4 处变异各自打红对应用例（改投影源、去掉 fail-closed、Loop 用顶层投影、去掉 Contains 守卫）。
- 前端：`workflow-editor-mobile.test.ts`（抽屉 frame 语义、收起/展开、滚动条、描述两行）、`workflow-inspector-placement.test.ts`（Plan 模式纯函数与面板接线、i18n 占位符说明）。3 处变异打红。
- 门禁：`go test -count=1 ./...` 全绿（28 包）；`npx vitest run` 757 通过 / 6 跳过，**唯一红项 `web/src/app/appSession.ts: 507` 属并发会话的未提交改动（HEAD 为 499 行），不在本组范围内**；`tsc -b --noEmit` clean；`go build ./...` 成功。
- 已知残留：`workflow_node_executor.go` 有一处 gofmt 对齐问题，**HEAD 上就存在**（并发会话的字段对齐），本轮未动。

## 未做

- 未部署、未重启任何节点（本机 `sudo systemctl restart mindfs` 仅用户可执行）。
- 线上 `workflows.json` 的两条工作流已通过 API 保存并发布，但**运行中的进程不缓存**（`WorkflowStore` 每次操作整份读写），所以下一轮任务创建即可选中新版本，无需重启。

## 追加轮（2026-11-20 晚，Scope: G-NO）—— 工作流编辑器的八条反馈

用户重启本机后逐条提出（`/refine`）：

1. 文本改成 `Plan` / `新会话` / `worktree`：`planModeShort` / `newSessionPerNodeShort` / `independentWorktreeShort` 按这三个词写死；en 用 `Plan` / `New session` / `Worktree`。
2. 「写入」被 Plan 取代：`setWorkflowNodeUsesPlanMode(params, true)` 同时写 `plan_mode: true` 与 `writes_workspace: false`，取消时两个键一起删；后端 `WorkflowNodeWorkspaceOf` 兜底 —— `plan_mode=true` 一律按只读处理（AI 生成或手写的图漏写 `writes_workspace` 时也不占 writer gate、不合并产物）。开关从四个变三个（1×3 等宽）。
3. 打开编辑器会自动聚焦 AI 输入框：根因是 `PromptEditor` 挂载时 `setText("")` → Lexical `$replaceWithSerializedText` 把内部选区落到文末并同步成 DOM 选区，Chrome 因此把 contenteditable 当成被点击。修复在 `TokenEditor.setText`：未聚焦时记下原焦点，update 后（含一个 `queueMicrotask` 补拍，Lexical 的 DOM 提交可能排在微任务）若焦点被拽进本编辑器就还给原元素。
4. Agent 配置收成右上角一个 `AgentSelector` 图标（`compact`），不再占一块「Agent 配置」区域；没有 agent 清单时才退回手填。
5. 手机侧栏：默认展开、选完/建完自动收起、点抽屉外面收起（`pointerdown` 捕获阶段）、收起后的悬浮把手竖向居中（`draftHandleFrame`）。抽屉是 absolute 撑满父列，所以父列在手机上必须有高度（`min(60vh, 520px)`），否则没选工作流时容器塌成十几像素、抽屉变成一条缝（390×844 实测到）。
6. 「草稿列表」→「工作流列表」（en `Workflows`）；列表行不再有删除键。
7. 保存缩小并挪到「工作流画布」那行右侧（`WorkflowCanvas` 新增 `actions`），旁边加「还原」（`revert` 丢弃本地改动回到服务端草稿）；发布按钮与 `publishConfirm` 删除 —— 保存即发布：`save` 先 `updateWorkflowDraft` 拿新修订，再 `publishWorkflowDraft(updated.revision)`，发布失败保留 dirty 以便重试。后端表/接口原样保留（执行侧仍按已发布版本解析），只是不再是一条用户流程。
8. 首行 = 任务名 + 编辑/删除（居左）……关闭（居右），描述退到第二行整宽（手机两行）。

同时把「AI 生成 / 提案」拆成 `WorkflowAuthoringPanel.tsx`（守 500 行硬线）：手机上去掉内圈框和底色、融入外框，桌面端在输入框上方加回这一行标题。

验证：
- 前端全量 `npx vitest run` 772 通过 / 6 跳过，`tsc -b --noEmit` clean；后端 `go test ./server/internal/kanban/` 与 `go build ./server/... ./cli/...` 通过。
- 隔离实例（临时 `XDG_CONFIG_HOME` + 新端口 + `MINDFS_STATIC_DIR=web/dist`，端口随机、不碰 7331）真实 Chromium：
  - 桌面 1440×900 逐条 11/11 PASS —— 列表标题、首行顺序、保存/还原在画布行、无发布按钮、三个开关（`Plan`/`New session`/`Worktree`）、写入开关已删、点 Plan 出脏标记、保存后脏标记消失（服务端日志 `PUT /api/workflows/{id}` 后紧跟 `POST /api/workflows/{id}/publish`）、**重开编辑器焦点停在 BODY**；
  - 手机 390×844（从移动壳的 Board 进入）6/6 PASS —— 抽屉默认展开、新建后自动收起、点外面收起、左缘把手可见、把手在自己那一列的竖向中点、点把手再展开。

交付（2026-11-20）：
- 提交 `925d910d`（实现 + 登记 G-NO）与 `de5b1a79`（G-NO 拆出 AI 面板后重指 G-LG/G-NI/G-NK 的三处锚点），已 push origin main。
- `make check-upstream`：2095 个 delta 文件 / 341 组全绿。
- `make install`（= build-web + go build + 安装）产出 `v0.5.5-1200-55-gde5b1a79`（md5 `0043eda6…`）；本机 `~/.local/share/mindfs/web/` 已覆盖，线上 7331 首页引用的 `assets/index-BuQKueRz.js` 返回 200，刷新即生效（静态资源跟磁盘走，不用重启）。
- 运行中的进程仍是 `v0.5.5-1200-52-gb61b81d8`（`/proc/377827/exe` 指向已删除的旧二进制）：**Plan 接管写入的后端兜底（`WorkflowNodeWorkspaceOf`）要等用户 `sudo systemctl restart mindfs` 才生效**，重启前只有前端写 `writes_workspace=false` 这一层。
- 未部署其他节点。

## 追加轮二（2026-11-20，Scope: G-NO）—— 收起键下移 + 关闭改图标

用户原话：「侧板收起的按钮也放到中间对应位置，不要在顶上。关闭请用“x” icon」。

- **收起键**：从抽屉标题行里的小按钮改成贴在抽屉**右缘**的把手，竖向中点与收起后左缘的
  `workflow-draft-panel-toggle` 完全重合（新纯函数 `draftCollapseFrame`，宽度取
  `DRAFT_PANEL_WIDTH` 单一常量，两边不各抄一份）。它是抽屉的**兄弟**节点而不是子节点：
  抽屉自己 `overflowY: auto`，放进去的绝对定位按钮会跟着列表滚走。`zIndex 30 > 抽屉 20`，
  与左缘把手镜像（靠外那侧直角、靠内那侧圆角）。
- **关闭**：文字按钮换成 `CloseIcon`（`WorkflowNodeIcons.tsx`）+ `aria-label`/`title`，
  与首行的编辑/删除同一种小图标键，带 `data-testid="workflow-editor-close"`。
- 首行的编辑/删除键与关闭键压成同一种单行写法，`WorkflowDraftEditor.tsx` 490 行（硬线 500）。

验证（2026-11-20）：
- 源码契约 `web/tests/workflow-editor-mobile.test.ts` 新增一条：两个把手 `top`/`transform` 相同、
  `left = calc(抽屉宽度 - 28px)`、只在靠内一侧圆角、层级高于抽屉、收起键渲染在 `nav` **之前**
  （兄弟节点）；关闭键有 `aria-label` 且画的是 `<CloseIcon>`、文字版已删。
- 干净 worktree（`HEAD` + 本组三个文件）：`tsc -b --noEmit` clean、`npx vitest run`
  251 文件 / 777 通过 / 6 跳过。共享工作区的 4 处红（`useTaskBoard.ts:509` 等）属并发会话在飞改动。
- 隔离实例真实 Chromium 390×844（`/tmp/verify_editor_tweaks.py`，日志 `/tmp/tweaks-run.log`）
  **18/18 PASS**：关闭键是 svg、无文字、可读名字 `Close`；抽屉 box `x=16 w=240`、收起键
  `x=228 w=28`（右缘差 **0.0px**）、竖向中点差 **0.0px**；收起后左缘把手与刚才的收起键
  同一高度（差 **0.0px**）、贴列左缘；再展开仍在同一处；收起态不再渲染收起键；点抽屉外面
  照旧收起（原行为未回归）。截图 `/tmp/tweak-mobile-open.png`、`/tmp/tweak-mobile-closed.png`。

