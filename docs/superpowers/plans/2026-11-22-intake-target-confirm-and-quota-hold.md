# 配额挂起收口 · 飞书显式目标确认 · 跨 harness 一致性订正

Plan 2026-11-22。三件互不依赖的工作，按 A1 → 飞书 → B3 的顺序逐件交付，
交付到 **代码 + 测试 + 本地提交**：不部署、不重启任何节点、不 push、不发版。

Scope: G-PW（A1）/ G-PX（飞书目标确认）/ G-PY（B3 文档与探针）。

## 背景与证据

### A1：`waiting_user` 的语义被挂起路径挪用了

`workflow_node_runs.not_before` 与 `releaseDueNodeHolds` 已经在册
（`workflow_node_holds.go`、`workflow_node_supervision_retry.go`），配额类失败
（`classifySupervisionFailure == SupervisionFailureQuota`）建 attempt+1 时写
`not_before`，到点由 coordinate 唤醒 —— 这条本身是对的。缺的是三样：

1. **没有上限**：同一 Run（一轮工作流执行）可以无限次挂起，上游一直 429 就永远
   在「等 15 分钟」里循环，人看不到任何提示。
2. **没有可见性**：挂起只在 NodeRun 的 `not_before` 列里，界面与对话流里没有一行
   文字说「这个节点在等用量重置」。
3. **spec 写错**：§6.9 断言「任务停在 `waiting_user`」。实际代码
   （`createWorkflowNodeAttempt` → `UpdateTaskStatus(StatusRunning)`）把任务留在
   `running`，而且**这是对的** —— `waiting_user` 是「需要人说话」的状态，
   而配额挂起会自己到点继续。只有**挂起额度用完**才该把人叫来。

### 飞书：识别之后没有「确认」这一步，也没有「这条消息属于哪个任务」的记忆

`IntakeService.Handle`（`intake.go:106`）在 `ResolveProject` 给出唯一项目后
**立刻**建任务（`creator.Create`），没有确认环节；`IntakeStore` 只记
`message_id → task_id` 的去重账本，没有「当前会话绑定了哪个任务」；
后续消息永远只能新建任务，不能追加到已有任务（尽管
`POST /api/tasks/{id}/activities` 早就存在）。

### B3：spec 把「适配器实现细节」写成了跨 harness 契约

§6.12 要求两个适配器在「compact 事件的三种形态」上一致 —— 那三种形态是 Codex
传输层的实现细节（`CompactedItem` / `UnknownItem.contextCompaction` / `GetType`
兜底），跨 harness 的共同面是**已归一化的 compact 事件**；§13 C5 又声称
「退避并入 §6.9」，本轮并没有吸收任何指数退避。

## Global Constraints

- 提交信息用 Conventional Commits（中文描述），各组标 `Scope: G-PW` / `G-PX` / `G-PY`。
- fork 定制三件套：登记 `docs/upstream-customizations.yaml`、留针对性测试、
  提交信息带 `Scope:`。
- 只本地提交；push / 部署 / 重启一律另需授权（本机 `sudo systemctl restart mindfs`
  只能用户执行）。
- 飞书凭据与长连接只在主节点；worker 卡片经主控 `/api/channels/feishu/relay` 代发，
  点击回传按卡片 `node_id` 由主控转发，绝不按请求体 URL 转发。
- 一个逻辑单元一个 commit，共享工作区只按显式 pathspec 提交。
- 门禁：`go test ./server/... ./cli/...`、必要时 `tsc`/Vitest、`make check-upstream`。

## Review Focus

1. **配额挂起额度用完后的状态**：第 4 次挂起不许再建 attempt，也不许把任务留在
   会自动恢复的 `running`/`waiting_user`，否则「额度用完」等于没写。
2. **挂起提示行的幂等**：同一 attempt 重放（coordinate 重投、进程重启对账）只能有
   一行，否则对话流会被刷屏。
3. **飞书确认卡的并发与过期**：同一条消息重投不能同时建两个任务，也不能把答案
   应用到一张已过期/已被替换的卡上。
4. **飞书默认归属的越权**：绑定目标必须逐次在服务端重新校验（节点 + 项目 + 任务
   仍然存在且归属一致），不能只信卡片里带回来的 ID。
5. **非 owner / 群聊 / 非文本事件**：确认卡链路不能把它们放进来。

---

### Task 1: A1 配额挂起额度（`holds_used`）与可见系统行

