# Edge 任务栏图标与产品名单一来源

> Scope IDs: G-SF / G-SG（claimed 2026-11-27T04:39Z）

日期：2026-11-27
状态：Active
范围：Windows 边控端（`pc`）桌面壳的窗口图标/标题，以及把对外展示的产品名收敛到
`internal/brand/brand.json`。

## 现象与根因

1. Windows 任务栏与 Task View 里 MindFS 窗口没有图标。
   `go-webview2` 在 `WindowOptions.IconId == 0` 时加载 `IDI_APPLICATION (32512)`；
   `cli/cmd/rsrc_windows_*.syso` 只定义了 ID 为 1 的 `RT_GROUP_ICON`，`LoadImageW`
   失败后窗口类拿到 NULL 图标。exe、快捷方式、安装器图标都正常，唯独窗口类是空白。
2. 任务栏条目标签只有节点名（如「书房 PC」），认不出是哪个应用。
3. 产品展示名散落在 Go、前端 i18n、PWA/原生清单、NSIS、PowerShell 安装脚本里，
   改名需要逐处搜索，容易漏。

## 方案

- **图标**：`WindowOptions.IconId` 指向 `.syso` 的组图标 ID；常量由测试直接解析
  `.syso` 校验，避免资源与常量各改各的。
- **标题**：`brand.Title(node)` 固定输出 `<产品名> · <节点名>`，无节点名时只有产品名；
  窗口创建、服务端节点名回填、第二实例聚焦共用同一函数。
- **品牌单一来源**：`internal/brand/brand.json`（`name` / `tagline` / `description`）。
  - Go 通过 `//go:embed` 读取；
  - 前端与原生/安装器由 `scripts/brand-sync.py` 生成或回填，`make brand-check` 校验；
  - i18n 自动注入 `{appName}`，文案写占位符即可。
- **不纳入品牌的安装/协议身份**：Go module、二进制名、配置目录、环境变量、HTTP 头、
  JSON 字段、桥对象名、Windows 注册表键/计划任务/互斥体、release notes 标题前缀、
  `[MindFS live-compact]` 标记。改名会破坏升级、卸载或解析，故意固定。

## 验收

- `go test ./cli/cmd -run 'TestDesktopIcon|TestDesktopTitle|TestTrayMenu'`
- `go test ./internal/brand ./internal/deploy`
- `make brand-check`
- `cd web && npm run typecheck && node --test tests/brand-single-source.test.mjs`
- Windows 交叉编译后解析 exe 资源：`RT_GROUP_ICON` 只有 ID 1，窗口 `IconId=1`。
- 真机待验：`pc` 重启桌面壳后任务栏显示应用图标与「MindFS · <节点名>」。