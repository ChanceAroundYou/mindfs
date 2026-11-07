# iOS 壳（iPad / iPhone）

MindFS 的 iOS 壳是一个 Capacitor App：WebView 直接加载线上站点（`web/capacitor.config.json` 的
`server.url = https://home.xiaokubao.space/mindfs/`），前端与安卓壳共用同一份 shell bundle
（`npm run build:shell` → `dist-shell/`，安卓与 iOS 都是 `cap sync` 到各自工程）。

本文写给「自己用、免费、iPad 优先」这一种场景。iOS 壳不是 `make release` 的产物，
不会出现在任何 GitHub release 里，也不会推给任何节点（见下「明确不做」）。

## 出包（只能在 Mac 上）

```bash
DEVELOPMENT_TEAM=ABCDE12345 bash scripts/build-ipa.sh
```

Team ID 是你 Apple ID 的 10 位编号（Xcode → Settings → Accounts → Personal Team）。
脚本做：`make build-ios`（shell bundle → `cap sync ios`）→ `xcodebuild archive` →
导出 `.ipa` 到 `dist/mindfs_<版本>_ios.ipa`。版本从 `git describe` 读，
`MARKETING_VERSION` 取 semver 三段、`CURRENT_PROJECT_VERSION` 取构建号
（与 `parseVersionParts` / `appVersion.ts` 同一读法）。

其它入口：

- `bash scripts/build-ipa.sh --build-only` — 只编译不出 ipa（第一次排错用）
- `bash scripts/build-ipa.sh --open` — 只同步资源并打开 Xcode（手点 Run 直接装到连着的 iPad）
- `make build-ios` — 只同步资源，不出包（无 Mac 的 Linux 机器上这是能做的全部）

> 本仓库的开发机没有 macOS，`scripts/build-ipa.sh` 未在 Mac 上实跑过。
> 第一次跑请把 `xcodebuild` 的报错原样贴回来，不要自己改工程文件猜。

## 装机三条路（免费自用）

个人免费 Apple ID 签名的 App 只有 **7 天**有效期，过期后 App 打不开；
重跑出包脚本（或 Xcode 重连 iPad 再 Run 一次）即续期。

### 路 1：借一台 Mac + Xcode 直装（最顺）

1. Mac 上装 Xcode，登录你的 Apple ID（免费账号即可）。
2. `DEVELOPMENT_TEAM=<你的 Team ID> bash scripts/build-ipa.sh` 出包，
   或 `--open` 打开工程后连上 iPad 手点 Run。
3. 第一次要在 iPad 里信任证书：设置 → 通用 → VPN 与设备管理 → 你的开发者项 → 信任。

### 路 2：Windows 上 Sideloadly 装自签 ipa（pc-win 可用）