**Files:**
- Modify: `server/internal/kanban/task_store_workflow_runs.go`
- Modify: `server/internal/kanban/workflow_types.go`
- Modify: `server/internal/kanban/workflow_runtime.go`
- Modify: `server/internal/kanban/workflow_node_holds.go`
- Create: `server/internal/kanban/workflow_node_hold_notice.go`
- Test: `server/internal/kanban/workflow_node_holds_test.go`（既有夹具扩展）

**Interfaces:**
- Produces:
  - `WorkflowRun.HoldsUsed int`（json `holds_used,omitempty`）
  - `(s *TaskStore) IncrementWorkflowRunHolds(ctx context.Context, runID string) (int, error)`
  - `const supervisionMaxHoldsPerRun = 3`
  - `func (s *Service) noteQuotaHold(ctx context.Context, store *TaskStore, task Task, id, body, decisionID string)`

- [ ] **Step 1: 写失败测试**

```go
func TestQuotaHoldKeepsTaskRunningAndNotesIt(t *testing.T)     // 正常挂起：任务仍 running；存在一行含节点/时间/1/3
func TestQuotaHoldNoticeIsIdempotent(t *testing.T)             // 同一 attempt 重复写只有一行
func TestHoldsExhaustedParksTaskWithoutNewAttempt(t *testing.T) // 第 4 次不建 attempt、任务 waiting_user、有一行说明
func TestHoldsUsedSurvivesReload(t *testing.T)                 // 仓库重开后 holds_used 仍是 3
```

- [ ] **Step 2: 跑测试确认失败**（`IncrementWorkflowRunHolds` 未定义 → 编译失败）

- [ ] **Step 3: 实现**

`holds_used INTEGER NOT NULL DEFAULT 0` 加进 DDL 与 `ALTER TABLE` 列表现有循环；
`workflowRunColumns` 追加 `holds_used`；`scanWorkflowRun` 多扫一列。
`IncrementWorkflowRunHolds` 用单条 `UPDATE ... SET holds_used = holds_used + 1 WHERE id=?`
再读回（同一事务）。`createWorkflowNodeAttempt` 在 `notBefore != ""` 时：

```
n, err := store.IncrementWorkflowRunHolds(ctx, run.ID)
if n > supervisionMaxHoldsPerRun {
    // 不建 attempt：额度用完，交给人
    park waiting_user + noteQuotaHold("本 Run 的配额挂起额度已用完（3/3）…")
    return NodeRun{}, fmt.Errorf("%w: quota holds exhausted", ErrWorkflowNodeRunNotActionable)
}
// 正常挂起
noteQuotaHold("节点 X 撞到用量上限，已挂起到 HH:MM UTC，到点自动继续（本 Run 第 n/3 次）")
```

- [ ] **Step 4: 跑测试确认通过**，并复跑 `workflow_node_holds_test.go` 全部既有用例。

- [ ] **Step 5: 提交**（`fix(kanban): 配额挂起设上限并写可见系统行（Scope: G-PW）`）

### Task 2: A1 spec 订正

- Modify: `docs/superpowers/specs/2026-11-20-evidence-gated-autonomous-completion-design.md`
- [ ] §6.9 的可见性段落改为「任务保持 `running`；达到 3 次挂起额度才转 `waiting_user`
  并写一行『额度用完，需要你介入』」；[ ] 提交（`docs: 订正配额挂起的状态语义（Scope: G-PW）`）

### Task 3: 飞书卡片类型与动作字段

**Files:**
- Modify: `server/internal/channels/feishu/types.go`
- Modify: `server/internal/channels/feishu/event.go`
- Test: `server/internal/channels/feishu/types_test.go`

**Interfaces:**
- Produces:
  - `KindTarget Kind`
  - `TargetRef{NodeID, RootID, TaskID string; TaskNumber int; TaskTitle, ProjectLabel string}`
  - `Card.Target *TargetRef`、`Card.Candidates []PickerProject`、`Card.ReviewHits []string`、
    `Card.TargetMode string`
  - `Action.Fields map[string]string`

- [ ] **Step 1: 失败测试**：`ParseAction` 保留 `fields`（JSON 字符串值解码为 string）；
  `Card.Target` 在 `Chosen()` 之后仍可读；空 `fields` 不 panic。
- [ ] **Step 2: 确认变红**；[ ] **Step 3: 实现**；[ ] **Step 4: 复跑包测试**；
- [ ] **Step 5: 提交**（`feat(feishu): 目标确认卡类型与动作字段（Scope: G-PX）`）

