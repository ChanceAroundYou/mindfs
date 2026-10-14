# Reviewer 执行指南

每次前端改动都必须完成本表。自动门禁失败或人工检查缺少要求的证据时，改动不得合并；没有按路径、历史状态或 reviewer 判断覆盖规则的机制。

## 必须检查

- [ ] `.ts` 文件不含 JSX、`.tsx` 文件含 JSX；视图不直接导入 `services/`；逻辑模块不返回 JSX。
- [ ] 新逻辑复用前，确认 `shared/`、`hooks/` 中没有可直接复用的实现。
- [ ] 新增共享函数或通用组件时，同批提供覆盖其行为的 Vitest 测试。
- [ ] 源文件符合统一文件长度规则；命名、类型、样式和后缀符合对应规则。
- [ ] 跨组件状态由组合层持有，局部 hook 只持有局部 UI 状态。
- [ ] WebSocket 状态更新按标识增量合并；状态快照与增量事件使用不同的数据路径，不能用整体替换覆盖已知状态。
- [ ] 检查用户可见行为、错误路径、账户与节点边界，并确认没有重复实现。
- [ ] SQLite 变更记录回滚方式；新 handler/usecase 同批有行为测试。

## 快速自查命令

```bash
# .ts 含 JSX
grep -rl "<[A-Za-z]" web/src/shared web/src/services --include="*.ts"
# view 直调 services
grep -rn "from.*services/" web/src/components --include="*.tsx"
# 行数红线
find web/src \( -name "*.tsx" -o -name "*.ts" \) -print0 | xargs -0 wc -l | awk '$1>500'
```

## 自动覆盖映射

- 独立组件/hook 主符号命名、`.ts` 无 JSX、`.tsx` 有 JSX、命名后缀：`web/tests/naming-convention.test.mjs`（`npm test`）；职责模块按职责命名，可有多个相关导出。
- component→service 依赖与统一文件长度：`web/tests/reviewer-architecture-rules.test.mjs`（`npm test`）。
- 逻辑/视图边界与 WebSocket 增量行为：对应 layering、stream 和跨节点回归测试（`npm test`）。
- 复用检查、跨组件状态归属、用户可见行为和数据库回滚说明：reviewer 按本表检查并在变更说明中给出依据。
