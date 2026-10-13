# 单向数据流（长期规则）

```
服务端 SQLite (WAL) ↕ REST + WS → services/（请求/缓存/合并）
→ app/ 四层组合 → components/（纯渲染 + 本地 UI 状态）
```

- 流向单向，禁止反向 import（components 不调 services，services 不调 app）。
- 状态源：服务端 SQLite 为准，前端 state 是带合并策略的缓存；冲突时以服务端为准重拉。
- WS 策略：只推增量事件，前端按 key 合并进现有 state，禁止整体替换。
- 跨节点：浏览器扇出 + 按 key 合并，新能力复用同一契约。
