package main

const (
	windowsCreateNewProcessGroup = uint32(0x00000200)
	windowsDetachedProcess       = uint32(0x00000008)
	windowsCreateNoWindow        = uint32(0x08000000)
)

// DETACHED_PROCESS 只保证「不挂在启动它的那个终端上」：控制台子系统程序在没有控制台时
// Windows 会**新建一个**，于是每次登录都弹一个黑框（实测 PC 上现在这个实例就是这么起的）。
// 两个标志要一起给：脱离启动终端 + 不分配新控制台。
func windowsBackgroundCreationFlags() uint32 {
	return windowsCreateNewProcessGroup | windowsDetachedProcess | windowsCreateNoWindow
}
