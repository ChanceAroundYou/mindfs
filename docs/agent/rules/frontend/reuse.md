# 复用原则（长期规则）

- **抽取门槛**：同一逻辑出现 **2 次就抽**到 `shared/` 或 `hooks/`；第 3 次用时必须已复用。
- **上移判据**：被 2+ 域用的东西必须上移（纯函数 → `shared/`，通用 UI → `components/common/`，
  跨域 hook → `hooks/`）。
- **就近优先**：只被 1 个域用的东西不许上移，就近 colocate。

## 已有复用（勿重复造）

`shared/datetime.ts`（`formatCompactTime`/`formatCompactSize`）、`shared/styles.ts`
（`iconButtonStyle`/`emptyStyle`/`errorStyle`…）、`sessionListShared.tsx`
（ToggleRowButton/PinIcon/ArchiveHeaderButton）、`layout/ViewHeader.tsx`、
`sessionCardUtils.tsx`（`formatTime` 相对时间版、`renderHighlightedText`）。
