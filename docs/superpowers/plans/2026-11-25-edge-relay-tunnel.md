# 边控端反向接入（Edge Relay Tunnel）执行计划

> Scope IDs: G-RC / G-RD / G-RE（claimed 2026-11-24T18:50Z）

Plan 2026-11-25。设计正文见
[2026-11-25-edge-relay-tunnel-design.md](../specs/2026-11-25-edge-relay-tunnel-design.md)。

## 背景

用户要求：没有公网域名/端口的边缘控制端（hasee，办公室）主动拨入主控（home，外网可达），
由主控在 `/mindfs/relay/<edge-name>` 下反向承载它的 HTTP 面，其他设备自动从主控同步到这个地址。
三条裁定：①relay 不加鉴权、完全跟随主控暴露面；②名字由主控分配；③边缘自建直连域名是它自己的事。

## 已完成（本计划实施于独立 worktree `edge-relay`）

| 部分 | 内容 | 关键文件 |
|---|---|---|
| 名字 | `ValidRelayName` / `SlugRelayName` / `AssignRelayName`；`Store.SetRelayName`（唯一、幂等、拒绝未登记） | `controlsync/relay_hub.go`、`store_edges.go`、`store_migrate.go` |
| 主控 hub | `RelayHub`（注册/注销/按名借连接/旧池替换） | `controlsync/relay_hub.go` |
| 字节流 | `wsConn`：WS 消息 ↔ `net.Conn` 字节流 | `controlsync/relay_conn.go` |
| 隧道客户端 | `TunnelClient`：https-only、控制通道保活、按需数据通道、请求转发 | `controlsync/relay_tunnel.go` |
| 反代 | `NewRelayHandler`：`/relay/<name>/` → hub → `ReverseProxy`；错误码映射 | `api/relay.go` |
| 签名 | tunnel/channel 握手签名（覆盖查询串） | `api/relay_tunnel.go` |
| 装配 | `relayService`：store/hub/nodes 串联；`OnTunnelUp` 发布节点表记录 | `app/relay_service.go`、`app/control_sync.go`、`cli/cmd/control_sync.go` |
| 账户对齐 | 在线采纳 `users` 域副本到权威存储；`@primary` 依赖序；`SetPrimary` 幂等 | `controlsync/replica_apply.go`、`runtime.go`、`domain_users.go`、`auth/store_mutate.go` |
| 前端 | `/relay/<name>/` 判为跨机器 | `web/src/services/net/base.ts` |

**端到端暴露并修掉的三个真问题**（单测各自都是绿的，只有真跑一遍才现形）：

1. `dialTunnel` / `dialChannel` 把 `https://` 基址直接喂给 gorilla —— 报
   `malformed ws or wss URL`。补 `toWSScheme`（https→wss / http→ws）。
2. `relayTransport.RoundTrip` 在 `defer cancel()` 里给整条请求加了 30 分钟超时。
   `RoundTrip` 返回时响应体还没读完，defer 一执行就关掉数据通道：256KB 响应只到 36KB，
   客户端拿到 `200 + unexpected EOF`。改成不给请求加上下文超时（寿命跟客户端连接走），
   死连接交给 `ResponseHeaderTimeout`（30s，只限响应头）。
3. 主控的 `serveControlChannel` 只写不读，边控端正常下线时主控要等下一次写 ping 失败
   （30s+）才发现；这段时间 `/relay/<name>/` 借到死通道，用户看到超时而不是 503。
   改成真的读（`ReadMessage` + 90s 读超时），断开即时 `Unregister`。
4. 数据通道**用完不补**。`oneShotListener` 的第二次 `Accept` 阻塞等 `Close`，而
   `Close` 只由 `srv.Serve` 返回后才触发 —— 两边互等，`Serve` 永不返回，池子被逐条
   吃空：第 `IdleChannels+1` 个请求起永久 502/503，日志里一行都没有。单次请求的测试
   永远看不到（第一次总是成功）。改成第二次 `Accept` 立刻返回 + `closeSignalConn`
   等请求真的结束 + 每条 worker 循环重拨。`api/relay_e2e_test.go` 的
   `TestRelayEndToEndReplenishesChannels`（6 次顺序请求 > IdleChannels=2）钉住它。

## 测试（针对性，全部已通过 + 变异验证）

| 测试文件 | 钉住什么 |
|---|---|
| `controlsync/relay_hub_test.go` | 离线/忙区分、借出即交入、池满拒绝、重连替旧池、迟到注销不误杀、名字分配四规则与幂等 |
| `controlsync/relay_conn_test.go` | 多消息在字节层无缝拼接、单次写落成一条消息 |
| `controlsync/relay_lifecycle_test.go` | 一次性 listener 第二次 Accept 立即返回、关连接发信号、并发数受 MaxChannels 限制、拨号失败退避 |
| `controlsync/relay_tunnel_test.go` | https-only、必填项、缺省值补齐 |
| `controlsync/store_relay_name_test.go` | 旧库补列后空串、持久化可解析、唯一冲突、未登记拒绝、幂等、映射不含未命名 |
| `controlsync/runtime_online_users_test.go` | 在线采纳账户副本、`@primary` 在账户之后、交接同序、删除传播 |
| `auth/store_primary_test.go` | `SetPrimary` 幂等（mtime 不变）、换主账户真落盘 |
| `api/relay_test.go` | 路径穿越全拒、直通路径、错误码（404/403/503/502）、借通道失败映射、查询串保留、签名覆盖查询 |
| `api/relay_e2e_test.go` | 真实 TLS+WS+ReverseProxy 主线：前缀被剥离、查询串保留、256KB 响应体完整、下线后 503 |
| `app/relay_service_test.go` | 上线发布节点记录（基址+前缀+分配名字、`Remote`、`relay:` 前缀 id）、回落到请求基址、无基址不发、按实例幂等、下线保留记录 |
| `cli/cmd/control_sync_test.go` | `relay_name` / `relay_base_url` 从 config.json 到启动参数的映射（写反了只表现为「配了不生效」） |
| `web/tests/relay-path-same-server.test.mjs` | relay 判跨机器、普通同源、文件路径碰巧叫 relay、非法名字、异 host |

## 待办（需单独授权，未执行）

1. **配置与运维**：hasee `control_sync.relay_name` / 主控侧节点表发布核对。
2. **隔离端到端**：两个 `XDG_CONFIG_HOME` 实例（非 7331 端口）验证断连/重连/并发。
3. **部署 / 重启 / 发布**：均未执行，需用户分别授权（本机重启仅用户执行）。

## 不变量

- relay 侧不加鉴权；配置错误一律显式失败（https-only、名字唯一）。
- relay 只送请求到边缘，不复制数据面。
- 错误码语义：名字不存在=404、已踢=403、离线/忙=503、上游失败=502。
