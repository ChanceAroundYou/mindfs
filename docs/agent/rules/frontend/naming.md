# 命名规范（长期规则）

| 对象 | 规则 | 例 |
|---|---|---|
| 独立组件入口 | `PascalCase.tsx`，与该文件的主要组件同名；职责支持模块按职责命名且可导出多个相关符号 | `SessionViewer.tsx`、`sessionViewerTypes.ts` |
| hook 入口 | `useXxx.ts`，与该文件的主要 hook 同名；同一职责的 hook 原语模块按职责命名 | `useSessionStream.ts`、`backNavigation.ts` |
| 工具文件 | `xxxUtils.ts`（纯函数）/ `xxxHelpers.tsx`（含 JSX 渲染辅助） | `appUtils.ts`、`gitStatusHelpers.tsx` |
| 类型文件 | `xxxTypes.ts`（域内）/ `types.ts`（跨域共享） | `sessionViewerTypes.ts`、`services/task/types.ts` |
| 样式文件 | `xxxStyles.ts`（复数），与组件同目录 | `sessionViewerStyles.ts` |
| 服务文件 | `camelCase.ts` 或 `xxxApi.ts`（API 门面） | `sessionApi.ts`、`sessionCacheStore.ts` |
| CSS 文件 | 仅第三方/全局用 `.css`；组件样式内联或 `xxxStyles.ts` | — |

命名规则对存量与新增代码一体适用。文件按职责分类命名；承载同一职责的支持模块可以导出多个相关符号，文件名描述模块职责即可。只有独立组件入口和独立 hook 入口要求与主要组件/hook 同名；export 数量或“没有同名 export”本身不构成违规。类型、服务、工具、样式和职责模块按职责命名，不要求存在同名 export。

后缀即语义：`.tsx` 必有 JSX，`.ts` 必无 JSX；`Utils` 不得含 JSX；导出 JSX 渲染辅助的模块使用 `Helpers.tsx` 后缀。混合 hook 与非 hook 的职责模块应按目录规则拆清边界，不以 hook 命名掩盖非 hook 原语。

## 落地

- 新代码和存量代码统一遵守；`web/tests/naming-convention.test.mjs` 使用 AST 检查独立组件/hook 入口的主符号命名、`.ts` 无 JSX、`.tsx` 有 JSX、工具/辅助文件后缀、域类型和样式命名。职责模块只按可机械识别的文件类别校验，不要求导出与文件名同名。
