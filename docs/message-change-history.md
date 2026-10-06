# 消息修改史 ↔ bug 对照（第二步）

> 目标：把所有跟「会话消息」相关的历史改动挖出来，标注每个改动**针对的现象**与**判定根因**，
> 并回答一个问题：**同一个现象是不是被修过很多次、而每次判的根因都不一样？**
> 那正是「多个机制在打架」的历史证据。
>
> 配套：`docs/message-mechanisms.md`（第一步机制清点）、`docs/session-streaming-rework.md`（协议层论证）。
> 状态：表 A / 表 B 已完成；§3 是第二步对第一步的回补；§4 是红测试清单。

## 0. 方法

1. `git log --oneline` 全量扫，按关键词筛（消息/流式/重放/渲染/正文/重复/会话/session/stream/replay/exchange/seq/overlay/窗口/window/transient/瞬时/done/reconnect/同步/导入）
2. `docs/upstream-customizations.md` 的消息相关组（G-T 流式重放与正文正确性、G-G 会话生命周期与 WS、G-F 前端流式与渲染性能、G-R）
3. `docs/session-streaming-rework.md`、`docs/session-flicker-root-cause.md`
4. 每个 `web/tests/*.test.mjs` 与 `server/internal/api/*_test.go` 的**注释** —— 它们记录了当年的事故与实测数据
5. 未提交的工作区改动（`git status` / `git diff`）

**核对强度**：表 A 的 12 行由我逐条 `git log -1 <hash>` 核对（日期 + 主题行一致），标 ✅；
其余行来自逐提交扫描，我未逐条复核，标 ⚠️。被引用的测试文件我全部核对过是否存在（11/11 存在）。

## 1. 表 A：改动 ↔ bug 对照

