# 任务卡补齐 Router 提问标记与分级提醒

> Scope IDs: G-RW（claimed 2026-11-25T12:20Z；原 G-RT 与较早登记的跨节点修复撞号，后来者按约定让号）

> 撞号说明：原 claim 号 `G-RT` 已由较早的 `f75d75f3`（跨节点归属修复）先登记，
> 本组 `4f4d73b3` 后登记同号，按「先引入者保留、后来者让号」改为 `G-RW`；
> 提交信息中的历史 `Scope: G-RT` 保持不变。

Plan 2026-11-25。用户要求：① 由任务 Router 自行派发的 ask 目前没有卡片标记，补一个
与「会话 worker 透传 ask」不混淆、不重复的标记；② 把刚加 severity 的 info / warning /
error 三类 Router 提醒也显示在任务卡上，沿用现有提醒样式并按级别着色。

## 现状与判据

- 卡片标记统一来自服务端派生字段（`Task.RouterAlert` 是先例），前端只做展示映射，
  不重算语义；项目看板拿完整 `Task`，工作台走 `/api/tasks/overview` 的投影。
- **worker 透传 ask**：会话里的提问卡 `session_key` 非空（绑定 tool call），同时
  `aux_flags.ask_user_waiting=true`；卡片现有问号徽标读的是后者，文案
  `task.waitingUser`。
- **Router 自行派发 ask**：`applyRouteAskUser` 落 `type=question`、`status=waiting_user`、
  `session_key` 为空的行，且不碰 `aux_flags`（`OpenRouterQuestionsByTask` 已把这套
  判据用于收口，属既有口径）。因此新标记直接按同一查询派生，天然与 worker ask 分源。

## 改动

1. **后端派生字段（不落库）**
   - `Task.RouterAsk *TaskRouterAsk`（`router_ask`）：同一任务里仍
     `waiting_user` 且 `session_key=''` 的 `question` 行数、最早时间与最新问题正文。
   - `Task.LatestAttention *TaskLatestAttention`（`latest_attention`）：该任务最新一条
     非 hidden 的 `attention` 行（正文、severity、created_at）。空 severity 由前端
     归一到 info，兼容存量行。
   - 两者都在 `ListTasks` / `GetTask` 里批量 attach；查询失败按装饰字段处理，只记日志。
2. **工作台投影**：`/api/tasks/overview` 补 `router_ask` / `latest_attention`，保证看板
   与工作台同一张卡拿到同一份数据（卡片路由字段扫描表同步加入新模型文件）。
3. **前端卡片**
   - 新增纯函数 `routerAskModel.ts`（照 `routerAlertModel` 的模式），输出独立 key
     `router_ask`、独立文案键与问题正文；图标用新的 `TaskRouterAskIcon`（红色问号），
     与会话 worker 透传 ask 同一档告警色，靠形状与文案区分来源。
   - 最新提醒放在正文下方、结果摘要上方：类型胶囊复用 `activityMetaPillStyle`，正文
     颜色复用 `attentionSeverityColor`，两行截断，`data-severity` 留测试锚点。
4. **不改动**：Router ask 不写 `aux_flags.ask_user_waiting`，worker ask 不产生 `router_ask`；
   两者同时存在时各显示各的，不去重也不互斥。

## 验收

- Go：派生字段批量查询（Router ask 识别、worker ask 不误判、已回答问题消失、最新提醒
  取最新且跳过 hidden、查询失败不影响列表）；overview 投影与卡片字段对齐。
- Web：`make test-web`（tsc + vitest）；新增卡片渲染/模型契约，钉住两种 ask 的
  key、文案、图标不同，以及三种 severity 的着色与文案键。
- 收尾走 `ship` skill；本机重启仅用户执行。
