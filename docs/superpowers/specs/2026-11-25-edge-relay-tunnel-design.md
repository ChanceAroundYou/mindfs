# 边控端反向接入（Edge Relay Tunnel）设计

Spec 2026-11-25。回答「没有公网域名/端口的边缘控制端，怎么让远端的其他设备经主控访问到它」。

## 0. 需求与已定口径

用户原话：

> 边控端这种没有暴露域名和端口的特殊设备连接到主控时，自动分配一个路径
> `https://controller.domain/mindfs/relay/<edge-name>`，主边之间挂长连接，
> 使得其他设备可以连接到它，其他设备自动从主控同步到这个访问方式。
> 当然 edge 可以给自己设置普通的域名等，如果设置了且能走通，就会优先走这个。

实际场景（用户描述）：hasee 电脑在办公室，主控在家（外网可达），手机在任何地方，
只要能连主控就要能用 hasee。

三条后续裁定：

| # | 裁定 | 落地含义 |
|---|---|---|
| 1 | **跟着主控正常暴露**，靠用户登录防范 | relay 不新加任何鉴权层；主控的 API 本来就是匿名可达（多账户是配置档不是安全边界），relay 继承同样的暴露面 |
| 2 | **主控分配名字** | `<edge-name>` 由主控决定并落库，边控端只上报候选名 |
| 3 | **直连域名是边缘自己的事** | MindFS 不实现「边缘自建域名」的配置或探活；用户在外网自己搞定。若边缘配了直连地址且前端能通，**优先走直连**（前端已有的节点 URL 机制） |

**不加登录层**（用户原话「不加 完全按主控」）。relay 是主控暴露面的透传，
安全边界与主控完全一致 —— 不引入第二套凭据、不做 relay 侧鉴权。

## 1. 结构

```
手机 ──https──> 主控 home (nginx, /mindfs)
                  │
                  ├─ /mindfs/api/...            主控自己的控制面/数据面
                  ├─ /mindfs/relay/hasee/...     ──反代──┐
                  │                                      │ 长连接（WS 多路复用）
                  │  hub.Acquire("hasee") ──tunnel conn──>│
                  │                                      ▼
                  │                              hasee 的 HTTP 面（本机 mux）
                  └─ /api/control-sync/relay/tunnel  ←── hasee 拨入（HMAC 签名）
                     /api/control-sync/relay/channel ←── 每请求一条数据通道
```

**为什么不是端口映射/VPN**：主控是唯一有公网入口的点；office 的 hasee
通常处在 NAT 后面，出站可达、入站不可达。由 hasee 主动向外拨长连接，主控
反向复用这条连接承载 HTTP 请求 —— 这是唯一不需要碰办公室路由器的形态。

**为什么走 WS 而不是裸 TCP**：主控前面的 nginx 只放行 `/mindfs/*` 的 HTTP(S)；
再用标准 `net/http` + `ReverseProxy`，WS 升级（101 之后的全双工字节流）由标准库处理，
不需要自己写帧协议。数据通道是「把一条 WS 连接包成 `net.Conn`」，
上层看到的就是一条普通 TCP。

## 2. 组成

| 组件 | 位置 | 职责 |
|---|---|---|
| `RelayHub` | `server/internal/controlsync/relay_hub.go` | 主控侧：记住在线 edge、每台一个空闲通道池、`Acquire` 借一条连接 |
| `wsConn` | `server/internal/controlsync/relay_conn.go` | 把 gorilla WS 连接包成 `net.Conn`（消息边界 → 字节流） |
| `TunnelClient` | `server/internal/controlsync/relay_tunnel.go` | 边控端侧：拨控制通道、保活、按需拨数据通道、把请求转给本机 handler |
| `NewRelayHandler` | `server/internal/api/relay.go` | 主控侧：`/relay/<name>/` → 查 hub → `ReverseProxy` 到借到的连接 |
| tunnel/channel handler | `server/internal/api/relay_tunnel.go` | 主控侧：控制通道与数据通道的签名校验与接管 |
| `relayService` | `server/app/relay_service.go` | 装配层：把 store/hub/nodes/prefs 串起来，发布节点表记录 |
| 名字分配 | `controlsync.AssignRelayName` / `Store.SetRelayName` | 主控分配、唯一、幂等、落库 |
| `edges.relay_name` | `store.go` + `store_migrate.go` | 新增列，旧库补列后为空串 |