1. Mac 上照路 1 出 `.ipa`（产物在 `dist/`）。
2. 把 ipa 拷到 Windows，用 [Sideloadly](https://sideloadly.com/) 选这个 ipa
   + 同一个 Apple ID → 装进 iPad。
3. 同样要在 iPad 设置里信任该开发者证书。签名 7 天，到期重跑路 1 的出包脚本再装。

### 路 3：完全没有 Mac —— Safari 添加到主屏（零签名）

1. 在 iPad 的 Safari 里打开 `https://home.xiaokubao.space/mindfs/`。
2. 分享 → 添加到主屏幕。

这条路没有签名、没有 7 天问题，功能与 PWA 一致（manifest 有暗/亮两份），
但壳的「壳的窗口底色」等原生细节少于 Capacitor 路线；它也**没有**本地 bundle，
每次打开都走线上站点（壳与 PWA 都是这样，壳只是把 URL 固定住了）。

## iPad 上各能力的实际行为

iOS 壳**没有自定义 Swift 插件**——Capacitor 8 的官方插件是自动注册的，
想加自有原生插件必须替换整个自动注册路径；免费自签场景下这不划算，
而官方插件已经覆盖 v1 需要的全部能力。所以自定义插件的调用点在 iOS 上短路
（`supportsCustomNativePlugins()`），改走官方插件：

| 能力 | 安卓壳 | iOS 壳（本实现） | 用户可见差异 |
|---|---|---|---|
| 下载文件 | 自定义 `NativeDownload` 插件 | 官方 `@capacitor/filesystem` 写到 `Documents/MindFS/` | 在「文件」App 里 `MindFS/` 目录可见；弹窗文案会说明存到哪里 |
| 打开外部链接 | 桥 `openExternalURL` | 官方 `@capacitor/browser` `Browser.open()` | 内置关闭按钮的系统浏览层（WKWebView 里 `window.open` 是空操作，不能用） |
| 剪贴板 | 桥 | 官方 `@capacitor/clipboard`（已有兜底） | 无差异 |
| 壳版本号 | `MindFSAppInfo` 桥 | 官方 `@capacitor/app` `App.getInfo()`（`CFBundleShortVersionString` + 构建号） | 关于弹窗显示真实版本，不再写死 `web (…)` |
| 状态栏 | 自定义 | 官方 `@capacitor/status-bar` | 暗色 `#0f172a`，与 manifest 底色一致 |

还有几处「故意的沉默」，它们不是漏接了：

- 「检查更新」按钮在 iOS 壳里**不显示**：iOS 内部装不了 ipa
  （装必须重新签名），`isUpdatableNativeRuntime()` 保持安卓独占。
  更新 = 重新装：重跑出包脚本装一遍即可。
- 「测试提醒」按钮不显示：那条链路测的是安卓的后台轮询服务，iOS 这边根本没有。
- 节点同步（`launcherNodeSync`）在 iOS 上直接返回空数组：WKWebView 的
  localStorage 本身持久，节点列表就存在登录页的网页存储里，不需要原生侧同步。
- 清理缓存（`nativeCacheControl`）只服务「壳内安装更新包」那条链，
  iOS 没有自更新就没有它。

## 明确不做（写下来，避免有人当 bug 调）

### 1. iOS 后台回复轮询 / APNs 推送

**不做。** 安卓壳靠 `ReplyPollerService` 在后台每 30 秒轮询
`/api/sessions/pending`、完成/提问时发本地通知；iOS 壳把这一整套拿掉。
理由：APNs 需要付费开发者账号（$99/年）+ 服务端推送密钥与设备 token 体系，
与「免费自签、自用」冲突；短期轮询（`BGAppRefreshTask`）也没绕过 Apple 的审核口径，
免费个人签名下更不会稳定运行。

v1 的回复提醒靠前端既有能力：提示音 + 侧栏未读徽标 +
`useReplyingSessionPoll` 的 5s/visible-only 轮询（页面可见时自己查）。
切后台后如果系统把 App 杀掉，回来重开页面会补齐——这是 iOS 壳的已知代价，
不是链路断了。

以后真想做通知，正确的 iOS 路线是付费账号 + 服务端在 session 完成事件时
按 `nodeId::rootId::sessionKey` 投递 APNs，而不是在 App 侧裸轮询。

### 2. iOS 包进 `make release`

**不做。** `.ipa` 需要签名、7 天过期，跟安卓 APK 的「下载安装」模型不对齐；
放进 release 资产会被安卓的自更新认成候选包（`pickNativeDownload` 按 `.apk` 挑，
`.ipa` 不会匹配，但资产清单多一条就是给人留坑）。
`build-ios` 刻意不在 `make release` 里，`web/tests/ios-shell.test.mjs` 钉住了这一点——
把 `build-ios` 加进 release 依赖，守卫就红。

### 3. WKScriptMessageHandler 自定义桥 / `MindFS*` JS 注入

安卓壳里有一层 `MindFS*` 桥（版本信息、下载、剪贴板……）供前端同步/异步调用。
iOS v1 刻意不复刻它：官方插件（App / Browser / Clipboard / Filesystem / StatusBar）
的调用面与前端既有兜底路径已经对齐，且 `supportsCustomNativePlugins()` 只挡
自定义插件，不挡统一桥 `window.MindFSNative` 的识别——将来真需要 Swift 桥
（例如接系统分享面板），再在官方插件之外加，并把判定从「是否 iOS」
换成「是否已注册该插件」即可。

## 工程文件

`web/ios/App` 是 `capacitor add ios` 生成的 Xcode 工程，已入仓（与 `web/android` 对称），
不是构建产物。`cap sync ios` 写进去的 `App/App/public/`（shell bundle 的副本）由
`web/ios/.gitignore` 忽略，只同步资源不出包的那一步也可以在没有 Mac 的 Linux 机器上跑。

守卫测试：`web/tests/ios-shell.test.mjs`（平台识别、自定义插件短路、官方插件接线、
`build:shell`/`build-ios`/`make release` 隔离、Xcode 工程在位与图标非空白）。
