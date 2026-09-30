import { useCallback, useState } from "react";
import { type TokenEditorHandle } from "../editor/TokenEditor";

/** 单行高度：TokenEditor 单行时 contentEditable 高 44px。 */
const SINGLE_LINE_HEIGHT = 44;
/** 超过这个高度就认为折行了。50 = 44 + 一点余量，与 TaskEditor 的 padding/行高对齐。 */
const MULTI_LINE_THRESHOLD = 50;
/** 折行后文字铺满整宽，右侧只留极窄的余量。 */
const MULTI_LINE_RIGHT_INSET = 14;
/** 折行后控件挪到底部，底部要让出控件高度。 */
const MULTI_LINE_BOTTOM_INSET = 44;
/** 单行时底部就是常规行距。 */
const SINGLE_LINE_BOTTOM_INSET = 12;

/**
 * 折行判定：决定 TokenEditor 的 rightInset / bottomInset 怎么切。
 *
 * 单行时控件排在右下角，文字得在右边给它让位（rightInset 大）；一旦折行，文字改占满整宽，
 * 控件挪到底部，只在底部让出高度（rightInset 收窄、bottomInset 变大）。
 *
 * 这个 50 的阈值与两处 inset 数值以前在 ActionBar 与 PromptEditor 里各抄了一份：
 * 一边改了另一边不改，就会重新出现「文字盖在控件上」或者「折行了还留着一大块右侧空白」。
 * 两边共用本 hook，阈值和 inset 只剩这一处。
 */
export function useComposerEditorHeight(editorRef: React.MutableRefObject<TokenEditorHandle | null>) {
  const [isMultiLine, setIsMultiLine] = useState(false);

  const syncEditorHeight = useCallback(() => {
    const height = editorRef.current?.getHeight() || SINGLE_LINE_HEIGHT;
    setIsMultiLine(height > MULTI_LINE_THRESHOLD);
  }, [editorRef]);

  /**
   * 清空时复位：否则从长文本切回空串，底部那片留白会一直留着
   * （高度回落了但 isMultiLine 仍是 true，inset 还按多行算）。
   */
  const resetEditorHeight = useCallback(() => {
    setIsMultiLine(false);
  }, []);

  return { isMultiLine, syncEditorHeight, resetEditorHeight };
}

/**
 * TokenEditor 的 right/bottom inset。
 *
 * 单行：控件在右下角，文字右侧要让位（largeInset），底部只留常规行距。
 * 折行：文字铺满整宽，控件挪到底部，于是右侧几乎不 inset（14），改在底部让出控件高度（44）。
 *
 * 以前这两组数字在 ActionBar 与 PromptEditor 各写一份。改了一处忘了另一处，就会出现
 * 「文字压在控件上」或「已经折行、右侧还空一大块」。现在只有一个来源。
 *
 * largeInset 由调用方给：对话输入框要避开 ModeSelector + AgentSelector + 附件 + 发送，
 * 面板里只有 AgentSelector + 附件 + 发送，右侧留位可以小一些。
 */
export function composerEditorInsets(isMultiLine: boolean, singleLineRightInset: number) {
  return {
    rightInset: isMultiLine ? MULTI_LINE_RIGHT_INSET : singleLineRightInset,
    topInset: 0,
    bottomInset: isMultiLine ? MULTI_LINE_BOTTOM_INSET : SINGLE_LINE_BOTTOM_INSET,
  };
}
