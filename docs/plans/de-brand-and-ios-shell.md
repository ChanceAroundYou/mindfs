# 去上游化：抹掉 agent9 痕迹 + iPad 套壳

状态：Plan 2026-11-07，**P1–P5 已收尾（2026-11-08）**。部署 `21a6feef`：WSL/fn/pc-win 已装 v0.5.5-1115-14 并独立核对在跑；本机 2026-11-08 12:01 重启后真机 `GET /api/app/update` 实测 `current_version=v0.5.5-1115-14-g21a6feef`、`latest_version=0.5.5-1115`、`release_url=https://github.com/ChanceAroundYou/mindfs/...`、`has_update=false` —— P1 落地。范围由用户 2026-11-07 明确拍定（四问四答），本文是可执行清单与验收判据。

> 实现进度（2026-11-07）：P1 / P2 / P3 已落盘，登记在 `docs/upstream-customizations.yaml` 的 **G-KM**，
> 守卫是 `web/tests/agent9-traces.test.mjs`（身份单源 + 无 agent9 痕迹 + 版本公式单源）与
> `web/tests/upstream-removals.test.mjs`（harmony 目录/接线/安卓 relay 桥的复活检测），两条都做过变异验证。
> 本机生效仍需用户 `sudo systemctl restart mindfs` 后重跑 P1 的真机验收。

## 用户已定的四条决定

| 决定 | 内容 |
|---|---|
| 上游血缘 | **保留** `upstream` remote 与 `docs/upstream-customizations.yaml` 定制清单 —— 以后还能合上游修复；agent9 的品牌/relay/鸿蒙壳靠守卫测试钉住不许回来 |
| 鸿蒙壳 | **删** `harmony/`，连同前端 harmony 接线与 `make build-harmony` |
| iOS 路线 | **Capacitor iOS 工程**（与安卓同一工具链） |
| 构建条件 | 自用 iPad，要免费且方便 —— 接受「必须借一次 Mac 构建/签名」，不买 $99 账号 |

## 为什么必须先做第 1 阶段（实测事实，不是推测）

本机 `GET /mindfs/api/app/update` 实测返回：

```
current_version: v0.5.5-1115-1-gf12a9ed7   （我们的构建）
latest_version:  0.5.9   has_update: true  （上游 a9gent/mindfs 的 release）
release_url:     https://github.com/a9gent/mindfs/releases/tag/v0.5.9
release_body:    「…增加 直连/更随系统/自动 三种 relay 连接模式设置」
message:         Auto update is unavailable because the release verification public key is not configured.
```

**四个节点的自动更新检查现在跑在上游的发布通道上**，界面上显示「可更新到 v0.5.9」——那是带 relay 的上游构建。挡住它的只有「校验公钥未配置」这一条意外状态。品牌清得再干净，一次点击就能把上游二进制装回生产。

版本比较语义核对的结果**比预想更糟**（`server/internal/update/version.go:parseVersionParts`）：它把版本截断到第一个非数字段，于是 `v0.5.5-1115-1-gf12a9ed7`、`v0.5.5-1115`、`v0.5.5-1116` 全都解析成 `[0,5,5]`。所以「repoint 后自指不报更新」确实成立，但**我们自己的构建号更新永远看不见** —— repoint 只解决「认错了上游」，不解决「看不见自己的新版本」。同一件事在 gradle 的 versionCode 与前端 `appUpdate.ts` 又各有一份实现，三处一起改成「semver 核心 + 构建号」（`-` 是分隔符，`git describe` 的 `-<n>-g<sha>` / `-dirty` 装饰整块剥掉）。gradle 侧另有一处独立缺陷：旧公式给 build 只留 `10^3`，四位构建号溢出进 patch 槽（`0.5.5-1115` 算出 506116、`0.5.6` 只有 506001 → 安装器把 0.5.6 判成更旧、拒绝安装），分段宽度改为 build 占 `10^4` 后序关系恢复，且新号码整体更大，装着旧公式包的机器能直接升上来。

## 阶段

### P1 更新与安装通道 repoint 到我们自己的仓库（功能，最高优先）✅ 已实现（2026-11-07）

