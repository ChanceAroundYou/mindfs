package main

import (
	"encoding/binary"
	"errors"
)

// 托盘图标就是网页那份应用图标（web/public/pwa-512.png，与 favicon 同一套 glyph），
// 构建时转成 assets/tray.ico 一起嵌进二进制 —— 后台实例不保证有 web 资源可读。
//
// Windows 的 CreateIconFromResourceEx 要的是**单张图**的位图数据（也就是 .ico 里 RT_ICON
// 那一份），不是整个 .ico 容器，所以按目录表切出最接近托盘尺寸的那张。
func pickTrayIconImage(data []byte, want int) ([]byte, error) {
	if len(data) < 6 || binary.LittleEndian.Uint16(data[2:4]) != 1 {
		return nil, errors.New("ico: not an icon file")
	}
	count := int(binary.LittleEndian.Uint16(data[4:6]))
	if count == 0 || len(data) < 6+count*16 {
		return nil, errors.New("ico: truncated directory")
	}

	best, bestDelta := -1, 0
	for i := 0; i < count; i++ {
		entry := data[6+i*16 : 6+i*16+16]
		size := int(entry[0])
		if size == 0 {
			size = 256 // .ico 里 0 表示 256
		}
		delta := size - want
		if delta < 0 {
			delta = -delta
		}
		if best < 0 || delta < bestDelta {
			best, bestDelta = i, delta
		}
	}

	entry := data[6+best*16 : 6+best*16+16]
	length := int(binary.LittleEndian.Uint32(entry[8:12]))
	offset := int(binary.LittleEndian.Uint32(entry[12:16]))
	if length <= 0 || offset < 0 || offset+length > len(data) {
		return nil, errors.New("ico: image outside file")
	}
	return data[offset : offset+length], nil
}
