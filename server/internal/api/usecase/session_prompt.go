package usecase

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"mindfs/server/internal/fs"
	"mindfs/server/internal/session"
)

func (s *Service) BuildPrompt(in BuildPromptInput) string {
	clientCtx := in.ClientContext
	prompt := buildUserPrompt(in.Message, clientCtx)
	if strings.TrimSpace(clientCtx.PluginCatalog) != "" {
		pluginDir := ".mindfs/plugins"
		if in.Manager != nil && in.Manager.Root().EffectiveMetaLocation() == fs.MetaLocationHome {
			pluginDir = filepath.Join(in.Manager.Root().MetaDir(), "plugins")
		}
		prompt = buildPluginPrompt(clientCtx.PluginCatalog, in.Message, in.IsInitial, pluginDir)
	} else if in.IsInitial && in.IncludeReplyTipsInUserMessage {
		prompt = appendReplyTips(prompt)
	}
	if in.IsInitial && in.Manager != nil && in.Session != nil {
		prompt += fmt.Sprintf("\n\nMindFS context: root_id=%s session_key=%s", in.Manager.Root().ID, in.Session.Key)
		if in.Session.TaskID != "" {
			prompt += " task_id=" + in.Session.TaskID
		}
		prompt += ". If asked to orchestrate tasks, read mindfs -orchestration; create a task group with this parent session_key, then create ordinary template tasks in that group.\n"
	}
	return prependSwitchHint(in, prompt)
}

func prependSwitchHint(in BuildPromptInput, prompt string) string {
	if in.Session == nil || in.Manager == nil {
		return prompt
	}
	currentAgent := strings.TrimSpace(in.Agent)
	if currentAgent == "" {
		return prompt
	}
	total := contextLineCount(in.Session.Exchanges)
	if in.LinesBeforeThisTurn != nil {
		total = *in.LinesBeforeThisTurn
	}
	last := 0
	if in.AgentCtxSeq != nil {
		last = *in.AgentCtxSeq
	} else {
		last = in.Session.AgentCtxSeq[currentAgent]
	}
	linesToRead := calculateSwitchReadLines(total, last)
	if linesToRead <= 0 {
		return prompt
	}
	logPath := switchReadHintPath(in.Manager, in.Session.Key)
	readHint := buildSwitchReadHint(logPath, linesToRead)
	return readHint + prompt
}

func contextLineCount(exchanges []session.Exchange) int {
	return len(exchanges)
}

const replyTips = "[REPLY_TIPS]\n\n" +
	"- Prefer GitHub-flavored Markdown. Fenced `mermaid` diagrams, mathematical formulas, and Markdown images are supported.\n" +
	"- When a useful workspace image exists, embed it with `![alt](path)` and prefer a workspace-root-relative path.\n" +
	"- Never invent file paths or URLs. Use images or diagrams only when they materially improve the reply."

func appendReplyTips(prompt string) string {
	return strings.TrimSpace(prompt) + "\n\n" + replyTips
}

func buildUserPrompt(message string, clientCtx ClientContext) string {
	lines := []string{strings.TrimSpace(message)}
	if clientCtx.Selection != nil {
		lines = append(lines, "[USER_SELECTION]")
		if clientCtx.Selection.FilePath != "" {
			lines = append(lines, "file: "+clientCtx.Selection.FilePath)
		}
		if clientCtx.Selection.StartLine > 0 || clientCtx.Selection.EndLine > 0 {
			lines = append(lines, "line range: "+strconv.Itoa(clientCtx.Selection.StartLine)+"-"+strconv.Itoa(clientCtx.Selection.EndLine))
		}
		if strings.TrimSpace(clientCtx.Selection.Text) != "" {
			lines = append(lines, "selected text: "+clientCtx.Selection.Text)
		}
	}
	return strings.Join(lines, "\n")
}

func buildPluginPrompt(catalogPrompt, userMessage string, isInitial bool, pluginDir string) string {
	if isInitial {
		return buildPluginPromptInitial(catalogPrompt, userMessage, pluginDir)
	}
	return buildPluginPromptFollowup(userMessage, pluginDir)
}