| 位置 | 改法 |
|---|---|
| `server/app/server.go:53` | `update.NewService("a9gent/mindfs", …)` 的 repo 身份改成单源常量（我们仓库）；不许再出现第二处字面量 |
| `scripts/sign-release-manifest.go:41` + `Makefile:215,240` | 签名清单里记录的 repo 身份同步改成我们仓库（`-repo` 缺省值与两处显式传参） |
| `scripts/install.sh:7,9` / `scripts/install.ps1:13,15` | `REPO` 改我们仓库；**删 `RELAY_DOWNLOAD_BASE`**（relay 早已删，这条外连是「删了一半」） |
| `cli/cmd/autostart_unix.go:19,77,113` | macOS LaunchAgent 标签 `com.a9gent.mindfs` → 我们的标签；卸载/清理要同时清旧标签（存量 macOS 安装的残留） |
| `cli/cmd/service_control.go:72,75,82,87` | 卸载提示里的下载 URL 指向我们仓库 |
| `server/internal/update/service_test.go` | fixture 里的 `a9gent/mindfs` 换成我们仓库（测试数据也是痕迹） |

验收（已做到的部分）：
- 新守卫 `web/tests/agent9-traces.test.mjs`：代码与构建脚本里 `<owner>/mindfs` 字面量只允许 `ChanceAroundYou`（合成夹具 `example/` 例外）；`a9gent` 字面量逐文件点名允许（血缘注释、旧标签清理、黑名单声明本身）；两处 `update.NewService` 必须写成 `deploy.ReleaseRepo`；Makefile / install.sh / install.ps1 / 前端壳四处字面量必须与常量逐字相同；版本公式三处同源。
- 变异验证已做：`server/app/server.go` 改回 `update.NewService("a9gent/mindfs", …)` → 红；`internal/deploy/release.go` 常量改回 `a9gent/mindfs` → 红；还原后两文件 sha256 逐字一致。
- 新单测 `TestVersionCompareForThisForksTagScheme`（`server/internal/update/service_test.go`）钉四对：自指 tag 对 git-describe 串 = 不报更新、tag 对已装 release = 不报更新、`v0.5.5-1116` 对 `v0.5.5-1115-1-g…` = 报更新、外来 `v0.5.9` = 报更新（合上游时会照单收）。
- **待用户重启后重跑**：真机 `GET /api/app/update` 的 `latest_version` 必须是我们的 tag、`release_url` 是 `github.com/ChanceAroundYou/mindfs`。

### P2 品牌、外链与文档残留 ✅ 已实现（2026-11-07）

- 重写 `README.md` / `README.zh.md`：删「官网 relay.a9gent.com、Discord、Twitter、微信群、竞品对比报告」；删 `### Enable Remote Access (Optional)`（让你登录 a9gent.com 绑定的那节）与 `## wechat-group`；安装命令改指向我们仓库；`### Access Modes` 只留 Local / 私有网络。
- 删除：`docs/remote-vibe-project-comparison.md`、`docs/remote-vibe-project-comparison.pdf`、`docs/images/mindfs-wechat-group.webp`（删前核对无其它引用）。
- `agents.json` 删死键 `tokenStationURL`（连同 Go 侧读取字段，若 `agent/config.go` 仍解析它）。
- `design/json-render-quick-start.md`：**决定保留**（与本文最初的计划相反）。判据：`@json-render/core|react|shadcn` 是 `web/package.json` 的活依赖、被 `web/src/renderer/registry.tsx` 懒加载使用，文件里也没有任何 a9gent 字样 —— 删这份文档不会删依赖，只会让渲染层失去说明。守卫测试扫的是 `a9gent` 字面量，它不命中，所以不构成痕迹。
- 顺带清掉 `server/internal/agent/config_load_test.go` 夹具里残留的 `tokenStationURL`（键已删、夹具只是没跟上）。
- 顺带核一遍 i18n 死键（`upstream-removals.test.mjs:154` 那类「入口删了、文案还在」）。

验收：守卫测试扫 README 两份 + docs，命中 `a9gent|relay.a9gent|token.a9gent|discord|yandc18|微信群` 即红。注意 `upstream-removals.test.mjs` 现有注释写「README 与 upstream/main 逐字节相同（改它等于新增一份定制）」—— 这条前提被本次决定作废，注释与检测范围都要随之改。

### P3 删鸿蒙壳与 harmony 接线 ✅ 已实现（2026-11-07）

