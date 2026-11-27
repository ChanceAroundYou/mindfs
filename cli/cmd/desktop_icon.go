package main

// desktopIconResourceID 必须等于 rsrc_windows_*.syso 里 RT_GROUP_ICON 的资源 ID。
//
// go-webview2 在 IconId==0 时会去加载 IDI_APPLICATION(32512)；.syso 只带了组图标 1，
// LoadImageW 失败后窗口类拿到 NULL 图标，任务栏/Task View 就是空白方块。
// cli/cmd/desktop_icon_test.go 直接解析 .syso 钉住这个值，避免常量与资源各改各的。
const desktopIconResourceID = 1
