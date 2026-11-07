# 安卓壳键盘弹起时的页面平移锁
> **Shipped 2026-11-07（v0.5.5-1115）** —— 实现提交已落在该 tag 内，产物已装本机 / WSL / fn 并版本逐字一致
> （本机待用户 `sudo systemctl restart mindfs`；pc-win 已装并在跑，见 `windows-worker-on-pc` 记录）。本计划已移入 `docs/plans/README.md` 的历史表，
> 下方「待提交与 ship / 待 ship」是当时的记录，不是当前待办。

- Scope: G-JC
- 状态：Implemented（未 ship）

## 背景

MindFS 安卓 App 在键盘弹起后，用户可把可视视口向上拖出一片 `#0F172A` 空带
（278–484dp），会话头也被推出屏幕。根因与证据见 `~/.claude/plans/app-jazzy-eich.md`：
尺寸是对的（485dp = 键盘上沿），缺陷是 `interactive-widget=resizes-visual` 让布局视口仍 791dp，
而 `installIOSKeyboardPanLock()` 在安卓壳上是 no-op。`window.scrollTo(0,0)` 重置不了可视视口 offset。

## 实现

- `web/src/app/nativeShellUtils.ts`：`installIOSKeyboardPanLock` → `installKeyboardPanLock`；
  新增 `shouldInstallKeyboardPanLock({iosWebKit, androidRuntime})` 与 `shouldBlockKeyboardPan(decision)`；
  平台门禁 `isIOSWebKit() || isAndroidRuntime()`；监听器只在 focusin/focusout 期间挂载；
  输入框内部拖动（`targetIsEditable`）放行。
- `web/src/app/useNativeShellLifecycle.ts`：import 与调用改名。
- `web/src/components/session/SessionViewer.tsx`：滚动容器加 `overscrollBehaviorY: "contain"`。

## 测试

- `web/tests/keyboard-pan-lock.test.mjs`（8 个用例）：判定全分支表驱动 + 平台矩阵 + 源码契约。
- `npm test` 全绿（201 files / 450 tests），`make build-web` 通过。

## 真机验收

- `adb devices` 当前无设备；验收判据已写入 `~/.claude/plans/app-jazzy-eich.md` §验证。
- 若真机仍可拖出空白，升级到原生方案（WebView 底部内缩 + 去掉 JS 侧 ime 扣减）。