| 核对 | 提交 | 日期 | 针对的现象（用户看到什么） | 判定根因 | 动的链路 | 现在还在不在 |
|---|---|---|---|---|---|---|
| ⚠️ | `bd52027` | 08-19 | 会话结束时「正在生成」卡住 | `message_done` 未立即清流式标记 | 生命周期 | 在 |
| ⚠️ | `8bc5a43` | 08-19 | 停止按钮有时失效 | 单读 goroutine 同步派发阻塞 + Interrupt 无超时 | 生命周期 | 在（CLAUDE.md 架构事实 1/2） |
| ✅ | `d36cc53` | 08-20 | 重连/刷新后丢掉在途内容 | 引入「按 `event_cursor` 续流」协议 | 接收分发 / 生命周期 | **已反转**（工作区删游标，改快照重建） |
| ⚠️ | `7f8a8c6` | 08-21 | 会话历史同步滞后 | `loadSessions` 时序 | 缓存 | 在 |
| ⚠️ | `9df43e9` | 08-21 | 多项目列表实时更新失效、pending 草稿不出现 | 无请求序号竞态守卫 | 缓存（列表） | 在 |
| ⚠️ | `2e85356` | 08-17 | 大会话卡顿 / WS 重拉风暴 | 全量读盘 + 无 debounce | 缓存 / 性能 | 在 |
| ⚠️ | `df445bd` | 08-17 | 流式渲染风暴 100/s；context_window 徽标闪没；结束后列表漏更新 | 每 chunk setState；done 走增量漏刚结束会话 | 渲染 / 缓存 / 生命周期 | 在 |
| ⚠️ | `ebcfc39` | 08-29 | 长会话一次性全量加载 | 无窗口分页 | 缓存（窗口） | 在（引入 `SESSION_WINDOW_SIZE` / `?latest=`） |
| ⚠️ | `faf35bd` | 09-09 | **长会话流式内容不显示**；列表新会话缺口 | 窗口 seq 过滤挡在途（seq=0）；无重锚定 | 渲染 / 排序 / 生命周期 | 部分反转（改由 reset / `dropTransientExchanges` 处理） |
| ⚠️ | `06f8ea4` | 09-09 | 双击渲染 / 切窗重复 | 双源 seq 合并缝合 | 渲染 | 在（方案 C） |
| ⚠️ | `ea8b394` | 09-09 | 会话完成后**瞬时轮次重复显示** | 视图内重拉，App 缓存残留 seq=0 被追加 | 渲染 / 生命周期 | 在 |
| ✅ | `460cb2b` | 09-10 | 用户消息渲染两次；流式不实时 | 服务端不下发用户消息 seq ⇒ 乐观项与窗口项各渲染一次 | 广播 / 缓存 / 渲染 | 在 |
| ⚠️ | `4ebafb9` | 09-10 | 切走期间完成的轮次**显示两次** | 窗口（seq>0）与 overlay（seq=0 陈旧拷贝）同屏 | 渲染 / 缓存 | **已被替换**（`460cb2b` 撤位次裁剪） |
| ⚠️ | `9578d12` | 09-11 | 同一段助手文本重复（截图出现 3 次） | 转录重复区块 uuid 未去重 | 导入判重 | 在（`seenUUIDs`） |
| ⚠️ | `abaf42a` | 09-13 | 用户气泡出现**两条**、刷新后仍在 | 两个写入者判重不对称 | 导入判重 | 在（`exchangeAlreadyRecorded`） |
| ✅ | `1a9fefb` | 09-13 | **重放风暴自持闭环**（18 次/秒、145 条 done）；工具卡重复渲染 + React 重复 key；往下加载闪一下又掉回 | `done(replay)` → reload → ready → done 成环；同 callId 两来源各渲染；loadMore 未 keepOlder | 生命周期 / 渲染 / 缓存 | 环已消除（工作区删 `completed` 表 + `done(replay)`）；去重在 |
| ✅ | `543fdae` | 09-13 | 点同步后 **ask 卡还在、直播正文消失** | `handleSyncSession` 补瞬时条目**只保留 `role=tool`**（实测 cacheBeforeTransient=4 只保住 1） | 缓存 / 渲染 | 在 |
| ✅ | `5ab8e9e` | 09-13 | ①同步丢 ask 卡 ②卡片标题被子代理报告顶替 ③刷新后旧内容当当前 | pending ask 卡纯内存态、sync 用 IDB base 覆盖；summary 当 title；`_windowMeta/_anchoredAt` 随 IDB 落盘 | 缓存 / 渲染 / 生命周期 | 在 |
| ⚠️ | `e741bac` | 09-13 | 重放**历史尾段** | 外部同步无幂等护栏、`ctx_seq` 不刷新 | 导入判重 / 生命周期 | 在 |
| ⚠️ | `03d4165` | 09-14 | **时间倒挂气泡**（12:34 后面跟 12:26）；脚手架并进真人消息 | 导入器未按 `isMeta` 丢 CLI 自注入条目 | 导入判重 / 排序 | 在 |
| ⚠️ | `04f4ec5` | 09-14 | 「同一条助手消息**两条气泡**」 | 双写快照长度不同（21 vs 556 字），只认完全相等 | 导入判重 | 在（前缀容忍 + ≥16 字闸） |
| ⚠️ | `43f4cf1` | 09-14 | 只差空行的**重复气泡**（1765 vs 1761 字） | `\n\n` 插进粗体标记内，前两类判据放行 | 导入判重 | 在（抹空白后相等） |
| ⚠️ | `1c9a979` | 09-14 | 「**用户气泡里装着助手正文**」（尾部 10704 字，全库 162 条） | 子代理完成通知是 `role=user` 整块条目，噪声名单漏了 | 导入判重 | 在 |
| ⚠️ | `89bfa88` / `bf23b66` | 09-14 | 「SYSTEM NOTIFICATION」输成用户消息；旧 prompt 又出现在回复后 | 导入噪声名单 / 中断标记 | 导入判重 | 在 |
| ⚠️ | `8652f28` | 09-15 | 手工改文件后消息不显示（seq 撞号 / 空洞） | seq 按「缓存长度 + 1」分配 | 排序 | 在（改 `max+1`） |
| ⚠️ | `52e1e16` | 09-15 | 双写（时间差 6.4/15.6/36.7 s 全漏网） | 未定义「谁拥有会话持久化」 | 导入判重 | 在（live-owned 判据） |
| ⚠️ | `ff012c9` | 09-15 | 双写重复行 | 写入侧只能猜，读取侧可证明折叠 | 导入判重 / 渲染投影 | 在（`MINDFS_SESSION_PROJECTION=off` 可关） |
| ⚠️ | `a61bca7` | 09-15 | 老会话（无 source 标注）重复折叠不掉 | 规则只认显式 source | 导入判重 | 在（`ExchangeHasLiveSignature` + 快照包含） |
| ✅ | `04ce7a7` | 09-17 | **ask 上下两块同样的分析文本** | 窗口已含落盘正文，缓存 seq=0 副本未让位 | 渲染 | 在 |
| ✅ | `f4efd0c` | 09-17 | 20:16 **仍重复**（刚落盘的 seq=2 未被重锚定拉进窗口，由 overlay seq>0 分支渲染） | 比对语料只有窗口 `visibleExchanges` | 渲染 | 在（`renderedPersistedTexts` 预扫描） |
| ✅ | `75da52e` | 09-17 | **ask 上下各渲染一遍**（重开会话必现，PC held-ask） | 重锚定把整份缓存（含 seq=0）当窗口装 ⇒ 同时进窗口态与 overlay | 渲染 / 生命周期 | 在（锚点只装 seq>0 + 对象同一性差集） |
| ⚠️ | `daee86b` | 09-28 | 长会话渲染卡（3000 工具卡） | 全量挂载、每 chunk 重挂 Markdown 子节点 | 渲染 / 性能 | 在（虚拟化） |
| ✅ | `4673ee4` | 09-28 | **回合执行中用户自己那条消息从列表消失**，点同步也刷不回来，回合结束才自愈 | user 行落盘太晚（回合末），广播的是预测 seq=base+1 | 发送 / 广播 / 排序 | 在 |
| ⚠️ | `8b4330d` | 09-28 | （`4673ee4` 配套）同一份内容重复渲染 | `pendingUser` 旁路前提消失后仍重渲 | 缓存 / 广播 | 在（旁路删除） |
| ⚠️ | `9ab9572` | 09-29 | **回复中整页闪动** | 每 chunk 改变 Markdown `components` 组件类型 ⇒ 未变段落被重挂载而非更新 | 渲染 | 在 |
| ⚠️ | `5b936f4` | 09-29 | 交换 seq 号重复 | exchange seqno 去重 | 排序 | 在 |
| ✅ | `1802d52` | 10-05 | 切会话先显示**上一个会话**消息 → 整块塌成空白 → 0.8 s 后新内容才回来 | 两处 `<SessionViewer>` 无 `key`，`visibleExchanges`（useState）跨会话复用 | 渲染 | 在 |
| ✅ | `858c3e7` | 10-06 | 切回**运行中未结束**会话：完整对话 → 塌到最后一条用户 prompt → 一点点补回来。已结束会话不闪 | 加载时丢掉在途内容（seq=0）；该规则写 4 遍、**3 遍去服务端回包找瞬时行**（恒空） | 缓存 / 渲染 | 在（收敛为 `composeLoadedExchanges` 单一规则） |
| ✅ | `495f3b5` | 10-06 | 「文本出现两遍、**越切越多**」（行内 `aabb`）；切会话**逐渐刷一大堆**；跨 7 次 compact 堆 1100+ 瞬时行 | ①正文无条件拼接（重放重推）②`clearEventCursor` 主动要全量重放 ③服务端逐条发事件 ④seq=0 无清理出口 | 接收分发 / 缓存 / 渲染 | **第一轮被工作区第二轮反转**：`mergeStreamedText`/`eventCursors` 已删（实测 HEAD 9 处 → 工作区 0 处） |
| ⚠️ | **工作区·第二轮协议反转** | 10-06 | 「切走再切回，ask 卡下面多出一整份助手正文」（自带工具卡，`data-session-seq="19"`，尾部 DOM 137→138）；「文本（含工具）→ ask → 文本（无工具）」 | ①`inferredSeq` 按位置编造 seq ⇒ 认领真持久行的 `exchange_aux` ②`appendAgentChunkForSession` 按位置合并，重放批次末条是 ask 卡 ⇒ 新开一行 | 接收分发 / 缓存 / 渲染 / 排序 | **工作区（未提交）** |
| ⚠️ | **工作区·第二轮完成交接** | 10-06 | 「进行中的 assistant message 在**正在完成的时候会瞬间全部消失，只剩最后一条 user msg**，切走再回来又能刷出来」 | `emitDecrypted` 先 `emit` 后 `updateActiveStreamState` ⇒ 监听者读到上一帧状态（恒 true）⇒ done 的重锚定门恒假；而同一 handler 更早已无条件清尾巴 ⇒ 清在前装在后 | 生命周期 / 缓存 / 渲染 | **工作区（未提交）** · 真机已复现：137 项 → 2 项 |