## 3. 关键约束（实现时不能破）

1. **名字唯一且稳定**：`SetRelayName` 拒绝重复与未登记 edge（`ErrRelayNameTaken` / `ErrEdgeNotFound`）。
   地址已发到其他设备的节点表里，重连/重启主控都不能改名。
2. **握手/通道一律签名**：复用既有 HMAC 同步方案（`X-MindFS-Timestamp/-Nonce/-Signature/-Instance`，
   5 分钟时钟偏差，nonce 防重放）。数据通道签名覆盖规范化查询串。
3. **隧道必须 https**：`NewTunnelClient` 直接拒绝 `http://` 的 `primary_url`（可选 CA 文件）。
4. **relay 侧不加鉴权**：透传主控暴露面（用户裁定）。
5. **借到的必须就是那条**：`Acquire` 交出的连接就是 `Attach` 交进来的那条；一条数据通道只服务一次请求。
6. **离线/忙可区分**：无隧道 → `ErrRelayOffline`(503)；池空 → `ErrRelayBusy`(503)；名字不存在 → 404；已踢 → 403。
7. **路径穿越全拒**：`splitRelayPath` 拒绝 `..`、`%2e/%2f/%5c`、反斜杠、空段、非法名字（大小写/下划线/超长）。
8. **重连替换旧池**：`Register` 关掉旧池里的空闲连接，避免请求派到死连接；迟到的 `Unregister` 不能误杀新隧道。

## 4. 账户对齐（为什么必须在线采纳 users 域）

经 `/relay/<name>/` 进来的请求带的是**主控那边的账户**（跨机器只能对齐用户名，
`auth.Resolve` 认 id 或用户名）。hasee 自己的账户表若只有本地 `admin`，
`Workspace("xiaokubao")` 会落到 `emptyWorkspace()` —— 页面打得开、内容全空。

因此在线追平时把 `users` 域副本写进本机权威存储（`applyOnlineUsers`），
并与离线 `Handover` 共用 `applyReplicaDomain`：

- 按域依赖序应用（`sortRecordsForApply`）——`@primary` 排在账户之后（`ListRecords` 是 key 升序，
  `@primary` < 任何用户名，盲写会 `user_not_found`）。
- `pending` 记录跳过（本地改过、还没回传）。
- 在 `Suppress` 内执行（否则采集器把远端写入当成本地改动再推回去）。
- 远端 `@primary` 指向本机不存在的账户时不强应用（保留本地主账户），避免每拍都报错。
- `SetPrimary` 幂等（已是主账户不落盘），否则每 5s 重写 `users.json`。

## 5. 前端

`web/src/services/net/base.ts` 的 `isSameServerAsPage` 把 `/relay/<name>/` 判为**跨机器**：
请求最终落到另一台机器，账户表是那台机器自己那份；判成同源会发本机账户 id，
对方查无此人。`isRelayPath` 只认第 0 或第 1 段（部署前缀后）的 `relay`，
名字必须匹配 `[a-z0-9][a-z0-9-]{0,31}` ——「文件路径里恰好叫 relay 的目录」不算。

## 6. 不做的

- 不做 relay 侧登录/鉴权（用户裁定）。
- 不做边缘自建域名的配置或探活（边缘自己的事，见裁定 3）。
- 不做端口映射、不做 VPN、不碰办公室路由器。
- 不复制数据面（项目/会话/任务/文件/agent 池）——relay 只是把请求送到边缘，
  数据仍在那台机器本地。

## 7. 验证口径

- 单元：hub（acquire/busy/offline/replace/unregister）、`wsConn`（消息拼接、单条写）、
  名字（valid/slug/assign/store 唯一/幂等/迁移补列）、路径（穿越全拒）、
  `TunnelClient`（https-only/必填项/缺省值）、api 错误码映射、前端 relay 判跨机器。
- 每条护栏做变异验证（改坏必须红）。
- 端到端用隔离实例（两个 `XDG_CONFIG_HOME`、非 7331 端口，不碰生产），见计划。
