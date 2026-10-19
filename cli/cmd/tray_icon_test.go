package main

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 合成一个只含目录表的 .ico：每张图的“数据”就是它的宽度那一个字节，方便断言挑中了哪张。
func syntheticICO(sizes ...int) []byte {
	header := make([]byte, 6)
	binary.LittleEndian.PutUint16(header[2:4], 1)
	binary.LittleEndian.PutUint16(header[4:6], uint16(len(sizes)))
	body := make([]byte, 0, len(sizes)*16+len(sizes))
	images := make([]byte, 0, len(sizes))
	offset := 6 + len(sizes)*16
	for _, size := range sizes {
		entry := make([]byte, 16)
		entry[0] = byte(size)
		entry[1] = byte(size)
		binary.LittleEndian.PutUint32(entry[8:12], 1)
		binary.LittleEndian.PutUint32(entry[12:16], uint32(offset+len(images)))
		body = append(body, entry...)
		images = append(images, byte(size))
	}
	return append(append(header, body...), images...)
}

func TestPickTrayIconImageChoosesClosestSize(t *testing.T) {
	data := syntheticICO(48, 32, 16)
	for _, want := range []struct {
		ask  int
		byte byte
	}{{16, 16}, {24, 32}, {32, 32}, {48, 48}, {256, 48}} {
		image, err := pickTrayIconImage(data, want.ask)
		if err != nil {
			t.Fatalf("want=%d: %v", want.ask, err)
		}
		if len(image) != 1 || image[0] != want.byte {
			t.Fatalf("want=%d picked %v, want the %dpx image", want.ask, image, want.byte)
		}
	}
}

// .ico 里宽度 0 表示 256 —— 256 那张在托盘尺寸下永远是最后选择，但解析不能因此错位。
func TestPickTrayIconImageTreatsZeroAs256(t *testing.T) {
	image, err := pickTrayIconImage(syntheticICO(0, 16), 16)
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	if len(image) != 1 || image[0] != 16 {
		t.Fatalf("picked %v, want the 16px image", image)
	}
}

func TestPickTrayIconImageRejectsBadInput(t *testing.T) {
	for name, data := range map[string][]byte{
		"empty":            nil,
		"not an icon":      []byte("GIF89a\x00\x00"),
		"no images":        {0, 0, 1, 0, 0, 0},
		"truncated dirent": {0, 0, 1, 0, 2, 0, 16, 16},
		"image past end":   syntheticICO(16)[:20],
	} {
		if _, err := pickTrayIconImage(data, 16); err == nil {
			t.Fatalf("%s: expected an error", name)
		}
	}
}

// 托盘图标用的就是网页那份应用图标（web/public/pwa-512.png），转换后随二进制一起嵌入。
// 这里钉住「资产还在、还是能被 CreateIconFromResourceEx 直接吃的 BMP 位图」——
// 换成 PNG 压缩的 .ico 或漏提交资产都会在这里断。
func TestTrayIconAssetIsEmbeddableBMP(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("assets", "tray.ico"))
	if err != nil {
		t.Fatalf("read tray icon asset: %v", err)
	}
	image, err := pickTrayIconImage(data, 16)
	if err != nil {
		t.Fatalf("parse tray icon asset: %v", err)
	}
	if got := binary.LittleEndian.Uint32(image[0:4]); got != 40 {
		t.Fatalf("icon image starts with %d, want a 40-byte BITMAPINFOHEADER (BMP, not PNG)", got)
	}
	if len(image) < 16 {
		t.Fatalf("icon image is %d bytes, too small to be a bitmap", len(image))
	}
}

// 黑框的根源是控制台子系统：被没有控制台的启动方拉起时 Windows 会新建一个控制台窗口。
// 只有 Windows 目标编成 GUI 子系统才彻底没有那个窗口，CLI 输出靠 attachParentConsole 找回。
func TestWindowsTargetsBuildAsGUISubsystem(t *testing.T) {
	script, err := os.ReadFile(filepath.Join("..", "..", "scripts", "build-all.sh"))
	if err != nil {
		t.Fatalf("read scripts/build-all.sh: %v", err)
	}
	text := string(script)
	if !strings.Contains(text, `"$GOOS" == "windows"`) {
		t.Fatal("build-all.sh must special-case the windows target for the GUI subsystem flag")
	}
	if !strings.Contains(text, "-H=windowsgui") {
		t.Fatal("windows builds must pass -H=windowsgui, otherwise the console window comes back")
	}
	if !strings.Contains(text, `PLATFORM_LDFLAGS="${LDFLAGS} -H=windowsgui"`) {
		t.Fatal("the GUI subsystem flag must be appended to the shared ldflags, not replace them")
	}
}
