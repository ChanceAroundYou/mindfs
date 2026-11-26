# 发布 v0.5.6：修复 NSIS 安装器构建的 payload 路径

> Scope IDs: G-SC（claimed 2026-11-26T17:24Z）

## 症状

`make release TAG=v0.5.6 RELEASE_ANDROID=1` 在 `build-all` 的 NSIS 步骤失败：
`File: "dist/nsis-payload\*.*" -> no files found.`，安装包未生成，发布在 GitHub release 创建之前中止。

## 根因（已复现）

`scripts/build-windows-installer.sh` 把相对路径以 `-DPAYLOAD` / `-DWEBVIEW2_BOOTSTRAPPER` 传给
`mindfs.nsi`，`.nsi` 的 `File /r "${PAYLOAD}\*.*"` 先报 `no files found`；修完 payload 后
`File /oname=… "${WEBVIEW2_BOOTSTRAPPER}"` 以同样原因再报一次。最小 `.nsi` 复现：
相对路径失败、绝对路径正常。

## 修法

构建脚本先把 `DIST_DIR` 转成绝对路径再拼 `nsis-payload`，并把 bootstrapper 路径（含用户
覆盖的相对路径）与 makensis 的 `OutFile` 一起归一成绝对路径；只在脚本内改变传给 makensis
的值，`.nsi` 的匹配方式与安装布局都不动。加一条定向测试钉住这些契约，并用真实 makensis
构建出 setup.exe。

## 边界

只修发布构建路径；不改图标、安装流程、`-DAPP_ICON` 与包内容。修完重跑 `make release`，
不重复 push（tag v0.5.6 已推送，四端已装同一版本）。