实际做完的比原清单多一块：**安卓壳里只服务鸿蒙注入脚本的 relay 残留**也一并清了 ——
`MainActivity.LauncherNodeSyncBridge`（JS 接口 `MindFSLauncherNodeSync`，只有一个 `storeRelayNodes`）与
`LauncherNodeSyncPlugin` 的 `storeRelayNodes` / `consumeRelayNodes` / `storeRelayNodesJSON` / `KEY_PENDING_RELAY_NODES`。
判据：全仓 grep 确认鸿蒙注入脚本是**唯一**调用方，鸿蒙删掉它们就是死代码；安卓的节点同步走 Capacitor 插件
（`LauncherNodeSync.getLauncherNodes/setLauncherNodes`），与 relay 无关。前端侧 `NativeBridge.consumeRelayNodes` 同一轮删掉。

- 删目录 `harmony/`（23 个文件，上游建的壳）—— 已 `git rm -r harmony`。
- `Makefile`：删 `build-harmony`、`HARMONY_DIR`、`HARMONY_DIST_HAP`、`HARMONY_BUILD_MODE`、`DEVECO_*`、help 行、`.PHONY` 项（原地留一行说明：合上游时不要带回来）。
- `web/package.json`：删 `build:harmony` 脚本。
- 前端：`services/platform/runtime.ts`（`NativePlatform` 去 `harmony`、加 `ios`、`isHarmonyRuntime` 删）、`nativeBridge.ts`（`MindFSHarmony` 桥 + `consumeRelayNodes`）、`update/appUpdate.ts`（harmony 分支、`.hap` 包名、"HarmonyOS" 标签）、`update/githubAppUpdate.ts`（判别注释改为 iOS）。
- `.gitignore` 的 harmony 条目（5 行）。
- 测试接线：`web/tests/apk-update-manifest.test.ts` 的 hap 期望换成 `.deb` / 别家平台包（钉的是「非 APK 不许当 APK」这一层，不是鸿蒙这个名字）；`upstream-removals.test.mjs` 把 harmony 从「文件必须存在」改成**目录级 + 接线级**复活检测（`harmony/` 出现任何文件即红；Makefile 的 `build-harmony:` / `HARMONY_* ?=` 赋值、`package.json` 的 `build:harmony`、`NativePlatform` 里的 harmony、`MindFSHarmony` 探测、`isHarmonyRuntime` 各自钉住），并新增安卓 relay 桥的复活检测。
  注释里的说明文字刻意**不被检测**（只钉接线）—— 那些文字是留给合上游的人看的判据。

验收实测：`harmony` 在代码与构建脚本里零命中（只剩 Makefile / 前端注释与登记清单里的说明）；
`make test` 全绿；Vitest 全量绿；`make build-android`（含改过的两个 Java 文件）BUILD SUCCESSFUL。
两条新守卫各做过变异验证（改回 `build-harmony`/`HARMONY_DIR`、重建 `harmony/`、加回 `MindFSHarmony?:` 都当场红，还原后 sha256 逐字一致）。

### P4 iPad 套壳（Capacitor iOS）✅ 已实现（2026-11-07）

实现进度：`npx cap add ios` 已生成 `web/ios/App`（工程入仓，与 `web/android` 对称）；
`build:shell` → `dist-shell/`（安卓/iOS 共用，`webDir` 只支持一个值）；
`runtime.ts` 认 `ios` 平台；
新增 `supportsCustomNativePlugins()`（`= !isIOSRuntime()`），**iOS 没有自定义 Swift 插件**——
Capacitor 8 官方插件自动注册，注册自有插件要替换整个自动注册路径；四处安卓自定义插件调用点
（`NativeDownload` / `ReplyPoller` / `LauncherNodeSync` / `NativeCacheControl`）在 iOS 上短路，
改走官方插件：下载 → `@capacitor/filesystem` 写 `Documents/MindFS/`、
外链 → `@capacitor/browser` `Browser.open()`、
版本信息 → `@capacitor/app` `App.getInfo()`。
「检查更新」保持安卓独占；关于弹窗的假版本号已换成真实 `getAppInfo()`；
「测试提醒」在 iOS 上不显示。
`docs/ios-shell.md` 写了装机三条路（借 Mac Xcode 直装 / Windows Sideloadly / Safari 主屏）
与明确不做清单；`scripts/build-ipa.sh` 已落地（Xcode 归档→导出，版本号从 `git describe` 读）。
守卫 `web/tests/ios-shell.test.mjs` 钉五项接线并对三处做过变异验证。
登记 `docs/upstream-customizations.yaml` 的 **G-KN**。
Mac 才能验的部分（xcodebuild 出包、真机首屏）仍未做，见下方风险 4。

