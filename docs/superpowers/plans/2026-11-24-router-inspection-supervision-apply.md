# Router 巡检回合放行 notify_user / escalate

> Scope IDs: G-QM（claimed 2026-11-23T18:06Z）

## 背景

交接审核发现生产现场两类决策失败：

- `rdec_47d5992f117dc51b`：`turn_interrupted` 回合产出 `escalate`，应用层以
  `requires an originating user activity` 拒绝。
- `rdec_04816ea01`：`turn_interrupted` 回合产出 `notify_user`，同样被拒。

上层 `validateRuntimeEventActions` 本来就允许这两个动作，`applyRouteNotifyUser`
与 `applyRouteEscalate` 的签名也不接收 `userAct`。真正的阻断点只有
`activity_route_apply.go` 里「无来源留言巡检回合只允许 say / ask_user」的闸门。

`bd9b4b2f` 加的 `route-rejected:<decision>:<seq>` 只让拒绝可见，不是修复。

## 修复

把 `applyRouteAction` 的无来源留言白名单扩到 `notify_user` / `escalate`；其余动作
（起 Run、投递、收尾等）仍需要来源留言。`notify_user` / `escalate` 依旧只在
上层确认存在监管观测块后才可进入应用层，配置 `router.supervision.mode=off`
时不改变行为。

## 验收

- RED：无来源留言的 `turn_interrupted` 事件分别产出 `router_notify_user` /
  `router_escalate`，决策必须 `applied`，库里必须出现
  `route-notify:<decision>:0` / `route-escalate:<decision>:0` 的 attention 系统行。
- 变异：把白名单恢复成只有 say / ask_user，两条用例都必须重新变红。
- GREEN：定向用例与 `go test ./server/internal/kanban/ -count=1` 全绿。

## 边界

只改本地代码与测试；不部署、不重启任何节点，不碰生产数据库。飞书凭据与长连接仍只在主节点；
worker 卡片经主控 `/api/channels/feishu/relay` 代发，点击回传按卡片 `node_id` 由主控转发，
绝不按请求体 URL 转发。
