# 命名规范（长期规则）

| 对象 | 规则 | 例 |
|---|---|---|
| 组件文件 | `PascalCase.tsx`，与主组件同名 | `SessionViewer.tsx` |
| hook 文件 | `useXxx.ts`，与 hook 同名 | `useSessionStream.ts` |
| 工具文件 | `xxxUtils.ts`（纯函数）/ `xxxHelpers.tsx`（含 JSX 渲染辅助） | `sessionCardUtils.ts`、`gitStatusHelpers.tsx` |
| 类型文件 | `xxxTypes.ts`（域内）/ `types.ts`（跨域共享） | `sessionViewerTypes.ts`、`services/task/types.ts` |
| 样式文件 | `xxxStyles.ts`（复数），与组件同目录 | `sessionViewerStyles.ts` |
| 服务文件 | `camelCase.ts` 或 `xxxApi.ts`（API 门面） | `sessionApi.ts`、`sessionCacheStore.ts` |
| CSS 文件 | 仅第三方/全局用 `.css`；组件样式内联或 `xxxStyles.ts` | — |

三条铁律：① 文件名 = 主 export 名；② 一个文件一个主 export；
③ 后缀即语义（`.tsx` 必有 JSX，`.ts` 必无 JSX；`Utils` 纯函数，`Helpers` 可含 JSX）。

## 落地

- 新代码强制遵守；CI 加命名检查（文件名 vs 主 export 名，`.ts` 无 JSX 扫描）。
- 存量违规见 `TODO.md`，不现在改；`web/tests/naming-convention.test.mjs` 覆盖本节。