（原计划原文：）

壳的形态与安卓一致：Capacitor 的 WebView 直接加载我们的线上站点（`capacitor.config.json` 的 `server.url`），原生能力靠注入 JS 桥，不靠前端打包本地 bundle。

- `npx cap add ios` 生成 `web/ios/App`（工程入仓；`web/android` 已在仓里，对称）。
- `capacitor.config.json`：`webDir` 目前只有 `dist-android`，iOS 需要构建产物对齐（新增 `build:ios` 走同一份 shell bundle，`cap sync ios`）。
- 前端：`runtime.ts` 认 `ios` 平台（`Capacitor.getPlatform()==="ios"` → `"ios"`），据此关闭 SW 注册与 PWA 安装提示、走原生 shell 的 API base 规则；`isIOSRuntime()`。
- 原生侧（Swift）：在 iOS 壳里注册 `WKScriptMessageHandler` 注入 `MindFS*` 桥的最小实现 —— v1 只做 **外部浏览器打开、剪贴板、下载/存文件、App 版本信息**；状态栏用现成 `@capacitor/status-bar`。
- **明确不做**（写进代码注释与文档，避免将来有人以为是 bug）：iOS 后台回复轮询与 APNs 推送。理由：APNs 需要付费开发者账号 + 服务端推送密钥，用户明确不自用付费；v1 的回复提醒靠前端既有能力（提示音 + 未读徽标 + `useReplyingSessionPoll` 的 5s/visible-only 轮询），切后台可能被系统杀掉是 iOS 壳的已知代价。
- 交付脚本与文档：`scripts/build-ipa.sh`（在 Mac 上跑：`make build-ios` → `cap sync ios` → `xcodebuild` 出 `.ipa`）+ `docs/ios-shell.md`（自用免费装机三条路：借 Mac 用 Xcode 直装 iPad / Windows 上 Sideloadly 装自签 ipa（pc-win 可用）/ 完全无 Mac 就 Safari 添加到主屏；7 天有效期与续期做法、UDID 与信任企业证书步骤）。
- 守卫测试：`web/tests/ios-shell.test.mjs` —— 钉 `ios` 平台识别、SW/安装提示在 iOS 下关闭、`build-ios` target 存在、`web/ios/App` 关键文件在位。

无 Mac 也能验的部分：Capacitor 配置与工程生成、`npm run build:ios`、Vitest/tsc 门禁、前端接线。**必须 Mac 才能验的部分**：`xcodebuild` 出包、真机装与首屏表现 —— 我会把这两件明确留在交付清单里，不谎报。

### P5 登记、门禁、收尾

- `docs/upstream-customizations.yaml`：新开一组登记「去上游化（更新通道/品牌/鸿蒙壳删除）」与「iOS 壳」（两组各自的互斥边界独立：前者是「合上游时这些必须留删」，后者是「上游没有的自有壳」）；`upstream-removals.test.mjs` 补 harmony / 品牌 / repo 身份的复活检测。
- 门禁：`make test`、`web` Vitest 全量、`tsc --noEmit`、`make check-upstream`（保留 upstream ref，按用户决定继续跑）。
- 收尾：`bash scripts/deploy-all.sh`（含 pc-win，按新脚本认 PID）；本机 `sudo systemctl restart mindfs` 由用户执行；重启后重跑 P1 的 `GET /api/app/update` 真机验收。

## 风险与待确认

1. **repoint 后自动更新只认我们的 release**：`make release` 必须继续产出全部平台资产（已核：v0.5.5-1115 有 10 个资产含 manifest）；若某平台漏发，节点会「查不到更新」而不是报错 —— 要在 `make release` 的资产清单里加校验。
2. **签名身份变更**：manifest 里记录的 `-repo` 变了，已装节点的校验公钥未配置（实测 message），所以切换过程不会打破校验；但若将来配置公钥，必须与签名身份同时一致。
3. **`harmony/` 删除不可逆于工作树**：git 历史可恢复，且 `upstream-removals` 会把「它回来」变成红灯 —— 符合用户「保留清单继续生效」的选择。
4. iOS 真机构建/装机需要一次 Mac 接触；没有 Mac 期间 iPad 用 Safari 主屏方式过渡（manifest 已有暗/亮两份、图标与键盘适配已在）。
