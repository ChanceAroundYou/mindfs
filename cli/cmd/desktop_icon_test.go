package main

import (
	"encoding/binary"
	"os"
	"slices"
	"strings"
	"testing"
)

// Windows 任务栏图标曾经是整个空白方块：go-webview2 在 IconId 未设置时加载
// IDI_APPLICATION(32512)，而 rsrc_windows_*.syso 里只有 ID 1 的 RT_GROUP_ICON，
// LoadImageW 失败后窗口类拿到 NULL 图标。这里钉住「常量 = .syso 里的组图标 ID」，
// 以及 desktop_windows.go 真的把常量传给了 WindowOptions.IconId。
func TestDesktopIconResourceIDMatchesWindowsResources(t *testing.T) {
	for _, name := range []string{"rsrc_windows_amd64.syso", "rsrc_windows_arm64.syso"} {
		ids := peGroupIconIDs(t, name)
		if !slices.Contains(ids, uint32(desktopIconResourceID)) {
			t.Fatalf("%s 的 RT_GROUP_ICON ID = %v，desktopIconResourceID = %d：任务栏图标会加载失败",
				name, ids, desktopIconResourceID)
		}
	}

	src, err := os.ReadFile("desktop_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "IconId: desktopIconResourceID") {
		t.Fatal("desktop_windows.go 必须把 desktopIconResourceID 传给 webview2.WindowOptions.IconId")
	}
}

// peGroupIconIDs 解析 PE 资源目录，返回 RT_GROUP_ICON(14) 下的整数名称（组图标 ID）。
func peGroupIconIDs(t *testing.T, path string) []uint32 {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 20 {
		t.Fatalf("%s: truncated file", path)
	}
	resOff := -1
	if string(data[0:2]) == "MZ" {
		// 完整 PE（将来确认层若直接分析 exe 也走这里）。
		peOff := int(binary.LittleEndian.Uint32(data[0x3c:]))
		if peOff+24 > len(data) || string(data[peOff:peOff+4]) != "PE\x00\x00" {
			t.Fatalf("%s: not a PE file", path)
		}
		coff := peOff + 4
		numSections := int(binary.LittleEndian.Uint16(data[coff+2:]))
		optSize := int(binary.LittleEndian.Uint16(data[coff+16:]))
		opt := coff + 20
		if opt+2 > len(data) {
			t.Fatalf("%s: truncated optional header", path)
		}
		ddOff := opt + 96
		if binary.LittleEndian.Uint16(data[opt:]) == 0x20b {
			ddOff = opt + 112
		}
		if ddOff+24 > len(data) {
			t.Fatalf("%s: missing data directories", path)
		}
		// 数据目录 index 2 是资源表。
		resRVA := int(binary.LittleEndian.Uint32(data[ddOff+2*8:]))
		resSize := int(binary.LittleEndian.Uint32(data[ddOff+2*8+4:]))
		if resRVA == 0 || resSize == 0 {
			t.Fatalf("%s: no resource directory", path)
		}
		secOff := opt + optSize
		for i := 0; i < numSections; i++ {
			s := secOff + i*40
			if s+40 > len(data) {
				t.Fatalf("%s: truncated section table", path)
			}
			va := int(binary.LittleEndian.Uint32(data[s+12:]))
			rawSize := int(binary.LittleEndian.Uint32(data[s+16:]))
			rawPtr := int(binary.LittleEndian.Uint32(data[s+20:]))
			if resRVA >= va && resRVA < va+rawSize {
				resOff = rawPtr + (resRVA - va)
				break
			}
		}
	} else {
		// Go 的 .syso 是 COFF 对象，没有 PE 头/可选头；资源目录直接位于 .rsrc 段首。
		numSections := int(binary.LittleEndian.Uint16(data[2:]))
		secOff := 20
		for i := 0; i < numSections; i++ {
			s := secOff + i*40
			if s+40 > len(data) {
				t.Fatalf("%s: truncated section table", path)
			}
			name := strings.TrimRight(string(data[s:s+8]), "\x00")
			if name == ".rsrc" {
				rawPtr := int(binary.LittleEndian.Uint32(data[s+20:]))
				rawSize := int(binary.LittleEndian.Uint32(data[s+16:]))
				if rawPtr+rawSize > len(data) {
					t.Fatalf("%s: .rsrc section out of range", path)
				}
				resOff = rawPtr
				break
			}
		}
	}
	if resOff < 0 {
		t.Fatalf("%s: no .rsrc section", path)
	}

	var ids []uint32
	var walk func(off int, level int)
	walk = func(dirOff int, level int) {
		if dirOff+16 > len(data) {
			t.Fatalf("%s: resource directory out of range", path)
		}
		named := int(binary.LittleEndian.Uint16(data[dirOff+12:]))
		entries := int(binary.LittleEndian.Uint16(data[dirOff+14:]))
		for i := 0; i < named+entries; i++ {
			e := dirOff + 16 + i*8
			if e+8 > len(data) {
				t.Fatalf("%s: resource entry out of range", path)
			}
			name := binary.LittleEndian.Uint32(data[e:])
			child := binary.LittleEndian.Uint32(data[e+4:])
			if name&0x80000000 != 0 {
				continue // 字符串命名项；组图标用整数 ID
			}
			subdir := child&0x80000000 != 0
			childOff := resOff + int(child&0x7fffffff)
			if level == 0 {
				if name == 14 && subdir { // RT_GROUP_ICON
					walk(childOff, 1)
				}
				continue
			}
			ids = append(ids, name)
		}
	}
	walk(resOff, 0)
	if len(ids) == 0 {
		t.Fatalf("%s: no RT_GROUP_ICON entries", path)
	}
	return ids
}
