package api

import (
	"path"
	"regexp"
	"strings"

	"mindfs/internal/deploy"
)

// HTML 预览里的「站点根」兜底。
//
// 页面里写 src="/assets/app.js" 是从**站点根**算起的，而在预览里站点根是 mindfs 的
// 服务器根，项目文件并不在那里 —— 结果是 404、页面白屏。把 /x 改写成相对当前文档的
// <深度层 ../>/x 之后，浏览器会解析回原始资源端点（/api/file/raw/{account}/{root}/）下的
// 项目根，无论部署前缀怎么变都对。
//
// 只动 src / href / poster 三个属性；以下情形一律原样返回：
//   - 文档自带 <base>：相对路径的基准不再是文档 URL，凭空改写会指错；
//   - 协议相对 //host/x；
//   - 部署前缀（mindfs 自己的 /mindfs/assets/... 必须继续打到应用）、/api、/ws；
//   - 太大（超过 8MB 不读进内存）。
const maxRewritableHTMLBytes = 8 << 20

var (
	rawHTMLBaseTagPattern = regexp.MustCompile(`(?i)<base[\s>]`)
	rawHTMLRootRefPattern = regexp.MustCompile(`(?i)(\b(?:src|href|poster)\s*=\s*)(["'])/([^"']*)(["'])`)
	rawHTMLDoctypePattern = regexp.MustCompile(`(?i)^\s*<!doctype[^>]*>`)
)

// previewErrorProbeMarker 用来识别「已经注入过」，避免同一份 HTML 被注入两次。
const previewErrorProbeMarker = "mindfs-preview-error-probe"

// previewErrorProbeScript 必须在页面自己的脚本**之前**执行：iframe 的 load 事件在
// 脚本跑完之后才触发，前端到那时再挂监听就只能看到迟到的错误，解析期抛的错全漏。
// 探针把错误回传给父窗口（直接在新标签页打开时 parent === self，无害），前端按
// event.source 校验来源后展示。
const previewErrorProbeScript = `<script>/*` + previewErrorProbeMarker + `*/(function(){` +
	`var list=window.__mindfsPreviewErrors=[];` +
	`function report(msg){if(!msg)return;msg=String(msg);list.push(msg);if(list.length>5)list.shift();` +
	`try{parent.postMessage({__mindfsHtmlPreviewError:msg},"*")}catch(e){}}` +
	`window.addEventListener("error",function(event){` +
	`if(event&&event.message){report(event.message);return;}` +
	`var t=event&&event.target;if(t&&(t.src||t.href)){report(String(t.tagName||"resource").toLowerCase()+": "+(t.src||t.href))}},true);` +
	`window.addEventListener("unhandledrejection",function(event){` +
	`var r=event&&event.reason;report(r&&r.message?r.message:String(r))});` +
	`})();</script>`

// prepareHTMLForPreview 生成实际发给浏览器的预览字节：注入错误探针 + 根绝对路径兜底。
// 传入的字节未被改动时返回原切片。
func prepareHTMLForPreview(raw []byte, relPath string) []byte {
	if len(raw) == 0 || len(raw) > maxRewritableHTMLBytes {
		return raw
	}
	text := injectPreviewErrorProbe(string(raw))
	if rewritten, changed := rewriteRootAbsoluteHTML([]byte(text), relPath); changed {
		return rewritten
	}
	return []byte(text)
}

// injectPreviewErrorProbe 插到 doctype 之后（放在 doctype 之前会把文档打回怪异模式）。
func injectPreviewErrorProbe(text string) string {
	if strings.Contains(text, previewErrorProbeMarker) {
		return text
	}
	index := 0
	if loc := rawHTMLDoctypePattern.FindStringIndex(text); loc != nil {
		index = loc[1]
	}
	return text[:index] + previewErrorProbeScript + text[index:]
}

// rewriteRootAbsoluteHTML 返回改写后的字节与是否发生改动。未改动时调用方应继续用原文件，
// 避免把整份 HTML 读进内存。
func rewriteRootAbsoluteHTML(raw []byte, relPath string) ([]byte, bool) {
	if len(raw) == 0 || len(raw) > maxRewritableHTMLBytes {
		return raw, false
	}
	text := string(raw)
	if rawHTMLBaseTagPattern.MatchString(text) || !rawHTMLRootRefPattern.MatchString(text) {
		return raw, false
	}
	prefix := relativeProjectRootPrefix(relPath)
	changed := false
	rewritten := rawHTMLRootRefPattern.ReplaceAllStringFunc(text, func(match string) string {
		parts := rawHTMLRootRefPattern.FindStringSubmatch(match)
		if len(parts) != 5 {
			return match
		}
		attr, quote, target, closing := parts[1], parts[2], parts[3], parts[4]
		// target 不含前导 /；以 / 开头说明原文是 //host（协议相对），不能碰。
		if target == "" || strings.HasPrefix(target, "/") || skipRootAbsoluteTarget(target) {
			return match
		}
		changed = true
		return attr + quote + prefix + target + closing
	})
	if !changed {
		return raw, false
	}
	return []byte(rewritten), true
}

// relativeProjectRootPrefix 计算「从当前文档所在目录回到项目根」的相对前缀。
// web/dist/index.html → "../../"；index.html → "./"。
func relativeProjectRootPrefix(relPath string) string {
	clean := path.Clean(strings.ReplaceAll(strings.TrimSpace(relPath), "\\", "/"))
	dir := path.Dir(clean)
	if dir == "." || dir == "/" {
		return "./"
	}
	depth := 0
	for _, segment := range strings.Split(dir, "/") {
		if segment != "" && segment != "." {
			depth++
		}
	}
	if depth == 0 {
		return "./"
	}
	return strings.Repeat("../", depth)
}

// skipRootAbsoluteTarget 判断一个根绝对路径是否属于「本来就该打到应用」的地址。
func skipRootAbsoluteTarget(target string) bool {
	lower := strings.ToLower(target)
	if lower == "api" || strings.HasPrefix(lower, "api/") || lower == "ws" {
		return true
	}
	prefix := strings.TrimPrefix(deploy.NormalizedPrefix(), "/")
	if prefix == "" {
		return false
	}
	return lower == prefix || strings.HasPrefix(lower, prefix+"/")
}
