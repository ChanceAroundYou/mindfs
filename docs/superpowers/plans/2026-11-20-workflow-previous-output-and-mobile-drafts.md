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
