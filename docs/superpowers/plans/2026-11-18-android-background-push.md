# 安卓后台回复提醒：常驻通知可点 + 近实时推送 · 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 安卓壳后台从「每 10s 串行轮询」升级为服务端长轮询的近实时推送，并让常驻「会话回复中」通知点击可进 App（单会话时直达）。

**Architecture:** 服务端新增 `GET /api/replying-sessions/wait`——形状（会话集合/状态/等回答）一变立即回包，空闲超时返回，复用 ETag/304。安卓前台服务改为「每节点一条等待循环」：拿到回包立刻再发下一次；失败/旧服务端退回普通轮询。常驻前台通知补 `PendingIntent`，复用现成的 `mindfs:open-reply-session` 深链。

**Tech Stack:** Go 1.26（chi + gorilla/websocket 既有栈）、React/TS（Vite + Vitest）、Android Java（Capacitor 壳，HttpURLConnection、前台服务）。

**Spec:** `docs/superpowers/specs/2026-11-18-android-background-push-design.md`

## Global Constraints

- Go 工具链在 `$HOME/.local/share/go`，裸跑 `go` / `make test`，**不要** `export PATH=...`。
- 提交信息：Conventional Commits（中文描述），本计划所有提交带 `Scope: G-MF`（Task 0 创建该组）。
- 工作区里有别人的未提交改动（`e2e/live/selection_scope.py`、`web/src/components/shell/PanelShell.tsx` 等）：**只 add 本计划涉及的文件**，禁止 `git add -A` / `git checkout` 撤改动。
- 服务端每次改动后跑 `go test ./server/...`；前端跑 `cd web && npm run typecheck && npm test`；安卓跑 `cd web/android && ./gradlew :app:assembleDebug`（以及 `make test-android`）。
- 本机 `sudo systemctl restart mindfs` 只能由用户执行；WSL/fn 的 user 单元可 `ssh <node> 'systemctl --user restart mindfs'`。
- API 层鉴权是用户明确决策，本计划**不**引入鉴权；只加在途上限。
- 等待端点默认超时 20s（夹 5–30），轮询粒度 200ms，摘要刷新 ≥5s，节点快照新鲜度 90s。

## Review Focus

最可能被踩到、而任务测试必须钉住的行为：

1. **代理/网络掐断长连接**：连续失败要退回普通轮询并在 10 分钟后重试，不能变成「提醒静默停更」或请求风暴。
2. **回合在两次刷新之间开始又结束**：客户端必须先播种（首帧立即返回），否则完成提醒丢失。
3. **节点掉线**：其会话不能被常驻通知永远算作「回复中」，也不能被点进一条早已结束的会话（90s 新鲜度）。
4. **多会话同时结束**：每会话一条通知、互不覆盖；常驻通知文案显示正确数量。
5. **点击常驻通知**：恰好 1 个会话时直达该会话；0 或多个时只打开 App，不乱跳。

---

### Task 0: 登记计划与定制组

**Files:**
- Modify: `docs/plans/README.md`（Active 段加一条）
- Modify: `docs/upstream-customizations.yaml`（新增 `G-MF` 组，files/tests/anchors 随后续任务补齐后回填）

**Interfaces:**
- Produces: 组号 `G-MF`，后续所有提交用它。

- [ ] **Step 1: 在 `docs/plans/README.md` 的 Active 列表顶部加一条**，指向本计划与 spec，状态写「执行中（2026-11-18 批准）」。
- [ ] **Step 2: 在 `docs/upstream-customizations.yaml` 末尾新增 `G-MF` 组**，`kind: feature`，`title` 写「安卓后台提醒改为服务端长轮询 + 常驻通知可点 + FGS 类型改 specialUse」，`files:` 至少列出本计划将改的 8 个文件（server/internal/api/http_sessions_list.go、http_sessions_replying_wait.go、http_routes.go、stream_hub.go、server/app/server.go、web/src/services/net/replyPoller.ts、web/android/app/src/main/java/com/mindfs/app/ReplyPollerService.java、ReplyStateMerger.java、AndroidManifest.xml），`tests:`/`anchors:` 先留空数组，Task 8 回填。
- [ ] **Step 3: 提交**

```bash
git add docs/plans/README.md docs/upstream-customizations.yaml docs/superpowers/specs/2026-11-18-android-background-push-design.md docs/superpowers/plans/2026-11-18-android-background-push.md
git commit -m "docs(plan): 登记安卓后台提醒近实时推送计划（Scope: G-MF）"
```

---

### Task 1: 服务端排序 —— `/api/replying-sessions` 顺序确定

**Files:**
- Modify: `server/internal/api/stream_hub.go:167-196`（`ListReplyingSessions`）
- Test: `server/internal/api/stream_hub_order_test.go`（新建）

**Interfaces:**
- Produces: `ListReplyingSessions()` 按 `SessionKey` 升序；`/wait` 的签名与 ETag 依赖这条。

- [ ] **Step 1: 写失败测试**

```go
func TestListReplyingSessionsSortedBySessionKey(t *testing.T) {
	hub := NewStreamHub()
	for _, key := range []string{"s-c", "s-a", "s-b"} {
		hub.SetPendingReply("root-1", key, key)
	}
	first := hub.ListReplyingSessions()
	second := hub.ListReplyingSessions()
	if len(first) != 3 {
		t.Fatalf("len=%d", len(first))
	}
	for i, want := range []string{"s-a", "s-b", "s-c"} {
		if first[i].SessionKey != want {
			t.Fatalf("first[%d]=%q want %q", i, first[i].SessionKey, want)
		}
	}
	for i := range first {
		if first[i].SessionKey != second[i].SessionKey {
			t.Fatalf("顺序不稳定：%v vs %v", first, second)
		}
	}
}
```

- [ ] **Step 2: 跑测试确认红**

