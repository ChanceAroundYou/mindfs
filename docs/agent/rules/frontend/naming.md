# 命名规范（长期规则）

| 对象 | 规则 | 例 |
|---|---|---|
| 组件入口文件 | `PascalCase.tsx`，与主要组件同名；支持模块按其职责命名 | `SessionViewer.tsx`、`sessionViewerTypes.ts` |
| hook 文件 | `useXxx.ts`，与 hook 同名 | `useSessionStream.ts` |
| 工具文件 | `xxxUtils.ts`（纯函数）/ `xxxHelpers.tsx`（含 JSX 渲染辅助） | `appUtils.ts`、`gitStatusHelpers.tsx` |
| 类型文件 | `xxxTypes.ts`（域内）/ `types.ts`（跨域共享） | `sessionViewerTypes.ts`、`services/task/types.ts` |
| 样式文件 | `xxxStyles.ts`（复数），与组件同目录 | `sessionViewerStyles.ts` |
| 服务文件 | `camelCase.ts` 或 `xxxApi.ts`（API 门面） | `sessionApi.ts`、`sessionCacheStore.ts` |
| CSS 文件 | 仅第三方/全局用 `.css`；组件样式内联或 `xxxStyles.ts` | — |

命名规则对存量与新增代码一体适用。文件按职责分类命名；承载同一职责的组件支持模块可以导出多个相关符号，文件名描述模块职责即可。后缀即语义：`.tsx` 必有 JSX，`.ts` 必无 JSX；`Utils` 不得含 JSX，含 JSX 的渲染辅助用 `Helpers.tsx`。

## 落地

- 新代码和存量代码统一遵守；`web/tests/naming-convention.test.mjs` 扫描前端源码，检查 `.ts` JSX、工具/辅助文件后缀、域类型和样式命名。