## 2. 表 B：反复出现过的症状（这是「打架」的历史证据）

| 症状 | 次数 | 每次判的根因一致吗 | 现有测试 |
|---|---|---|---|
| **「ask 卡下面多出一堆文本」/「ask 上下各一整块」** | ≥5（09-11、09-17×3、10-06） | **否**。09-11 判成导入 uuid 重复；09-17 三次是「窗口态 vs 瞬时 overlay 重叠」的三种形态（已含未让位 / 未重锚定 / 重锚定把瞬时行当窗口装）；10-06 换成「按位置合并 + `inferredSeq` 认领别人 aux」。**共同底层都是「同一轮两份表示，不知看哪份」** | `session-window.test.mjs` ⑦⑧⑨⑩、`session-window-overlay-dedup.test.mjs`、`session-timeline-seq.test.mjs` |
| **「文本a（含工具），ask，文本a（无工具）」** | 1（10-06） | 是（唯一）：`inferredSeq` 编造 seq ⇒ `exchange_aux["19"]` 被认领 | `session-timeline-seq.test.mjs` |
| **「同一段正文两遍、越切越多」** | ≥4（09-10、09-11、10-06 行内 `aabb`、compact 堆积 1100+） | **否**。行内 `aabb`=重放重投递 + 无条件拼接；compact 堆积=seq=0 无清理出口；09-10=乐观项与窗口项；09-11=导入重复 | `session-window.test.mjs`（含 `mergeStreamedText===undefined` 反向断言）、`stream_hub_user_message_test.go` |
| **「切回运行中会话塌成最后一条用户消息，再慢慢补回」** | ≥3（10-05、10-06 `858c3e7`、10-06 工作区） | **否（前几次都错）**。10-05 判成「未重挂载 / seed 未就绪」；`858c3e7` 更正为「丢在途内容，规则写 4 遍 3 遍错」；工作区再加「done 交接非原子 + 重锚定门恒假」。**前几次拿已结束会话复现 ⇒ 条件选错** | `session-switch-flicker.test.mjs`（其注释明确说它不是根因）、`session-window.test.mjs` ②、`session-done-replay-no-reanchor.test.mjs` |
| **「完成瞬间正文消失，只剩最后一条 user msg，切走再回来又刷出来」** | 1（10-06，真机复现） | 是：done 先无条件清尾巴、再用 `isSessionStreaming` 猜重锚定，门恒假 | `session-done-replay-no-reanchor.test.mjs` |
| **「重放自持闭环」** | ≥2（09-13 18 次/秒、10-06 80 条 done/78 次 `?latest=20`） | 是：服务端补发 `done(replay:true)` + 客户端据此再重锚定 | `ws_test.go`（`TestDoneCarriesNoReplayReceipt` 等 4 条）、`session-done-replay-no-reanchor.test.mjs` |
| **「点同步后正文 / ask 卡消失」** | ≥2（09-13×2） | 是：sync 以 IDB 为 base 重建，丢掉纯内存瞬时条目 | `sync-preserves-live-transient-text.test.mjs`、`sync-preserves-pending-ask-card.test.mjs` |
| **「用户自己那条消息消失 / 渲染两次」** | ≥2（09-10、09-28） | **否**。09-10=缺 seq 致乐观项与窗口项重复；09-28=落盘太晚致窗口读不到 | `stream_hub_user_message_test.go`、`user-message-persist-timing.test.mjs` |
| **「重复气泡 / 双写」** | ≥6（09-11 ~ 09-15） | **否**。09-11=转录 uuid 重复；09-13~14=两个写入者判据不足；09-15 才从「所有权判据 + 读取侧投影」收口 | `http_window_test.go`（投影折叠）等 |
| **「会话切换闪动」** | ≥3（09-29、10-05×2） | **否**。09-29=Markdown 组件每次 chunk 换类型（整页闪）；10-05=SessionViewer 无 key；另一次=置顶快照迟到 | `session-switch-flicker.test.mjs`、`session-virtualization.tsx`、`pins-authority.test.mjs` |