func buildPluginPromptFollowup(userMessage, pluginDir string) string {
	systemPrompt := strings.TrimSpace(strings.Join([]string{
		"You are still in view-plugin development mode.",
		"Continue editing/refining the plugin under " + filepath.ToSlash(pluginDir) + "/.",
		"",
		"Follow these strict constraints:",
		"- If the user explicitly asks to generate/update plugin code, output JS code only (no markdown fences, no explanation text).",
		"- If the user asks analysis/design/review questions, answer normally and do not output plugin code unless requested.",
		"- Use CommonJS: module.exports = { name, match, fileLoadMode, theme, process(file) { return { data?, tree } }, viewContext?(file) { return string | object } }.",
		"- fileLoadMode must be \"incremental\" or \"full\".",
		"- theme is required with all keys: overlayBg, surfaceBg, surfaceBgElevated, text, textMuted, border, primary, primaryText, radius, shadow, focusRing, danger, warning, success.",
		"- Optional viewContext(file) returns concise current-view context for agent conversations when this plugin view is active.",
		"- viewContext should describe current view state, not duplicate large visible content; selected text is attached separately by the app.",
		"- Do not modify framework CSS/TS code.",
		"- Do not output global CSS overrides.",
		"- For dynamic interactions, use action \"navigate\" with params { path?, cursor?, query? }.",
	}, "\n"))

	return strings.Join([]string{
		"[SYSTEM_PROMPT]",
		systemPrompt,
		"",
		"[USER_PROMPT]",
		userMessage,
	}, "\n")
}