Run: `go test ./server/internal/api -run TestListReplyingSessionsSortedBySessionKey -count=3`
Expected: FAIL（map 顺序随机，`-count=3` 很容易撞出非升序）

- [ ] **Step 3: 实现**

在 `ListReplyingSessions` 返回前加 `sort.Slice(items, func(i, j int) bool { return items[i].SessionKey < items[j].SessionKey })`，并补 `sort` import。注释写明：顺序稳定是为了 ETag/304 与等待循环签名可比。

- [ ] **Step 4: 跑测试确认绿**

Run: `go test ./server/internal/api -run 'TestListReplyingSessions' -count=3`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add server/internal/api/stream_hub.go server/internal/api/stream_hub_order_test.go
git commit -m "feat(api): 在回复会话列表按 sessionKey 稳定排序（Scope: G-MF）"
```

---

### Task 2: 服务端 —— `GET /api/replying-sessions/wait`

**Files:**
- Create: `server/internal/api/http_sessions_replying_wait.go`
- Modify: `server/internal/api/http_sessions_list.go:158-207`（抽出载荷构造函数）
- Modify: `server/internal/api/http_routes.go:80`（注册路由）
- Test: `server/internal/api/http_sessions_replying_wait_test.go`（新建）

**Interfaces:**
- Consumes: `AppContext.GetSessionStreamHub()`，`StreamHub.ListReplyingSessions()`（Task 1 的排序）。
- Produces:
  - `func (h *HTTPHandler) handleReplyingSessionsWait(w http.ResponseWriter, r *http.Request)`
  - `func (h *HTTPHandler) replyingSessionsPayload(r *http.Request, items []ReplyingSessionState) []map[string]any`（`handleReplyingSessions` 也改用它，行为不变）
  - 包级测试缝：`replyingWaitPollInterval = 200 * time.Millisecond`、`replyingWaitRefreshInterval = 5 * time.Second`、`replyingWaitInFlight int32`
  - 常量：`replyingWaitDefaultTimeout = 20 * time.Second`、`replyingWaitMinTimeout = 5 * time.Second`、`replyingWaitMaxTimeout = 30 * time.Second`、`replyingWaitMaxInFlight = 32`
  - `func replyingShapeSignature(items []ReplyingSessionState) string`（`sessionKey|status|awaiting` 按 Task 1 顺序拼接）
  - `func replyingPayloadSignature(items []ReplyingSessionState) string`（再加 rootTitle/sessionTitle/summary/updatedAt）

**端点契约：**

- 查询参数 `timeout`（秒，缺省 20，夹 5–30）。
- **无 `If-None-Match` → 立即 200**，正文 `{"sessions":[...]}`，带 `ETag`（播种）。
- **有 `If-None-Match` → 等待**，直到：形状签名变化；或距请求开始 ≥ `replyingWaitRefreshInterval` 且载荷签名变化；或超时。然后 `respondJSONConditional`（命中则 304）。
- 请求上下文取消 → 立即返回、不写响应。
- 在途 > 32 → 503 + `Retry-After: 3`。

- [ ] **Step 1: 写失败测试**（新文件，`app := &AppContext{Dirs: registry}`，`h := &HTTPHandler{AppContext: app}`，直接调 handler）

```go
func TestReplyingWaitFirstRequestReturnsImmediatelyWithoutETag(t *testing.T) {
	replyingWaitPollInterval, replyingWaitRefreshInterval = 200*time.Millisecond, 50*time.Millisecond
	hub := app.GetSessionStreamHub()
	hub.SetPendingReply("root-1", "s-a", "A")
	start := time.Now()
	w := httptest.NewRecorder()
	h.handleReplyingSessionsWait(w, httptest.NewRequest(http.MethodGet, "/api/replying-sessions/wait?timeout=5", nil))
	if time.Since(start) > 100*time.Millisecond { t.Fatalf("首次请求必须立即返回") }
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"s-a"`) { t.Fatalf("code=%d body=%s", w.Code, w.Body.String()) }
}

func TestReplyingWaitReturnsOnShapeChange(t *testing.T) {
	// 带 If-None-Match 起请求（goroutine），200ms 后另起一轮 replying
	// 断言 ≤ 1s 返回，且正文包含新会话
}

func TestReplyingWaitTimeoutReturns304WhenUnchanged(t *testing.T) {
	// 无变化的 If-None-Match 请求 + timeout=5；用注入的 pollInterval=10ms
	// 先把超时改成可注入（见 Step 3）：timeout 仍走秒级，所以这里直接构造
	// 一个已过期 deadline 的请求不可行 —— 用 timeout=5 会跑 5s。
}
```

> 超时用例不要真等 5 秒：把「最小时长」也做成可注入 —— 用包级 `replyingWaitMinTimeout` / `replyingWaitDefaultTimeout` / `replyingWaitMaxTimeout`（`time.Duration`），测试里设成 `50 * time.Millisecond`，URL 仍传 `timeout=1`。断言：耗时 ≥ 50ms、`w.Code == 304`（请求头带上首次响应给的 ETag）。

其余用例：

```go
func TestReplyingWaitSummaryOnlyChangeWaitsForRefresh(t *testing.T) // refresh=300ms，期间只 AppendReplyEvent，断言不早于 300ms 返回且正文带新摘要
func TestReplyingWaitCancelledContextReturns(t *testing.T)          // ctx 取消后 handler 在 <1s 内返回
func TestReplyingWaitRejectsWhenInFlightFull(t *testing.T)          // replyingWaitInFlight = 32 时 503 + Retry-After
func TestReplyingWaitTimeoutClamp(t *testing.T)                     // parseReplyingWaitTimeout("1")==min, ("999")==max, ("abc")==default
```

- [ ] **Step 2: 跑测试确认红**

Run: `go test ./server/internal/api -run TestReplyingWait -v`
Expected: FAIL（方法未定义）

- [ ] **Step 3: 实现**

- 在 `http_sessions_list.go` 抽出 `replyingSessionsPayload`，`handleReplyingSessions` 改为 `respondJSONConditional(w, r, map[string]any{"sessions": h.replyingSessionsPayload(r, items)})`（行为不变）。
- 新文件实现 handler：先 `atomic.AddInt32` 入在途、`defer` 减；读 `If-None-Match`；无则直接返回；有则 `time.NewTicker(replyingWaitPollInterval)` 循环，每轮只调 `ListReplyingSessions()` 算两个签名，命中条件 `break`；`select` 同时监听 `r.Context().Done()`。
- 返回统一走 `respondJSONConditional`。
- 超时参数解析函数 `parseReplyingWaitTimeout(raw string) time.Duration`。
- 路由：`r.Get("/api/replying-sessions/wait", h.handleReplyingSessionsWait)`。

- [ ] **Step 4: 跑测试确认绿**

Run: `go test ./server/internal/api -run 'TestReplyingWait|TestReplyingSessions' -v`
Expected: PASS；再跑 `go test ./server/internal/api` 全包 PASS

- [ ] **Step 5: 提交**

```bash
git add server/internal/api/http_sessions_replying_wait.go server/internal/api/http_sessions_list.go server/internal/api/http_routes.go server/internal/api/http_sessions_replying_wait_test.go
git commit -m "feat(api): 新增在回复会话长轮询等待端点（Scope: G-MF）"
```

---

### Task 3: 服务端 —— 关停时打断等待

**Files:**
- Modify: `server/app/server.go:178-199`
- Test: `server/app/server_wait_shutdown_test.go`（新建）

**Interfaces:**
- Produces: `func newWaitAwareServer(addr string, handler http.Handler) (*http.Server, context.CancelFunc)` —— `BaseContext` 用可取消 context；`runServer` 在 `Shutdown` 前先 `cancelWait()`。

- [ ] **Step 1: 写失败测试**：起 `newWaitAwareServer("127.0.0.1:0", handler)`，handler 阻塞在 `<-r.Context().Done()`；`http.Get` 发请求；100ms 后调 `cancelWait()`；断言请求在 1s 内返回，随后 `srv.Shutdown` 在 1s 内返回。
- [ ] **Step 2: 跑测试确认红**：`go test ./server/app -run TestWaitAwareServerCancel` → FAIL（函数未定义）。
- [ ] **Step 3: 实现** `newWaitAwareServer` 并在 `runServer` 里替换 `&http.Server{...}` 构造；退出 goroutine 里 `cancelWait()` 放在 `server.Shutdown` 之前。
- [ ] **Step 4: 跑测试确认绿**：`go test ./server/app -run TestWaitAwareServerCancel`。
- [ ] **Step 5: 提交**

```bash
git add server/app/server.go server/app/server_wait_shutdown_test.go
git commit -m "fix(server): 关停时先取消请求上下文，长等待不再拖住重启（Scope: G-MF）"
```

---

### Task 4: 前端 —— 下发 `waitUrl`

**Files:**
- Modify: `web/src/services/net/replyPoller.ts:1-30`（类型）与 `:100-125`（payload）
- Test: `web/tests/reply-poller-wait-url.test.mjs`（新建，源码守卫，风格同 `apk-self-update.test.mjs`）

**Interfaces:**
- Produces: 原生 `configure` 载荷：
  ```json
  {"apiBaseUrl":"","waitUrl":"","nodes":[{"id":"home","url":"https://…/api/replying-sessions","waitUrl":"https://…/api/replying-sessions/wait"}]}
  ```
  `nodes` 为空时的单节点兜底：顶层 `apiBaseUrl` 与 `waitUrl` 成对出现。

- [ ] **Step 1: 写失败测试**：断言 `replyPoller.ts` 里同时存在 `absoluteNodeURL("/api/replying-sessions", id)`（旧锚点，不能删）与 `absoluteNodeURL("/api/replying-sessions/wait", id)`，且 payload 的节点对象含 `waitUrl`，顶层含 `waitUrl`。
- [ ] **Step 2: 跑测试确认红**：`cd web && npx vitest run tests/reply-poller-wait-url.test.mjs`。
- [ ] **Step 3: 实现**：`nodes` map 改为 `{ id, url: absoluteNodeURL("/api/replying-sessions", id), waitUrl: absoluteNodeURL("/api/replying-sessions/wait", id) }`（保留 `filter(node => /^https?:\/\//i.test(node.url))`）；顶层 `waitUrl: apiBaseUrl ? apiBaseUrl + "/api/replying-sessions/wait" : ""`；两种 payload 各加字段到现有类型定义。
- [ ] **Step 4: 跑测试确认绿 + 类型检查**：`cd web && npx vitest run tests/reply-poller-wait-url.test.mjs tests/apk-self-update.test.mjs && npm run typecheck`。
- [ ] **Step 5: 提交**

```bash
git add web/src/services/net/replyPoller.ts web/tests/reply-poller-wait-url.test.mjs
git commit -m "feat(web): 原生回复轮询下发长轮询地址（Scope: G-MF）"
```

---

### Task 5: 安卓 —— 抽出 `ReplyStateMerger` + JVM 单测

**Files:**
- Create: `web/android/app/src/main/java/com/mindfs/app/ReplyStateMerger.java`
- Create: `web/android/app/src/test/java/com/mindfs/app/ReplyStateMergerTest.java`
- Modify: `Makefile`（新增 `test-android` 目标，**不**并入 `make test`）

**Interfaces:**
```java
final class ReplyStateMerger {
    static final class Entry {            // 纯数据，替代现有私有 ReplyState
        final String nodeId, rootId, sessionKey;
        String rootTitle = "", sessionTitle = "", summary = "", updatedAt = "";
        boolean replying = true, awaiting = false;
        String key() { return nodeId + "\u001f" + rootId + "\u001f" + sessionKey; }
    }
    static final class Result {
        List<Entry> newlyCompleted;   // 本轮从 replying 消失的（带最后摘要）
        List<Entry> live;             // 新鲜节点上的 replying 或 awaiting
        Set<String> currentKeys;      // 新鲜节点上出现过的全部键
    }
    ReplyStateMerger(long freshnessMillis);            // 生产传 90_000
    Result applyNode(String nodeId, List<Entry> entries, long nowMillis);
    void markNodeFailed(String nodeId);
    Map<String, Entry> states();                       // key → Entry（只含尚未剪枝的）
}
```

- [ ] **Step 1: 写失败测试**（JUnit4）
  - `applyNode` 新增 replying → `live` 含它、`newlyCompleted` 空、`currentKeys` 含它。
  - 同一节点下一次快照没有它 → `newlyCompleted` 含它且 `summary` 保留；之后 `states()` 不再含它（剪枝）。
  - `markNodeFailed` 后其会话不进 `live`；超过 `freshnessMillis` 仍未成功同样不进。
  - `awaiting=true, replying=false` → 进 `live`。
  - 同一 key 的 `replying` 结束后重新开始 → 重新进 `live`（新条目）。
- [ ] **Step 2: 跑测试确认红**：`cd web/android && ./gradlew :app:testDebugUnitTest`。
- [ ] **Step 3: 实现** `ReplyStateMerger`：内部保存 `Map<String, Entry> states` 与 `Map<String, Long> lastSuccessAt`；`applyNode` 替换该节点全部条目、做 DONE 判定（仅对本节点）、按新鲜度算 `live`、剪掉已完成的键。
- [ ] **Step 4: 跑测试确认绿**；Makefile 加：
  ```make
  # 安卓壳的纯逻辑单测（ReplyAlertPlanner / ReplyStateMerger）。不进 make test：
  # 它要拉起 Gradle daemon，与每次 ship 的快路径无关。
  test-android:
  	cd $(ANDROID_DIR) && ./gradlew :app:testDebugUnitTest
  ```
- [ ] **Step 5: 提交**

```bash
git add web/android/app/src/main/java/com/mindfs/app/ReplyStateMerger.java web/android/app/src/test/java/com/mindfs/app/ReplyStateMergerTest.java Makefile
git commit -m "refactor(android): 抽出回复状态合并器并补纯逻辑单测（Scope: G-MF）"
```

---

### Task 6: 安卓 —— 服务改为每节点长轮询

**Files:**
- Modify: `web/android/app/src/main/java/com/mindfs/app/ReplyPollerService.java`（轮询主体：`pollOnceSafely` / `applyReplyingSessions` / `fetchReplyingSessions` / `scheduleNextPoll` / `resumePolling` / `pausePolling` / `stopAll` / `stopPollingTask` / `recordPollDebug`）
- Modify: `web/src/services/net/replyPoller.ts` 只用于确认载荷字段（无代码改动）

**Interfaces:**
- Consumes: Task 4 的 `waitUrl`、Task 5 的 `ReplyStateMerger`。
- Produces（服务内部）：
  - `private void startNodeLoops()` / `private void stopNodeLoops()`
  - `private void runNodeLoop(NodeTarget target, int generation)`
  - `private HttpURLConnection openConnection(NodeTarget target, String urlString)`（从 `fetchReplyingSessions` 抽出，含 token/E2EE/proof/本地 TLS）
  - `private static final class WaitResult { int status; String etag; Map<String, ReplyStateMerger.Entry> entries; }`
  - `private WaitResult fetchWait(NodeTarget target, String etag) throws Exception`
  - `private void applyNodeSnapshot(NodeTarget target, Map<String, ReplyStateMerger.Entry> entries)`
  - `NodeTarget` 新增 `final String waitUrl`（JSON `waitUrl`，缺省回退 `url + "/wait"` 仅在 `url` 以 `/api/replying-sessions` 结尾时；否则空串表示不支持）
  - 常量：`WAIT_TIMEOUT_SECONDS = 20`、`WAIT_READ_TIMEOUT_MS = 30_000`、`POLL_FALLBACK_INTERVAL_MS = 10_000`、`WAIT_RETRY_INTERVAL_MS = 600_000`、`ERROR_BACKOFF_MS = 5_000`、`NODE_FRESH_MS = 90_000`

**行为：**
- `resumePolling()`：`startForegroundCompat` 后 `startNodeLoops()`。
- `startNodeLoops()`：`generation++`，按 `nodeTargets()` 为每节点提交 `runNodeLoop` 到 `Executors.newScheduledThreadPool(max(2, nodeCount))`。
- `runNodeLoop`：`while (running && generation == myGeneration)` 内：
  - 未降级时 `fetchWait(target, etag)`：200 → 更新 etag、`applyNodeSnapshot`、清零失败计数；304 → 什么都不做；404/405 → 标记该节点 `mode=poll`；其他异常 → `failures++`，≥3 次标记 `mode=poll` 并睡 `WAIT_RETRY_INTERVAL_MS` 后重试等待。
  - `mode=poll` 时：睡 `POLL_FALLBACK_INTERVAL_MS`，普通 GET `/api/replying-sessions`，成功后 `applyNodeSnapshot`、`failures` 清零，并在 `WAIT_RETRY_INTERVAL_MS` 之后恢复等待模式。
- `applyNodeSnapshot`：`synchronized (applyLock)`：`merger.applyNode(...)` → 对 `newlyCompleted` 逐条 `postAlert("done", entry)` + `vibrateReplyCompletion()` → 用 `currentKeys` + `merger.states()` 构造 `ReplyAlertPlanner.Snapshot` 并 apply → `updateForegroundNotification(result.live)` → `recordPollDebug(true, states.size(), errorSummary)`。
- `pausePolling` / `stopAll` / `onDestroy` 调 `stopNodeLoops()` 并 shutdown 线程池。
- `recordPollDebug` 增加 `"modes": {"home":"push","fn":"poll"}` 与 `"freshNodes": N`；`describeNotificationState` 的诊断串追加「模式=push 3 / poll 1」。

- [ ] **Step 1: 编译基线**：`cd web/android && ./gradlew :app:assembleDebug` → 通过（确认改造前是绿的）。
- [ ] **Step 2: 改造**：按上述接口重写轮询主体；删除单 future 的 `pollingTask` 与 `scheduleNextPoll`；`ReplyState` 私有类替换为 `ReplyStateMerger.Entry`（`notificationIdFor` / `postAlert` / `buildReplyTitlePrefix` 改为接收 `Entry`）。
- [ ] **Step 3: 编译**：`./gradlew :app:assembleDebug` → BUILD SUCCESSFUL；`make test-android` → PASS。
- [ ] **Step 4: 隔离实例联调**：`bash scripts/mindfs-iso.sh`（端口 7431）起新服务端；
  - `curl -sI 'http://127.0.0.1:7431/api/replying-sessions/wait?timeout=1'` → 立即 200 且带 `ETag`；
  - 带该 ETag 的请求 → 等 1s 后 304；
  - 期间用 `POST /api/sessions/{key}/messages` 起一轮 → 等待请求立刻 200。
- [ ] **Step 5: 提交**

```bash
git add web/android/app/src/main/java/com/mindfs/app/ReplyPollerService.java
git commit -m "feat(android): 后台回复提醒改为每节点长轮询推送（Scope: G-MF）"
```

---

### Task 7: 安卓 —— 常驻通知可点 + FGS 类型改 `specialUse`

**Files:**
- Modify: `web/android/app/src/main/java/com/mindfs/app/ReplyPollerService.java`（`buildIdleNotification` / `updateForegroundNotification` / `startForegroundCompat`）
- Modify: `web/android/app/src/main/AndroidManifest.xml`
- Test: `web/tests/reply-poller-notification-click.test.mjs`（新建，源码守卫）

**Interfaces:**
- `private Notification buildIdleNotification(String text, ReplyStateMerger.Entry target)` —— `target != null` 时带 `nodeId/rootId/sessionKey` extras 与 `PendingIntent`；否则普通启动 Intent。
- `updateForegroundNotification(List<Entry> live)`：`live.size() == 1` 时把该条传进去，否则传 `null`。
- Manifest：`android:foregroundServiceType="specialUse"` + `<uses-permission android:name="android.permission.FOREGROUND_SERVICE_SPECIAL_USE"/>` + service 内 `<property android:name="android.app.PROPERTY_SPECIAL_USE_FGS_SUBTYPE" android:value="session reply sync"/>`。
- `startForegroundCompat`：`Build.VERSION.SDK_INT >= 34` 用 `ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE`，`>= Q` 用原来的 dataSync 常量，低于 Q 不带类型。

**测试断言：**
- manifest 含 `specialUse`、`FOREGROUND_SERVICE_SPECIAL_USE`、`PROPERTY_SPECIAL_USE_FGS_SUBTYPE`，且不再有 `foregroundServiceType="dataSync"`。
- 服务源码中 `buildIdleNotification` 带 `setContentIntent`；常驻通知的 extras 只在 `live.size() == 1` 时设置（断言出现 `size() == 1` 且 `putExtra("nodeId"` 在 `buildIdleNotification` 内）。
- 已有的 `absoluteNodeURL("/api/replying-sessions", id)` 锚点仍在（`apk-self-update.test.mjs` 复跑）。

- [ ] **Step 1: 写失败测试**（源码守卫），`cd web && npx vitest run tests/reply-poller-notification-click.test.mjs` → 红。
- [ ] **Step 2: 实现**上述三处。
- [ ] **Step 3: 跑测试确认绿**：`npx vitest run tests/reply-poller-notification-click.test.mjs tests/apk-self-update.test.mjs`。
- [ ] **Step 4: 编译**：`cd web/android && ./gradlew :app:assembleDebug` → SUCCESS。
- [ ] **Step 5: 提交**

```bash
git add web/android/app/src/main/java/com/mindfs/app/ReplyPollerService.java web/android/app/src/main/AndroidManifest.xml web/tests/reply-poller-notification-click.test.mjs
git commit -m "feat(android): 常驻回复通知可点直达，前台服务改用 specialUse（Scope: G-MF）"
```

---

### Task 8: 定制登记、门禁与部署核对

**Files:**
- Modify: `docs/upstream-customizations.yaml`（回填 G-MF 的 `tests`/`anchors`）
- Modify: `docs/plans/README.md`（记执行结果）

**步骤：**

- [ ] **Step 1: 回填 G-MF**：`tests` 列 4 个新测试文件；`anchors` 每条「文件:片段」都要在代码里真实存在（例：`server/internal/api/http_sessions_replying_wait.go:func (h *HTTPHandler) handleReplyingSessionsWait`、`web/src/services/net/replyPoller.ts:absoluteNodeURL("/api/replying-sessions/wait", id)`、`web/android/app/src/main/AndroidManifest.xml:android:foregroundServiceType="specialUse"`、`.../ReplyStateMerger.java:final class ReplyStateMerger`）。
- [ ] **Step 2: 全量门禁**

```bash
go test ./...
cd web && npm run typecheck && npm test && cd ..
make build
make test-android
```

- [ ] **Step 3: `make check-upstream`**：提交前应报新文件未登记（预期），提交后必须转绿。
- [ ] **Step 4: 提交**

```bash
git add docs/upstream-customizations.yaml docs/plans/README.md
git commit -m "docs(custom): 登记安卓后台推送定制组 G-MF（Scope: G-MF）"
```

- [ ] **Step 5: 部署（需用户在场）**：`bash scripts/deploy-all.sh`；分发后独立核对四端版本与 `/health`。本机 `sudo systemctl restart mindfs` **由用户执行**。
- [ ] **Step 6: 代理超时实测**：从外网（手机流量）对每个节点发一次 `curl -m 35 -w '%{time_total} %{http_code}\n' '<节点>/api/replying-sessions/wait?timeout=20'`（带该节点自己的 `user=` 参数）。预期 `20.0x 200`；若出现 `code=000`/502 且耗时 < 20s，说明代理更早掐断，把 `WAIT_TIMEOUT_SECONDS` 降到实测值以下再发一版。
- [ ] **Step 7: 出包与真机验收**：走 `apk-release` skill 出 APK（签名密钥走 `MINDFS_RELEASE_PRIVATE_KEY_FILE`）；真机核对：后台起一轮任务 → 秒级响铃；点常驻「有会话正在回复」→ 直达该会话；两轮会话时点常驻 → 只打开 App。

---

## Self-Review 记录

- Spec 覆盖：长轮询端点（Task 2）、确定性排序（Task 1）、在途上限（Task 2）、关停（Task 3）、每节点循环与降级/新鲜度/剪枝（Task 5+6）、常驻通知可点（Task 7）、FGS 类型（Task 7）、前端 waitUrl（Task 4）、登记与门禁（Task 0/8）、代理实测（Task 8）。
- Review Focus 对应测试：掐断降级（Task 6 Step 4 + Task 8 Step 6）、播种（Task 2 首帧用例）、掉线新鲜度（Task 5 单测）、多会话通知（现有行为 + Task 7 断言 `size()==1`）、点击（Task 7）。
- 已知未覆盖：Java 服务层并发（无 UI 测试基建），靠 `assembleDebug` + 真机验收兜底。
---

## 实施结果

**Task 0–8 已实现并提交 main，未部署、未出包、未做真机验收。** 提交（均带 `Scope: G-MF`）：

| 任务 | 提交 | 内容 |
|---|---|---|
| Task 0 | `9d55743d` | 计划 + spec + `docs/plans/README.md` + `G-MF` 空组登记 |
| Task 1 | `8c9ef5c4` | `stream_hub.go` 按 `sessionKey` 稳定排序 + `stream_hub_order_test.go` |
| Task 2 | `2821fad8` | `GET /api/replying-sessions/wait` 长轮询 + 载荷抽取 + 路由 + `http_sessions_replying_wait_test.go` |
| Task 3 | `9bd064c8` | `newWaitAwareServer` 可取消 BaseContext + `server_wait_shutdown_test.go` |
| Task 4 | `78074250`、`2f12d5f7` | 前端下发 `waitUrl` + `reply-poller-wait-url.test.mjs`；重锚 `conditional-request.test.mjs` |
| Task 5 | `a2b78e59` | `ReplyStateMerger` + JUnit 单测 + `Makefile` 的 `test-android` |
| Task 6 | `8216251c`、`23a87f56` | `ReplyPollerService` 每节点长轮询循环；awaiting 集合读写移入 `applyLock` |
| Task 7 | `f6301f94`、`329c3733` | 常驻通知 PendingIntent 可点 + `specialUse|dataSync`；`reply-poller-notification-click.test.mjs`；重锚 `apk-self-update.test.mjs` |
| Task 8 | 紧随本节的登记提交 | 回填 `G-MF` 的 `files`/`tests`/`anchors`，spec 状态改「已批准」 |

登记要点：`G-MF.files` 补 `Makefile`（Task 5 第 10 个文件）；`tests` 只列 check-upstream 认的 5 个（3 个 `*_test.go` + 2 个 `*.test.mjs`）；JUnit 的 `ReplyStateMergerTest.java` 跑不到 check-upstream 的运行器 glob，改登记在 `anchors`（`class ReplyStateMergerTest`），由 `make test-android` 执行。

**门禁（2026-11-18）：**

- `go test ./...`：首次运行 `server/internal/kanban` 偶发失败（既有 flaky，见 memory `kanban-gate-flaky-sqlite-io`）；`go test -count=1 ./...` 复跑全绿（退出 0）。
- `cd web && npm run typecheck && npm test`：typecheck clean；232 文件通过 / 5 跳过，650 用例通过 / 6 跳过。
- `make build`：成功（`v0.5.5-1118-153-g329c3733-dirty`）。
- `make test-android`（`ANDROID_HOME=$HOME/Android/Sdk`）：BUILD SUCCESSFUL。
- `make check-upstream`：✓ 1873 个 delta 文件被 306 组完整覆盖（登记提交后复跑）。

**全分支评审 + 修复波（2026-11-18）：** 终审结论「可合并」，另提 3 条 Important，均已修并做了一次限定范围复审（A–G 全部 ADDRESSED、零回归）：

| 提交 | 内容 |
|---|---|
| `dd8a91c3` | 顶层 `waitUrl` 贯通两条 bridge 到服务端兜底（不再拼 `"/wait"`）；`failNode` 区分「瞬时失败」与「判定掉线」（瞬时只按 90s 新鲜度衰减，不再一失败就闪）；503 按 `Retry-After` 退避且不计入降级计数；apply/刷新路径补 generation 校验 |
| `73911594` | 补 `TestReplyingWaitRefreshElapsedUnchangedKeepsWaiting`（刷新到点但载荷未变必须继续等，变异验证会红） |
| `a28bd445` | `conditional-request.test.mjs` 负例锚点重指向当前调用形状 |
| `19b84cdc` | `make test-android` 补 `$(ANDROID_GRADLE_ENV)` 与 SDK 守卫 |

复审残留（仅登记，不修）：`ReplyPollerService` 对 `apiBaseUrl` 有本地 shell 来源守卫、对 `waitUrl` 没有——只在开发构建用 localhost 且已持久化真实 base 时可达，线上下发配置（`capacitor.config.json` 为 `https://home.xiaokubao.space/mindfs/`）不可达。

**已部署并出包（2026-11-18）：** `bash scripts/deploy-all.sh` 退出 0，四端 `v0.5.5-1118-159-g055d138d`（本机、WSL、fn、pc-win；pc-win PID 35344 health 200）。WSL/fn 上长轮询实测 `seed=200 <1ms` → `If-None-Match` 超时 `304 @5.0–5.3s`；本机重启后 `/mindfs/api/replying-sessions/wait` 返回 200。APK 发布 tag `v0.5.5-1119`（`6d…` 见 GitHub）：`versionName=v0.5.5-1119`、`versionCode=5051120`（1118 为 …1119，单调递增），签名证书 `CN=MindFS`，APK sha256 `92284f2e39609994d4f91325d2c0658fc7324f6ea98664356b2aa690d2ab0a78`，`make verify-release` 通过，线上 `android-version.json` 版本字段为 `v0.5.5-1119` 且 `.apk` 直链可下载。

**仍未完成（需用户执行）：** Step 6 外网代理超时实测、Step 7 真机验收（后台秒级响铃、常驻通知点击直达/多会话只开 App、挂一夜确认 `specialUse` 前台服务存活）。

## 第二轮回补：完成账本 + 子会话静默（2026-11-19，Scope: G-MX）

用户 `v0.5.5-1120` 装完仍报「后台时间一长完全没有提醒」，并另提「子任务完成不要提醒」。前者说明上一轮（Task 0–8）修的是**投递链路**，而漏掉的是**事件来源**：完成判定一直是纯客户端边沿检测（上一帧 replying=true、这一帧消失），手机在后台被冻结/被 ROM 清掉进程的那段时间跑完的回合，客户端从头到尾没见过 —— 醒来后 pending 表里根本没有这条，也就永远补不出来。这一轮改的是架构，不是又打一个补丁：

| 层 | 改动 | 文件 |
|---|---|---|
| 源头 | 子会话标记收敛到唯一源头 `SetPendingReply`（所有调用点都是 `OnSubAgentAttached`） | `stream_hub_pending.go`、`stream_hub_types.go` |
| 服务端 | 进程内完成账本：单调 `seq` + `epoch`（进程身份），256 条 / 24h；`/api/replying-sessions` 与 `/wait` 共用一份 `replyingPayload`，带 `completed_since` 才回 `completed[]`；长轮询用 `HasCompletionsSince` 提前唤醒 | `stream_hub_completions.go`、`http_sessions_list.go`、`http_sessions_replying_wait.go` |
| 推送 | 子会话不推 Web Push | `appcontext_notify.go` |
| 安卓 | `ReplyCompletionFeed` 按节点游标补发（未播种/epoch 变化/head 回退静默重播种，一次最多补最新 8 条，跳过 subSession）；老服务端无 `completedEpoch` 自动退回边沿检测 | `ReplyCompletionFeed.java`、`ReplyPollerService.java` |
| 安卓存活 | CONFIGURE 返回 `START_STICKY`；`onTaskRemoved` 把兜底闹钟提前到 1 分钟；兜底唤醒改走广播接收器（`PendingIntent.getService` 在 Android 8+ 后台起不来服务）；`configure()` 在 running=false 时补建循环；服务启动统一捕获异常 | 同上 + `ReplyPollerWatchdogReceiver.java`、`MainActivity.java`、`ReplyPollerPlugin.java` |
| 网页 | 轮询路径按子会话剔除完成音（未读徽标照标），`carrySubSessionKeys` 跨帧保留标记 | `useReplyingSessionPoll.ts`、`pendingCompletion.ts` |

**门禁（2026-11-19）：** 隔离 worktree 跑 `go test -count=1 ./...` 全绿（工作区里 `kanban` 的 gate 校验与 `api` 的两条用例是另一条会话在飞的未提交改动所致，与本轮无关 —— 干净 worktree 上同一提交全绿）；web `tsc` clean、Vitest 236 文件 / 691 通过 / 6 跳过；`make test-android` BUILD SUCCESSFUL（`ReplyCompletionFeedTest` 8、`ReplyStateMergerTest` 10、`ReplyRetryPolicyTest` 5）；`make check-upstream` 里 G-MX 全绿（剩余红是 G-MN / G-MV / G-MO 三组别的会话在飞的工作）。

**已部署（2026-11-19）：** 四端 `v0.5.5-1120-18-g056423c9-dirty`（本机二进制已装、待用户 `sudo systemctl restart mindfs`；WSL/fn user 单元已重启并核对；pc-win PID 22592、health 200，7332 LISTENING）。WSL/fn 上实测 `/api/replying-sessions` 已带 `completedEpoch`/`completedSeq`，带 `completed_since=0` 时返回 `completed[]`。

**APK 已构建未发布：** `dist/mindfs_v0.5.5-1120-18-g056423c9_android.apk`（7.4 MB，`versionCode=5051121`、`versionName=v0.5.5-1120-18-g056423c9`、证书 `CN=MindFS`、sha256 `8061ac25df9f29a16bd1d241d80f82587b54daff770b8a464679aa358e73de30`）。**未打 tag、未推 GitHub** —— 发布是对外动作，等用户确认。

**端到端仍未验：** 本机在跑的进程还是重启前的旧二进制（`/api/replying-sessions` 无 `completedEpoch`），要等用户重启后账本才生效；没有连接安卓设备的 adb 目标，真机验收仍需用户执行。

## 第三轮：兜底闹钟从不送达（2026-11-19 晚，Scope: G-MX）

**用户报告（1121 装后）**：「还是不行 这几分钟内没有任何会话完成的提示 我打开 app 到前台才出现提示」。

**证据（nginx 访问日志 8 小时 + 本机进程，2026-11-19）**

| 观测 | 事实 |
|---|---|
| 手机 192.168.1.149 → `home.xiaokubao.space/mindfs` | 原生轮询 UA `Dalvik/2.1.0 (…Android 17; PLJ110…)` 打 `/wait?timeout=20`，WebView 用 `…wv` UA |
| 存活窗口 | 13:14–13:17、15:08–15:19、16:23–16:40、19:19–19:21、20:16（1 分钟）、20:39–20:48 |
| 静默窗口 | **110 / 63 / 158 / 54 / 22 分钟**，每段都以「用户打开 App」的同一分钟结束 |
| 原生与 WebView | **同一分钟一起出现、一起消失**；没有一分钟是「App 在后台、原生轮询还活着」 |
| `completed_since` | 8 小时 0 次（本机无 `completedEpoch`，客户端游标无从播种 —— 不能据此判断 APK 版本） |
| 本机进程 | `PID 126`，`/proc/126/exe` 指向已删除路径，启动于 17:33；二进制 18:27 被替换 ⇒ 跑的是启动前的旧二进制 |
| 通知侧 | 渠道 `mindfs_reply_alert_v5` 未静音、总开关开、勿扰关、电池优化已豁免 |

**结论**：不是通知被拦，是**后台没有任何东西在跑**。切后台 1–4 分钟进程就被 ROM 冻死/清掉，而 15 分钟一次的 `setAndAllowWhileIdle`（inexact）**一次都没响** —— 国产 ROM 的省电模式会无限推迟 inexact 闹钟。第 4 次修同一症状，因此按系统化调试的规矩停下来问了用户方向，用户选择「精确闹钟兜底」。

**改动**（`Scope: G-MX`）

| 项 | 旧 | 新 |
|---|---|---|
| 闹钟 API | `setAndAllowWhileIdle`（inexact） | 精确闹钟 `setExactAndAllowWhileIdle`：不延迟、Doze 下同样唤醒，**不占状态栏图标**（`setAlarmClock` 会被当成用户设的闹钟而常驻图标，用户明确不要；`setExactAndAllowWhileIdle` 的代价只是 Doze 下约 9 分钟限频，而本 App 在电池优化豁免名单里）；以精确闹钟送达的广播自带「允许启动前台服务」豁免 |
| 精确闹钟授权 | 未声明 | 补 `SCHEDULE_EXACT_ALARM`（Android 12+ / targetSdk 36 必需）。`canScheduleExactAlarms()` 为假时**退回 inexact**，并在诊断串报「精确闹钟=未授权」；收到 `SCHEDULE_EXACT_ALARM_PERMISSION_STATE_CHANGED` 时重排（授予时系统会删掉先前排不上的闹钟） |
| 间隔 | 15 分钟 | **3 分钟**（`WATCHDOG_INTERVAL_MS`，接收器取同一常量，不再写字面量） |
| `onTaskRemoved` | 1 分钟后 | 10 秒后 |
| 暂停 | 不取消闹钟 | `pausePolling()` 取消（否则 3 分钟后闹钟会把轮询拉回来） |
| 可观测性 | 无 | `ACTION_WATCHDOG` 写 `PREF_LAST_WAKE`；诊断串新增「巡检=每 3 分钟 · 最近唤醒=…」 |
| 状态栏 | — | 不新增图标（2026-11-19 晚按用户反馈把 `setAlarmClock` 换成 `setExactAndAllowWhileIdle`） |

**为什么「最近唤醒」是关键证据**：闹钟按时送达 = 时间戳持续前进（ROM 只是冻进程）；时间戳停住 = ROM 直接 force-stop 并取消了闹钟（系统语义），届时唯一出路是厂商白名单（自启动 / 后台无限制 / 锁定后台），不是继续改代码。

**门禁**：`npx vitest run web/tests/android-background-poller-liveness.test.mjs` 6 passed；三个变异（间隔退回 15 分钟、pause 去掉 `cancelWatchdog()`、删 `SCHEDULE_EXACT_ALARM` 声明）各自打红对应用例后还原复绿；`make test-android` BUILD SUCCESSFUL。

**残留**：仍不承诺必达（force-stop 会取消闹钟）；未做真机验收（无 adb 目标）；完成账本要在本机 `sudo systemctl restart mindfs` 后才生效 —— 与本次闹钟修复相互独立，账本负责「醒来后补发」，闹钟负责「定期醒来」。

**2026-11-19 晚追加（同 `Scope: G-MX`）**：用户反馈「状态栏多一个闹钟图标、太麻烦，为什么别的 App 能实时提醒」。图标是 `setAlarmClock` 的语义代价（系统把它当用户设的闹钟），已改用 `setExactAndAllowWhileIdle` —— 同样受 `SCHEDULE_EXACT_ALARM` 约束、同样在 Doze 下唤醒，但不占状态栏；只有在深度 Doze 且不在电池优化豁免名单时才可能被限频到约 9 分钟一次。**这不是「实时」**：轮询无论怎么调优都只能跑在系统允许的缝隙里，真正实时的提醒来自系统级推送通道（FCM / 厂商推送），那是另一条架构路线，见下节。

**「别的 App 为什么实时」**：微信、钉钉这类国产 App 靠的是手机厂商在系统进程里跑的一条常驻长连接（OPPO/一加是 OPPO 推送，小米 MiPush，华为 HMS Push），App 被冻、被杀、被划掉都不影响它送达；海外机型走 FCM。第三方 App 要用这条通道，必须在厂商开放平台注册应用、登记包名与签名、接 SDK，并由服务端调厂商接口发消息 —— 这是本项目目前明确排除的（G-MX boundary：不做 FCM/APNs）。本轮把「自建轮询 + 兜底闹钟」压到它的物理上限（≤3 分钟、无图标），要再往前只有两条路：接系统推送，或接受这个延迟。
