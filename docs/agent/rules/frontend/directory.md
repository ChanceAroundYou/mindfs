# 目录划分（长期规则）

```
web/src/
├── app/            应用编排：四层组合 hooks、跨域 handlers、AppRootView。禁纯 UI
├── components/
│   ├── <domain>/   14 个域 UI + 强绑定 colocate hook。禁跨域复用件
│   ├── app/        壳层专属组装型纯展示组件（SessionPane/AppActionBar/SessionSheet/
│   │               FileTreePanel/OnboardingGate/TaskInlineEditPanel/
│   │               TaskDetailPanelGate/TaskSessionErrorDialog）。禁逻辑门
│   └── common/     无业务语义通用 UI 小件（Select/Icon/Scroll）
├── services/
│   ├── <domain>/   按域收拢（file/git/net/platform/prefs/task/session）
│   └── *.ts        根只留门面 re-export
├── hooks/          跨 2+ 域复用的 hook。禁单域 hook、禁非 hook 内容
├── shared/         零 UI：纯函数 + 纯样式（.ts，禁 JSX）
├── layout/         有布局语义的跨视图复用 UI（AppShell/ViewHeader）
├── i18n/           纯基建：语言包 + useI18n + locale 类型。零业务
├── plugins/        插件管理器 + 信任/安全逻辑。不依赖具体域
└── renderer/       渲染器注册表。只依赖具体 viewer，禁反向依赖
web/tests/          前端测试，镜像 src/ 结构
```

## 三条判据（可执行）

1. **有没有 JSX**：无 JSX → `shared/`；有 JSX 且无业务语义 → `components/common/`；
   有 JSX 且有布局语义 → `layout/`；有 JSX 且有域语义 → `components/<domain>/`。
2. **被几个域用**：1 个域 → 就近 colocate；2+ 域 → 上移 `shared/` 或 `hooks/`；跨域编排 → `app/`。
3. **碰不碰 UI**：碰 UI 的一律不许进 `services/`；`services/` 只做数据/网络。

## 类型文件归置（类型跟使用域走）

- 单域类型 → `components/<domain>/xxxTypes.ts`（如 `sessionViewerTypes.ts`）。
- 跨域类型 → `services/<domain>/types.ts` 或 `shared/`。
- 跨 file/git/task 三域的类型 → 放 `services/` 对应域后由门面再导出，不放 `components`。

## 通用三筐边界

| 筐 | 判据 | 例 |
|---|---|---|
| `components/common/` | 无业务语义的通用 UI 小件 | `Select.tsx`、`ExitIcon.tsx`、`FloatingScroll.tsx` |
| `layout/` | 有页面结构语义，决定内容怎么排 | `ViewHeader.tsx`、`AppShell.tsx` |
| `shared/` | 零 UI，纯数据/格式化/算法 | `datetime.ts`、`scope.ts`、`sessionOverlay.ts` |
