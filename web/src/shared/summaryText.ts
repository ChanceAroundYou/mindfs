/**
 * 卡片摘要的纯文本化（S8）。
 *
 * 任务结果摘要（`result_summary`）由 Router 写成受限 Markdown：四个标签行、`- ` 列表、
 * 行内代码。看板卡按 `white-space: pre-wrap` 渲染、没有 Markdown 解析器，于是 `**结论**`
 * 与反引号原样显示，卡片上读起来是一串符号。这里把摘要压成纯文本，口径与
 * `server/internal/notify/plaintext.go` 的 `stripMarkdown` 一致：
 *
 *   去围栏行/分隔线行、去表格分隔行（表格行把竖线换成空格）、去标题/引用/列表标记、
 *   去行内代码的反引号、图片与链接只留文字、去粗体/删除线/斜体标记。
 *
 * 唯一区别在换行：通知栏把换行也折叠成空格，卡片要保留四个标签行各自成行，所以
 * 只折叠行内连续空白、丢掉空行。
 *
 * 存量数据（例如 2026-11-21 那条 2,833 字带表格的卡片）也走同一套规则，不用改库。
 */

const FENCE_LINE = /^\s*(?:```|~~~)/;
const HEADING = /^\s{0,3}#{1,6}\s*/;
const BLOCKQUOTE = /^\s{0,3}(?:>\s?)+/;
const LIST_MARKER = /^\s{0,3}(?:[-*+]|\d{1,3}[.)])\s+/;
const RULE_LINE = /^\s{0,3}(?:[-*_])(?:\s*[-*_]){2,}\s*$/;
const TABLE_SEPARATOR = /^\s*\|?[\s:|-]*-[\s:|-]*\|?\s*$/;
const TABLE_PIPE = /\s*\|\s*/g;
const IMAGE = /!\[([^\]]*)\]\([^)]*\)/g;
const LINK = /\[([^\]]*)\]\([^)]*\)/g;
const BOLD_STAR = /\*\*([^*]+?)\*\*/g;
const BOLD_UNDERSCORE = /__([^_]+?)__/g;
const STRIKE = /~~([^~]+?)~~/g;
const ITALIC_STAR = /\*([^*\s][^*]*?)\*/g;
const INLINE_SPACE = /[ \t\f\v\u00a0]+/g;

export function summaryPlainText(value: string): string {
  const text = String(value ?? "").replace(/\r\n?/g, "\n");
  const kept: string[] = [];
  for (const raw of text.split("\n")) {
    if (FENCE_LINE.test(raw) || RULE_LINE.test(raw)) {
      continue;
    }
    if (raw.includes("|") && TABLE_SEPARATOR.test(raw)) {
      continue;
    }
    let line = raw.replace(HEADING, "").replace(BLOCKQUOTE, "").replace(LIST_MARKER, "");
    // 只把「首尾带竖线」的行当表格行；代码里的 `a | b`（shell 管道）保持原样。
    const trimmed = line.trim();
    if (trimmed.startsWith("|") || trimmed.endsWith("|")) {
      line = line.replace(TABLE_PIPE, " ");
    }
    kept.push(line);
  }
  let joined = kept.join("\n").replace(/`/g, "");
  joined = joined
    .replace(IMAGE, "$1")
    .replace(LINK, "$1")
    .replace(BOLD_STAR, "$1")
    .replace(BOLD_UNDERSCORE, "$1")
    .replace(STRIKE, "$1")
    .replace(ITALIC_STAR, "$1");
  return joined
    .split("\n")
    .map((line) => line.replace(INLINE_SPACE, " ").trim())
    .filter((line) => line !== "")
    .join("\n")
    .trim();
}