---
name: apk-release
description: 发布一个版本：写发布说明 → 打 tag → make release（含签名与 Android APK）→ 验线上发布清单与 APK 签名。触发词：发布版本/发版/出 APK/发安卓包/release/新版本/tag 一个版本
---

# APK / 版本发布

> `ship` 管「代码到三台机器」，本 skill 管「代码到 GitHub 发布页 + 安卓客户端的更新清单」。
> 两者独立：ship 不需要发版，发版前代码必须已经 ship 过。

## 前置（三样，缺一样都会在半路报错）

| 东西 | 位置 / 传法 | 为什么必须 |
|---|---|---|
| 发布说明 | 仓库根 `release-notes.md` **第一行**的版本必须**逐字等于** `TAG` | `make release` 第一步就比对，不匹配直接退出 |
| 公钥 | `MINDFS_RELEASE_PUBLIC_KEY=<值>` | 自动更新的 manifest 要签名；缺了就报错退出 |
| 私钥 | **只用** `MINDFS_RELEASE_PRIVATE_KEY_FILE=~/.android/keys/mindfs-release.key` | 签名需要私钥。**永远不要把私钥值写进命令行、日志或提交**；`mindfs-release.key` 是 44 字节 base64（32 字节 Ed25519 seed），另有 `.pub` 与 `.jks`（Android 签名库，`web/android/keystore.properties` 指向它） |

Android SDK：本机 `ANDROID_HOME` / `ANDROID_SDK_ROOT` **都是 unset**（SDK 在 `~/Android/Sdk`），
`build-android` / `android-verify` 前要显式带上，否则 gradle 与 `apksigner` 找不到东西。

## 流程

```bash
# 0. 前提：改动已合进 main 并已 ship（走 /ship）。发布一个正在飞的分支没有意义。

# 1. 写发布说明 —— 顶部标题必须是 `# MindFS v0.5.5-1115`（版本与 TAG 逐字一致）
#    条目按「用户看到什么」写，别写内部重构。历史上整段条目被上游合并冲掉过，写完核对还在。

# 2. 打 tag（会先 push main）
make tag TAG=v0.5.5-1115

# 3. 发布：GH_REPO 必须显式指定
GH_REPO=ChanceAroundYou/mindfs \
  ANDROID_HOME="$HOME/Android/Sdk" ANDROID_SDK_ROOT="$HOME/Android/Sdk" \
  MINDFS_RELEASE_PUBLIC_KEY="$(cat ~/.android/keys/mindfs-release.pub)" \
  MINDFS_RELEASE_PRIVATE_KEY_FILE="$HOME/.android/keys/mindfs-release.key" \
  make release TAG=v0.5.5-1115 RELEASE_ANDROID=1

# 4. 验 APK（签名与哈希；android-verify 硬用 build-tools/36.0.0/apksigner）
ANDROID_HOME="$HOME/Android/Sdk" make android-verify

# 5. 验线上：安卓客户端读的就是这条固定 URL（不是 api.github.com）
curl -sL https://github.com/ChanceAroundYou/mindfs/releases/latest/download/android-version.json
```

`make release` 内部顺序：`dist-clean` → `build-all`（7 平台）→ 若 `RELEASE_ANDROID=1`：`build-android`
→ `android-version-json` → `sign-release-manifest` → `verify-release` → `gh release create --draft`
→ 并发上传产物 → `gh release edit --draft=false` → `publish-release-notes`。

## 两个坑（都实际踩过）

1. **`GH_REPO` 不显式指定会推到上游仓库**。本仓库同时挂了 `origin`（`ChanceAroundYou/mindfs`）与
   `upstream`（`a9gent/mindfs`），gh 挑仓库时可能选中 upstream，卡在 `gh release create`。
   **失败点在创建之前，重跑安全**（`dist-clean` 会重来，不会有半截发布）。
   注意签名那一步的 `-repo` 是**硬编码 `a9gent/mindfs`**（manifest 里记的仓库身份），与 `gh` 推到哪
   是两件事 —— 看到 `a9gent/mindfs` 不代表推错了地方。
2. **版本号递增看 `versionCode`，不是看 tag 尾号**：
   `versionCode = major*10000000 + minor*100000 + patch*1000 + build + 1`（gradle 里算，tag 尾号即 build）。
   Android 只认 `versionCode` 更大才肯更新，所以 tag 命名 `vX.Y.Z-NNNN` 的尾号必须**单调递增**。

## 走完要报什么

别只说「发布成功」。给出可核对的事实：

- tag 与 `git describe` 出来的版本串（worker 上跑的就是这个串）
- `apksigner verify --print-certs` 的结果（应见 `CN=MindFS`）与 APK 的 sha256
- `make verify-release` 通过（manifest 签名可被公钥验回）
- 线上 `android-version.json` 的实际返回：版本字段 == TAG，且带 `.apk` 直链

## 常见后续

- **只想装到手上这台安卓机**：`make android-install`（走 adb，含 `build-android`）。
- **先看看环境齐不齐**：`make android-doctor`。
- **发完要上线三台机器**：那是 `/ship` 的活，不在这里做。
- **发布说明漏了一段**：`release-notes.md` 是 `publish-release-notes` 的输入，改完重跑第 3 步会覆盖
  `dist/release-notes-<TAG>.md`；已发布的 release 正文要 `gh release edit <TAG> --notes-file ...` 单独改。
- **`make release` 中途失败**：先看它死在哪一步（版本号不匹配 / 缺公钥 / gradle / `gh`），
  不要盲目重跑整包 —— 前四种都在创建 release 之前，重跑安全；**上传之后**失败要先看
  `gh release list` 有没有留下 draft，有就 `gh release delete <TAG> --cleanup-pull-requests` 再重来。
