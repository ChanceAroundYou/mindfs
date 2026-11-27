// Package brand 是产品对外名称的唯一来源。
//
// 只有**展示用**的名字、标语和描述放在这里；下面这些是协议或安装身份，改名会破坏
// 升级、卸载、跨版本兼容或代理解析，因此**故意不**走 brand：
//
//   - Go module / 二进制名 / 配置目录（mindfs、~/.config/mindfs、.mindfs）
//   - 环境变量、HTTP 头、JSON 字段、桥对象名（X-MindFS-Local-CLI-Token、mindfs_id、
//     MindFSThemeBackground 等）
//   - Windows 注册表键 / 计划任务名 / 互斥体名（Software\MindFS、mindfs、Local\MindFS-Desktop）
//   - Release 说明的标题前缀 "# MindFS "（server/internal/update 按它解析历史发布）
//   - 压缩指针标记 "[MindFS live-compact]"（fold 解析器按它识别已折叠内容）
//
// 改展示名时只改 brand.json，然后跑 `make brand-sync` 同步前端与原生/安装器产物。
package brand

import (
	_ "embed"
	"encoding/json"
	"strings"
)

//go:embed brand.json
var source []byte

// Profile 是 brand.json 的结构。字段都是展示文本。
type Profile struct {
	Name        string `json:"name"`
	Tagline     string `json:"tagline"`
	Description string `json:"description"`
}

// Current 是编译进二进制的品牌档案。brand.json 在编译期嵌入，运行时不再读文件。
var Current = mustDecode(source)

func mustDecode(raw []byte) Profile {
	var p Profile
	if err := json.Unmarshal(raw, &p); err != nil {
		panic("brand: 无法解析 brand.json: " + err.Error())
	}
	for name, value := range map[string]string{
		"name":        p.Name,
		"tagline":     p.Tagline,
		"description": p.Description,
	} {
		if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) {
			panic("brand: brand.json 的 " + name + " 必须是非空且无首尾空白的字符串")
		}
	}
	return p
}

// Name 返回产品展示名，例如窗口标题、托盘菜单与安装器的产品名。
func Name() string { return Current.Name }

// Tagline 返回一句话定位，例如「关于」对话框里的副标题。
func Tagline() string { return Current.Tagline }

// Description 返回用于 PWA/安装器描述的名词短语（不含产品名）。
func Description() string { return Current.Description }

// Title 生成窗口标题：有节点名时是「<产品名> · <节点名>」，否则只有产品名。
// 节点名与产品名相同时不重复拼接。
func Title(node string) string {
	node = strings.TrimSpace(node)
	if node == "" || node == Current.Name {
		return Current.Name
	}
	return Current.Name + " · " + node
}