**表 B 读出来的三件事：**

1. **10 个反复出现的症状里，6 个的根因判定前后不一致** —— 这不是「同一个 bug 修不干净」，
   是**每次都在不同的一层上看见它**（导入层 / 广播层 / 缓存层 / 渲染层各看一次）。
2. 最贵的那个症状（ask 卡下的重复正文）被修了 ≥5 次，**每次的根因判定都不同**，
   而每次都留下了补丁：这就是第一步数出来的那 5 处内容比对启发式的来历。
3. 「拿已结束的会话复现运行中会话的 bug」这种**复现条件选错**至少发生过两次
   （10-05 与 `858c3e7` 之前），这解释了为什么根因判定会漂移。

## 3. 第二步对第一步的回补（回补项）

第一步只清了「会话消息」链，第二步暴露出**一条前置链路**：**外部转录导入判重**。
它产生的重复行会以「同一段正文两遍」的形态混进渲染问题里，两者症状相同、层次不同。

| | 机制 | 位置 | 作用 | 独特性 |
|---|---|---|---|---|
| ⚠️ | `seenUUIDs` | `server/internal/agent/claude/importer.go` | 按转录 uuid 去重 | 同一区块被写两次是 CLI 的现实 |
| ⚠️ | `isMeta` 过滤 | `importer.go`、`agent/types/types.go` | 丢掉 CLI 自注入条目（脚手架/SYSTEM NOTIFICATION） | 只有导入器知道哪些是注入 |
| ⚠️ | `exchangeAlreadyRecorded` | `api/usecase/session.go`、`api/http.go` | 写入侧「这条是不是已经记过」 | 前缀容忍 + ≥16 字闸（长度 21 vs 556 的教训） |
| ⚠️ | `ExchangeHasLiveSignature` | `session/types.go`、`usecase/session_projection.go` | 「这条有实时写入者的签名」 | 老会话没有 source 标注 ⇒ 只能靠签名 |
| ⚠️ | `ProjectExchanges` / `SessionProjectionEnabled` | `usecase/session_projection.go`、`api/http.go` | **读取侧**折叠同期重复行（不删文件） | 写入侧只能猜，读取侧可证明 |
| ⚠️ | `ctx_seq`（外部同步幂等护栏） | `session/manager.go`（`RepointAgentBinding` 同事务） | 防重导已渲染过的回合 | 是 Full 同步的幂等判据 |

