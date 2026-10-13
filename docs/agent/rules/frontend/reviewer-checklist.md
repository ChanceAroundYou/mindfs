# reviewer 执行指南（长期规则）

> 每次前端改动按本表打勾。任一红灯项不通过即挡合并。

## 红灯（BLOCK，不通过即挡合并）

- [ ] 无 `.ts` 文件含 JSX（`grep -l "<[A-Z]"` 扫 `shared/`、`services/`、`*Utils.ts`）。
- [ ] 无 view 文件 import `services/`（以 `components/` 下 `from.*services/` 计）。
- [ ] 无 logic 文件返回 JSX（`app/*.ts`、`hooks/*.ts` 无 `return (` JSX 块）。
- [ ] 无第三份拷贝：新增逻辑先 `grep` 确认 `shared/`、`hooks/` 无现成实现。
- [ ] 新复用件带测试：新进 `shared/` 的函数、新通用组件同批有 `web/tests/*.test.mjs`。
- [ ] 改动文件行数：无新增 >500 文件；触及 300–500 文件时附拆分 TODO。

## 黄灯（WARN，建议修后合）

- [ ] 文件名 = 主 export 名；`Utils` 纯函数、`Helpers` 可含 JSX。
- [ ] 类型文件后缀统一（`xxxTypes.ts` / `types.ts`）；样式文件用 `xxxStyles.ts` 复数。
- [ ] 跨组件状态在四层组合持有，业务 hook 只有本地 UI 状态。
- [ ] WS 更新为增量合并，无整体替换（`setX(data)` 整包替换要能解释）。
- [ ] 命名规范测试覆盖：改动涉及命名时 `naming-convention.test.mjs` 同步更新。

## 快速自查命令

```bash
# .ts 含 JSX
grep -rl "<[A-Za-z]" web/src/shared web/src/services --include="*.ts"
# view 直调 services
grep -rn "from.*services/" web/src/components --include="*.tsx"
# 行数红线
find web/src -name "*.tsx" -o -name "*.ts" | xargs wc -l | awk '$1>500'
```
