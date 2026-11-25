// 思考文本的可见性判据（CUSTOM(G-LR)）。
//
// 为什么放在 shared 而不是 hooks：三条产出 `type:"thought"` 的路径分属 hooks
// （块流 / aux 回退 / 流式瞬时行），而思考框组件本身也要对「拿到不可读内容」做兜底；
// 组件反依赖 hooks 会把渲染层和纯函数层绞在一起，判据放这里谁都能用。

/**
 * 这段文本里有没有**看得见的字符**。
 *
 * agent 在工具卡之间会吐 `"\n\u200b"` 这种增量：Go 的 `strings.TrimSpace` 用的是
 * `unicode.IsSpace`，而 U+200B（零宽空格）不在其列，于是它被当成正文落成 text 块；
 * 前端这里只判空串，就渲染成一个只有零宽字符的 markdown 段落 —— 段落自带 `p` 的
 * 上下 margin，把相邻工具卡的间距从 6px 撑到 87.5px（实测，2026-10-25 用户报
 * 「每一个 tool call 之间的间距隔得非常大」）。
 */
const ZERO_WIDTH_BLANK = /^[\s\u200b\u200c\u200d\ufeff]*$/;

export function isBlankText(content: string): boolean {
  return ZERO_WIDTH_BLANK.test(content);
}

/**
 * 内容里有没有至少一个「有意义的字符」——字母、数字或 CJK。
 * reasoning 若全是标点/符号（如 "..."、"。。。"）不值得渲染成占位块。
 */
const MEANINGFUL_CHAR = /[\p{L}\p{N}]/u;

export function hasMeaningfulChar(content: string): boolean {
  return MEANINGFUL_CHAR.test(content);
}

/**
 * 思考内容是「占位」而非真实思考：空白/零宽，或只剩标点符号。
 *
 * 实测（2026-11-25，pc-wsl / CMAI）上游 redacted thinking 落盘就是
 * `{"thinking":"...","type":"thinking","signature":"..."}` —— MindFS 原样导入成
 * reasoning 块与 aux thought。渲染出来就是「思考过程 (3 字符)」，展开只有一个 `...`。
 * 三条路径必须用同一把尺，否则块流路径过滤掉了，流式/aux 回退照样把它画出来。
 */
export function isPlaceholderThought(content: string): boolean {
  return isBlankText(content) || !hasMeaningfulChar(content);
}