### Task 4: 活跃目标账本（`IntakeStore`）

- Modify: `server/internal/channels/feishu/intake_store.go`
- [ ] 增加 `ActiveTarget{NodeID, RootID, TaskID, TaskNumber, TaskTitle, UpdatedAt}`
  keyed by chatID，与去重账本同文件（`active` 段），TTL 沿用 7 天；`SetActiveTarget /
  ActiveTarget / ClearActiveTarget`；新状态 `confirming / appended / switched` 可写入。
- [ ] 测试：Round-trip、过期即读不到、同一 chat 覆盖、并发的 `saveLocked` 不丢别的 chat。
- [ ] 提交（`feat(feishu): 会话级活跃目标账本（Scope: G-PX）`）

### Task 5: 任务目录与目标识别

- Create: `server/internal/channels/feishu/task_catalog.go`
- Modify: `server/internal/channels/feishu/catalog.go`（复用 `ProjectRef` 风格）
- [ ] `TaskRef{TaskID, TaskNumber, Title, Status, RootID, NodeID}`；
  `TaskFinder{List(ctx, nodeID, rootID string, keyword string) ([]TaskRef, error)}`；
  `TaskCatalog` 带 60 s TTL 与按 (node, root, keyword) 分组缓存。
- [ ] 测试：命中/未命中/大小写与 `#N` 精确匹配/最多 5 条/缓存命中。
- [ ] 提交（`feat(feishu): 任务目录与关键词识别（Scope: G-PX）`）

### Task 6: 显式确认链路

- Create: `server/internal/channels/feishu/target_confirm.go`
- Modify: `server/internal/channels/feishu/intake.go`
- Modify: `server/internal/channels/feishu/service_click.go`
- Modify: `server/internal/channels/feishu/render.go`
- [ ] `IntakeService.Handle`：识别出唯一 (节点, 项目) 后**不再立即建任务**，而是
  - 有活跃目标且本消息未带显式目标 → 直接追加（`TaskAppender.Append`）；
  - 否则 → 发 `KindTarget` 确认卡（已识别任务则预填；无则「新建」；多个候选列出来）。
- [ ] 点击：`确认` → 建/追加；`新建任务` → 新建；`切换目标` → 重新识别；
  `取消` → 关卡。全部走既有 `HandleAction` 的 owner/过期/CAS/ack 流程。
- [ ] 服务端重校验：`TaskAppender` 只认 (nodeID, rootID, taskID) 三元组都还成立。
- [ ] 测试：唯一识别→确认卡→确认建任务；已有目标→直接追加；编辑任务号→改目标；
  重投只建一个；过期卡点击被拒；非 owner 被拒。
- [ ] 提交（`feat(feishu): 显式目标确认与后续默认归属（Scope: G-PX）`）

### Task 7: 文本命令与装配

- Modify: `server/internal/channels/feishu/intake.go`
- Modify: `server/app/feishu_intake.go`
- [ ] `#123` / `切换` / `新任务` 三条命令；`TaskAppender` 在 app 层用
  `POST <node>/api/tasks/{id}/activities` 实现，`TaskFinder` 用
  `GET <node>/api/tasks?root=…`；审核分类从 app 层注入
  （`kanban.ReviewCategories`，feishu 包不 import kanban）。
- [ ] 测试：命令解析、远程节点走主控中继、审核分类命中时确认卡标注。
- [ ] 提交（`feat(feishu): 目标切换命令与生产装配（Scope: G-PX）`）

### Task 8: B3 文档与探针订正

- Modify: `docs/superpowers/specs/2026-11-20-evidence-gated-autonomous-completion-design.md`
- Modify: `server/internal/agent/conformance`（如目录已存在则补用例，否则只改文档）
- [ ] §6.12 改为「共同面 = 已归一化的 compact 事件；三种 wire 形态属适配器细节」；
  §13 C5 改为「本轮不吸收」。
- [ ] 测试：每个 harness 一条「归一化 compact 事件」契约用例。
- [ ] 提交（`docs(test): 订正跨 harness 一致性契约（Scope: G-PY）`）

### Task 9: 登记与门禁

- [ ] `docs/upstream-customizations.yaml` 新增 G-PW / G-PX / G-PY 三组，锚点逐条核对。
- [ ] `docs/plans/README.md` Active 条目。
- [ ] `go test ./server/... ./cli/...`、`make check-upstream`。
