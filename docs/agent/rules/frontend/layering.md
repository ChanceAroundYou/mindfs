# 视图与功能分离（长期规则）

四层结构：`App.tsx`（小型组合根）→ `useAppStateLayer`（状态持有）
→ `useAppSessionLayer`（会话装配）→ `useAppDomainLayer`（跨域接线）
→ `useAppViewComposition`（视图组装）→ `AppRootView.tsx`（壳层渲染）。

| 层 | 只放什么 | 禁止 | 后缀 |
|---|---|---|---|
| view（`components/`） | 纯 JSX 渲染、props 驱动、本地 UI 状态（hover/expand） | 数据获取、跨组件状态、业务副作用 | `.tsx` |
| logic（`app/` 或 colocate hook） | 数据获取、状态编排、业务动作、跨域协调 | 返回 JSX、DOM 操作 | `.ts` |
| service（`services/`） | 网络请求、数据转换、外部 API 封装 | React、状态、UI | `.ts` |
| presenter（view 内子文件） | 单一渲染职责，可含局部样式 | 副作用、数据获取 | `.tsx` |

三条铁律：① view 禁止 import `services/`（数据经 logic 层 props 传入），严格执行；
② logic 禁止返回 JSX；③ 副作用（WS 订阅、定时器、localStorage）一律在 logic 层。

hook 归属：单域 → `components/<domain>/` colocate；2+ 域共用 → `hooks/`；跨域编排 → `app/`。
状态归属：跨组件状态一律上收四层组合；业务 hook 只允许本地 UI 状态。
透传风格：四层之间维持 `{...l1} {...l2}` 展开透传。
