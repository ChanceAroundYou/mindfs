# Claude 会话初始化卡死：一批任务同时卡在 waiting_user

> Scope IDs: G-SH（claimed 2026-11-27T12:57Z）

## 现场证据（2026-11-27，只读）

- 4 个任务同时停在 `waiting_user`：mindfs #132 / #133、llmux #24 / #25，
  `aux_session_error` 均为 `agent pool closed`；队列等待时长 54 min / 111 min。
- Router 会话的 agent binding 指向已不存在的转录：
  `0ab3ccea-…`、`aecbd35b-…`，磁盘上任何 slug 目录都没有对应 `.jsonl`。
- 用真实 CLI 手工复现：`claude --resume <缺失 id>` 打印 `No conversation found`
  后立刻退出，**不回答** initialize 控制请求。
- SDK 的 `waitForSDKResponse` 只在收到 control response 或 ctx 取消时返回；
  打开会话用的是 `openCtx := pool.Context()`（无 deadline），读循环又吞掉子进程退出，
  于是初始化永久阻塞。
- 每个 `{agent, protocol}` 只有一把 `sync.Mutex`：一次卡死的初始化会让此后所有
  Claude 会话打开无上限排队；直到进程退出才一起失败为 `agent pool closed`。
- 首次失败后落回新会话，但 binding 里的旧 id 从未清除，下一轮继续 resume 死 id ——
  同一个任务每回合重演。

## 根因

「缺失转录的 resume → CLI 静默退出」没有被预检；初始化没有超时；运行时锁不可取消；
失败回落不修绑定。四者叠加，单个坏会话就能拖住整个 claude-sdk 协议组。

## 设计

1. `OpenSession` 在起 CLI 之前校验 `--resume` 的转录是否存在，缺失直接返回
   `ErrResumeTranscriptMissing`。
2. `awaitInitialized` 给流初始化套 90 s 上限（父 ctx 更短时以其为准），超时取消并
   `client.Close()` 回收子进程；成功时把 cancel 交给 session，关闭时统一释放。
3. 运行时组锁改为可取消的 channel 锁；调用方 ctx 无 deadline 时给 5 min 排队上限，
   已拿到的锁在超时路径上逆序释放；关闭/清理等管理路径仍无条件等待。
4. `GetOrCreate` 的 resume 回落成功后，清空 binding 的 `agent_session_id` 与
   `agent_ctx_seq`（空 id = agent 已选定、尚无转录），下一轮重新学习新 id。