func buildPluginPromptInitial(catalogPrompt, userMessage, pluginDir string) string {
	systemPrompt := strings.TrimSpace(strings.Join([]string{
		"You are in view-plugin development mode.",
		"The user will describe requirements. Generate a view plugin and write it under " + filepath.ToSlash(pluginDir) + "/.",
		"",
		"## Plugin Spec",
		"- Use CommonJS: module.exports = { name, match, fileLoadMode, theme, process(file) { return { data?, tree } }, viewContext?(file) { return string | object } }",
		"- fileLoadMode: \"incremental\" | \"full\".",
		"- fileLoadMode controls how file content is loaded before process(file).",
		"- Use \"full\" for views that need global understanding of the file (chapter TOC, CSV table pagination/sort/filter, whole-document search).",
		"- Use \"incremental\" only for very large plain-text streaming/append-like views where byte-window loading is acceptable.",
		"- In \"full\" mode, plugin should treat input as whole-file content and should not rely on cursor.",
		"- If interaction is query-based pagination (page/pageSize), prefer \"full\" and update only query.",
		"- theme is required and must include all keys:",
		"  overlayBg, surfaceBg, surfaceBgElevated, text, textMuted, border,",
		"  primary, primaryText, radius, shadow, focusRing, danger, warning, success.",
		"- Do not modify framework CSS/TS code.",
		"- Do not output global CSS overrides.",
		"- Style customization must be done via theme tokens only.",
		"- file input: { name, path, content, ext, mime, size, truncated, next_cursor, query }",
		"- query comes from URL plugin params. Plugin reads file.query.<key> directly.",
		"- query is for business state only; do NOT store cursor in query.",
		"- Plugin must treat query as plain keys and must NOT depend on URL encoding details.",
		"- process must be a pure function (no external IO/state).",
		"- Optional viewContext(file) should also be pure. It may return a string or object with concise current-view context for agent conversations.",
		"- Use viewContext for state such as current page/chapter/filter/sort. Do not include large visible content; selected text is attached separately by the app.",
		"- event bindings must use top-level `on` field, not inside `props`.",
		"- filename should be lowercase kebab-case, e.g. txt-novel.js",
		"",
		"## Match Rule",
		"- ext: \".txt\" or \".csv,.tsv\"",
		"- path: \"novels/**/*.txt\"",
		"- mime: \"text/*\"",
		"- name: \"README*\"",
		"- any/all for OR/AND composition",
		"",
		"## Output Requirement",
		"- Use available file-write tool(s) to write plugin file to " + filepath.ToSlash(pluginDir) + "/<name>.js",
		"- tree must be valid UITree: root points to an existing element id",
		"- For dynamic interactions (pagination/sort/filter), use action: \"navigate\"",
		"- navigate params: { path?, cursor?, query? }",
		"- path: target file path (relative path under current root).",
		"- cursor: byte cursor used when re-reading the file.",
		"- query: plugin state map; after navigate, plugin reads it from file.query.",
		"- navigate usage examples:",
		"  - Change query only: { action: \"navigate\", params: { query: { page: 2 } } }",
		"  - Change cursor only: { action: \"navigate\", params: { cursor: 131072 } }",
		"  - Change both: { action: \"navigate\", params: { path: \"a.txt\", cursor: 0, query: { chapter: 1 } } }",
		"  - Incremental next chunk: read next cursor from file.next_cursor, then set navigate.params.cursor to that value.",
		"  - Example: { action: \"navigate\", params: { cursor: file.next_cursor } }",
		"- Plugin should always read current plugin state from file.query.",
		"- Return only JS plugin code. No markdown fences. No explanation text.",
		"",
		"## Responsive Breakpoints (required)",
		"- mobile: width < 768",
		"- tablet: 768 <= width < 1024",
		"- desktop: width >= 1024",
		"- Prefer single-column, tighter spacing, and larger touch targets on mobile",
		"- For wide tables/code blocks on mobile, provide horizontal scrolling or condensed fallback",
		"- Avoid fixed-width layouts that overflow small screens",
		"",
		"## Example Plugin (TXT Novel Reader)",
		"module.exports = {",
		"  name: \"TXT Novel Reader\",",
		"  match: { ext: \".txt\" },",
		"  fileLoadMode: \"full\",",
		"  theme: {",
		"    overlayBg: \"rgba(2,6,23,0.62)\",",
		"    surfaceBg: \"#f8fafc\",",
		"    surfaceBgElevated: \"#ffffff\",",
		"    text: \"#0f172a\",",
		"    textMuted: \"#475569\",",
		"    border: \"rgba(15,23,42,0.12)\",",
		"    primary: \"#2563eb\",",
		"    primaryText: \"#ffffff\",",
		"    radius: \"10px\",",
		"    shadow: \"0 16px 40px rgba(2,6,23,.22)\",",
		"    focusRing: \"rgba(37,99,235,.4)\",",
		"    danger: \"#dc2626\",",
		"    warning: \"#d97706\",",
		"    success: \"#16a34a\"",
		"  },",
		"  process(file) {",
		"    const content = typeof file.content === \"string\" ? file.content.replace(/\\r\\n?/g, \"\\n\") : \"\";",
		"    const query = file.query || {};",
		"    const lines = content.split(\"\\n\");",
		"    const chapterTitles = lines.filter((line) => /^\\s*第.+[章节回卷篇部]/.test(line.trim()));",
		"    const chapters = chapterTitles.length ? chapterTitles.map((title) => ({ title: title.trim(), text: content })) : [{ title: file.name ? String(file.name).replace(/\\.txt$/i, \"\") : \"正文\", text: content }];",
		"    const total = Math.max(1, chapters.length);",
		"    const chapterIdx = Math.min(Math.max(1, parseInt(query.chapter || \"1\", 10) || 1), total) - 1;",
		"    const current = chapters[chapterIdx] || { title: \"正文\", text: content };",
		"    const paragraphs = (current.text || \"\").split(\"\\n\").map(s => s.trim()).filter(Boolean).slice(0, 500);",
		"    const tocValue = String(query.toc || \"0\");",
		"    const showToc = tocValue !== \"0\";",
		"    const nextTocValue = String((parseInt(tocValue, 10) || 0) + 1);",
		"    return {",
		"      data: { ui: { tocOpen: showToc } },",
		"      tree: {",
		"        root: \"root\",",
		"        elements: {",
		"          root: { type: \"Stack\", props: { direction: \"vertical\", gap: \"sm\" }, children: [\"header\", \"nav-top\", \"content-card\", \"nav-bottom\", \"toc-dialog\"] },",
		"          header: { type: \"Stack\", props: { direction: \"horizontal\", gap: \"sm\", justify: \"between\", align: \"center\" }, children: [\"title\"] },",
		"          title: { type: \"Heading\", props: { text: current.title, level: \"h4\" }, children: [] },",
		"          \"nav-top\": { type: \"Stack\", props: { direction: \"horizontal\", gap: \"sm\", justify: \"between\" }, children: [\"prev-t\", \"toc-t\", \"next-t\"] },",
		"          \"nav-bottom\": { type: \"Stack\", props: { direction: \"horizontal\", gap: \"sm\", justify: \"between\" }, children: [\"prev-b\", \"toc-b\", \"next-b\"] },",
		"          \"prev-t\": { type: \"Button\", props: { label: \"上一章\", disabled: chapterIdx <= 0 }, on: { press: { action: \"navigate\", params: { query: { chapter: chapterIdx, toc: \"0\" } } } } },",
		"          \"toc-t\": { type: \"Button\", props: { label: \"目录\" }, on: { press: { action: \"navigate\", params: { query: { toc: nextTocValue } } } } },",
		"          \"next-t\": { type: \"Button\", props: { label: \"下一章\", disabled: chapterIdx >= total - 1 }, on: { press: { action: \"navigate\", params: { query: { chapter: chapterIdx + 2, toc: \"0\" } } } } },",
		"          \"prev-b\": { type: \"Button\", props: { label: \"上一章\", disabled: chapterIdx <= 0 }, on: { press: { action: \"navigate\", params: { query: { chapter: chapterIdx, toc: \"0\" } } } } },",
		"          \"toc-b\": { type: \"Button\", props: { label: \"目录\" }, on: { press: { action: \"navigate\", params: { query: { toc: nextTocValue } } } } },",
		"          \"next-b\": { type: \"Button\", props: { label: \"下一章\", disabled: chapterIdx >= total - 1 }, on: { press: { action: \"navigate\", params: { query: { chapter: chapterIdx + 2, toc: \"0\" } } } } },",
		"          \"content-card\": { type: \"Card\", props: { title: null, description: null, maxWidth: \"full\" }, children: [\"para-stack\"] },",
		"          \"para-stack\": { type: \"Stack\", props: { direction: \"vertical\", gap: \"sm\" }, children: paragraphs.map((_, i) => `p-${i}`) },",
		"          ...Object.fromEntries(paragraphs.map((line, i) => [`p-${i}`, { type: \"Text\", props: { text: line, variant: \"body\" }, children: [] }])),",
		"          \"toc-dialog\": { type: \"Dialog\", props: { title: \"章节目录\", openPath: \"/ui/tocOpen\" }, children: [\"toc-list\", \"toc-close\"] },",
		"          \"toc-list\": { type: \"Stack\", props: { direction: \"vertical\", gap: \"sm\" }, children: chapters.slice(0, 16).map((_, i) => `c-${i}`) },",
		"          ...Object.fromEntries(chapters.slice(0, 16).map((ch, i) => [`c-${i}`, { type: \"Button\", props: { label: `${i + 1}. ${ch.title}`, variant: i === chapterIdx ? \"primary\" : \"secondary\" }, on: { press: { action: \"navigate\", params: { query: { chapter: i + 1, toc: \"0\" } } } }, children: [] }])),",
		"          \"toc-close\": { type: \"Button\", props: { label: \"关闭\", variant: \"secondary\" }, on: { press: { action: \"navigate\", params: { query: { toc: \"0\" } } } }, children: [] }",
		"        }",
		"      }",
		"    };",
		"  },",
		"  viewContext(file) {",
		"    const query = file.query || {};",
		"    return `文件：${file.path || file.name}\\n当前章节：${query.chapter || \"1\"}`;",
		"  }",
		"};",
		"",
		"## Available Components Catalog",
		catalogPrompt,
	}, "\n"))

	return strings.Join([]string{
		"[SYSTEM_PROMPT]",
		systemPrompt,
		"",
		"[USER_PROMPT]",
		userMessage,
	}, "\n")
}