**回补结论**：这 6 个机制**不在**第一步的清点里，但它们与渲染问题**症状重叠**
（都表现为「同一段正文出现两遍」）。因此第三步做「重复正文」这一族时必须把两层分开验证：
**是导入层写了两行，还是渲染层把一行渲染了两次？** —— 判法是用 seq 去查库：
`grep` 会话 JSONL，出现两次即导入层，只出现一次即渲染层。

## 4. 「一定会红」的测试清单（第二步要求）

**已完成：23 条红 → 全部转绿**（2026-10-06）。逐条的改法与红色依据见
`docs/message-mechanisms.md` §6；测试分层：

| 层 | 载体 | 条数 |
|---|---|---|
| 行为（单元） | `session-core-unit` · `session-overlay-unit` | 22 |
| 删除契约 / 不变量（源码结构，其中 3 条**自动推导**计数） | `message-mechanism-merges` | 15 |
| 契约（既有，随改动重定向） | `session-window` · `upstream-restore` · `sync-preserves-*` · `session-timeline-seq` 等 | — |
| E2E（隔离实例 + CDP） | `e2e-message-behavior` | 4 绿 + 1 带原因跳过 |

**仍未覆盖的**（见 `docs/message-mechanisms.md` §6.4）：两泳道结构升级、冲突⑪ 的行为测试、
导入判重那 6 个机制本身。

## 5. 结论

1. **同一个症状被修过很多次、而每次判的根因都不一样** —— 10 个反复症状里 6 个如此。
   这就是「多个机制在打架」的历史形态：不是修不干净，是**每次都在不同的一层上看见它**。
2. 表 A 里有 **11 行动的是「导入判重」**，它们与渲染层的重复**症状相同**。
   第一步漏了这条链路，已回补（§3）。**第三步必须能区分这两层**，否则会继续在渲染层
   给导入层的重复打补丁（历史上前 4 次「重复」修的就是这个错位）。
3. 工作区的两轮改动（协议反转 + 完成交接）**都未提交**，且线上处于混合态
   （旧后端二进制 + 新前端 dist）。R-3 类端到端验证在被授权的隔离实例上做。
