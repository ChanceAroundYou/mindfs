import React, { forwardRef, useEffect, useImperativeHandle, useMemo, useRef, useState } from "react";
import { LexicalComposer } from "@lexical/react/LexicalComposer";
import { EDITOR_FONT_SIZE, EDITOR_LINE_HEIGHT } from "../action/composerStyles";
import { PlainTextPlugin } from "@lexical/react/LexicalPlainTextPlugin";
import { ContentEditable } from "@lexical/react/LexicalContentEditable";
import { HistoryPlugin } from "@lexical/react/LexicalHistoryPlugin";
import {
  $createTextNode,
  $getRoot,
  $getSelection,
  $isRangeSelection,
  $isTextNode,
  $setCompositionKey,
  type LexicalEditor,
  type TextNode,
} from "lexical";
import { TokenNode, $isTokenNode, $createTokenNode, createLabel, type TokenType } from "./TokenNode";
import {
  $replaceWithPlainText,
  $replaceWithSerializedText,
  expectedActiveTokenType,
  parseActiveToken,
  triggerChar,
  type ActiveToken,
} from "./tokenEditorUtils";
import { EditorBridge } from "./EditorBridge";
import type { BridgeOnChangePayload } from "./EditorBridge";

export type CandidateType = TokenType | "slash_command" | "prompt" | "command";

export type TokenEditorHandle = {
  focus: () => void;
  blur: () => void;
  getHeight: () => number;
  clear: () => void;
  setText: (value: string) => void;
  insertCandidate: (type: CandidateType, value: string) => void;
};

type TokenEditorProps = {
  placeholder: string;
  disabled?: boolean;
  /** 只读：任务阶段卡的「已执行/未执行」态用（PromptEditor 的 readonly / done）。 */
  readOnly?: boolean;
  isDark?: boolean;
  rightInset?: number;
  topInset?: number;
  bottomInset?: number;
  fillHeight?: boolean;
  onChange: (payload: BridgeOnChangePayload) => void;
  onFocusChange?: (focused: boolean) => void;
  onPointerDown?: () => void;
  onKeyDown?: (event: React.KeyboardEvent<HTMLDivElement>) => void;
  onPaste?: (event: React.ClipboardEvent<HTMLDivElement>) => void;
  onEnter?: (event: KeyboardEvent | null) => boolean;
  enterKeyHint?: React.HTMLAttributes<HTMLElement>["enterKeyHint"];
  onCompositionStart?: () => void;
  onCompositionEnd?: () => void;
};

function PlaceholderLayer({
  isEmpty,
  isFocused,
  placeholder,
  isSingleLine,
  rightInset,
  topInset,
  readOnly,
}: {
  isEmpty: boolean;
  isFocused: boolean;
  placeholder: string;
  isSingleLine: boolean;
  rightInset: number;
  topInset: number;
  readOnly?: boolean;
}) {
  if (!isEmpty || isFocused || readOnly) {
    return null;
  }
  return (
    <div
      style={{
        position: "absolute",
        left: "14px",
        right: `${rightInset}px`,
        top: topInset > 0 ? `${topInset + 12}px` : "50%",
        transform: topInset > 0 ? "none" : "translateY(-50%)",
        color: "var(--text-secondary)",
        fontSize: `${EDITOR_FONT_SIZE}px`,
        pointerEvents: "none",
        zIndex: 1,
        whiteSpace: "nowrap",
        overflow: "hidden",
        textOverflow: "ellipsis",
      }}
    >
      {placeholder}
    </div>
  );
}

const TokenEditor = forwardRef<TokenEditorHandle, TokenEditorProps>(function TokenEditor(
  {
    placeholder,
    disabled = false,
    readOnly = false,
    isDark = false,
    rightInset = 120,
    topInset = 0,
    bottomInset = 12,
    fillHeight = false,
    onChange,
    onFocusChange,
    onPointerDown,
    onKeyDown,
    onPaste,
    onEnter,
    enterKeyHint,
    onCompositionStart,
    onCompositionEnd,
  },
  ref
) {
  const editorRef = useRef<LexicalEditor | null>(null);
  const rootRef = useRef<HTMLDivElement | null>(null);
  const [isEmpty, setIsEmpty] = useState(true);
  const [isFocused, setIsFocused] = useState(false);
  const isSingleLine = bottomInset <= 12;

  const initialConfig = useMemo(
    () => ({
      namespace: "mindfs-token-editor",
      theme: {},
      onError(error: Error) {
        throw error;
      },
      nodes: [TokenNode],
    }),
    []
  );

  useImperativeHandle(ref, () => ({
    focus() {
      rootRef.current?.focus({ preventScroll: true });
    },
    blur() {
      rootRef.current?.blur();
    },
    getHeight() {
      return rootRef.current?.scrollHeight || 44;
    },
    clear() {
      editorRef.current?.update(() => {
        $setCompositionKey(null);
        $replaceWithPlainText("");
      });
    },
    setText(value: string) {
      // 只在焦点已经在这个编辑器里时才抢焦点：常驻挂载的编辑器（如工作台快速发起）
      // 灌值不能把整页焦点夺走；已聚焦的调用方（改写草稿、插入候选）行为不变。
      const keepFocus = !!rootRef.current && rootRef.current.contains(document.activeElement);
      editorRef.current?.update(() => {
        $replaceWithSerializedText(value);
      });
      if (keepFocus) {
        rootRef.current?.focus({ preventScroll: true });
      }
    },
    insertCandidate(type: CandidateType, value: string) {
      const editor = editorRef.current;
      if (!editor) return;
      if (type === "command") {
        editor.update(() => {
          $replaceWithPlainText(value);
        });
        rootRef.current?.focus({ preventScroll: true });
        return;
      }
      editor.update(() => {
        const selection = $getSelection();
        if (!$isRangeSelection(selection) || !selection.isCollapsed()) {
          return;
        }
        const anchorNode = selection.anchor.getNode();
        if (!$isTextNode(anchorNode) || $isTokenNode(anchorNode)) {
          return;
        }
        const text = anchorNode.getTextContent();
        const offset = selection.anchor.offset;
        const token = parseActiveToken(text, offset);
        const expectedType = expectedActiveTokenType(type);
        if (!token || token.type !== expectedType) {
          return;
        }
        let start = offset - 1;
        while (start >= 0 && text[start] !== triggerChar(token.type)) {
          start--;
        }
        if (start < 0) {
          return;
        }
        let end = offset;
        while (end < text.length) {
          const ch = text[end];
          if (/\s/.test(ch) || ch === "[" || ch === "]" || ch === "\n") {
            break;
          }
          end++;
        }
        const prefix = text.slice(0, start);
        const suffix = text.slice(end);
        const replacementNodes = [];
        if (prefix) replacementNodes.push($createTextNode(prefix));
        if (type === "slash_command") {
          replacementNodes.push($createTextNode(`/${value}`));
        } else if (type === "prompt") {
          replacementNodes.push($createTextNode(value));
        } else {
          replacementNodes.push($createTokenNode(type, value, createLabel(type, value)));
        }
        const tailNode = $createTextNode(" ");
        replacementNodes.push(tailNode);
        if (suffix) replacementNodes.push($createTextNode(suffix));
        let current = replacementNodes[0];
        anchorNode.replace(current);
        for (let i = 1; i < replacementNodes.length; i++) {
          current.insertAfter(replacementNodes[i]);
          current = replacementNodes[i];
        }
        tailNode.select(1, 1);
      });
      rootRef.current?.focus({ preventScroll: true });
    },
  }));

  useEffect(() => {
    const root = rootRef.current;
    if (!root) {
      return;
    }
    if (enterKeyHint) {
      root.setAttribute("enterkeyhint", enterKeyHint);
      return;
    }
    root.removeAttribute("enterkeyhint");
  }, [enterKeyHint]);

  const handleChange = (payload: BridgeOnChangePayload) => {
    setIsEmpty(payload.displayText.length === 0);
    onChange(payload);
  };

  const handleDeleteToken = (forward: boolean) => {
    const editor = editorRef.current;
    if (!editor) {
      return false;
    }
    let handled = false;
    editor.update(() => {
      const moveSelectionToTextEdge = (node: TextNode | null, atStart: boolean) => {
        if (!node) {
          $getRoot().selectEnd();
          return;
        }
        if (atStart) {
          node.select(0, 0);
          return;
        }
        const size = node.getTextContentSize();
        node.select(size, size);
      };

      const selection = $getSelection();
      if (!$isRangeSelection(selection)) {
        return;
      }
      if (!selection.isCollapsed()) {
        selection.removeText();
        handled = true;
        return;
      }
      const anchorNode = selection.anchor.getNode();
      const anchorOffset = selection.anchor.offset;
      if ($isTokenNode(anchorNode)) {
        const target = forward ? anchorNode.getNextSibling() : anchorNode.getPreviousSibling();
        anchorNode.remove();
        moveSelectionToTextEdge($isTextNode(target) ? target : null, forward);
        handled = true;
        return;
      }
      const textNode = $isTextNode(anchorNode) ? anchorNode : null;
      if (!textNode || $isTokenNode(textNode)) {
        return;
      }
      const sibling = forward
        ? anchorOffset === textNode.getTextContentSize()
          ? textNode.getNextSibling()
          : null
        : anchorOffset === 0
        ? textNode.getPreviousSibling()
        : null;
      if (!$isTokenNode(sibling)) {
        return;
      }
      const target = forward ? sibling.getNextSibling() : sibling.getPreviousSibling();
      sibling.remove();
      if ($isTextNode(target)) {
        moveSelectionToTextEdge(target, forward);
      } else {
        textNode.select(anchorOffset, anchorOffset);
      }
      handled = true;
    });
    return handled;
  };

  return (
    <div
      onMouseDown={onPointerDown}
      onTouchStart={onPointerDown}
      style={{
        position: "relative",
        width: "100%",
        height: fillHeight ? "100%" : undefined,
        minHeight: "44px",
        ["--token-file-bg" as any]: isDark ? "rgba(59,130,246,0.16)" : "rgba(59,130,246,0.10)",
        ["--token-file-text" as any]: isDark ? "#93c5fd" : "#1d4ed8",
        ["--token-skill-bg" as any]: isDark ? "rgba(139,92,246,0.18)" : "rgba(139,92,246,0.10)",
        ["--token-skill-text" as any]: isDark ? "#c4b5fd" : "#7c3aed",
      }}
    >
      <LexicalComposer initialConfig={initialConfig}>
        <PlainTextPlugin
          contentEditable={
            <ContentEditable
              className="token-editor-input"
              aria-placeholder={placeholder}
              aria-readonly={readOnly || undefined}
              placeholder={<span></span>}
              spellCheck={false}
              onFocus={() => {
                setIsFocused(true);
                onFocusChange?.(true);
              }}
              onBlur={() => {
                setIsFocused(false);
                onFocusChange?.(false);
              }}
              onKeyDown={onKeyDown}
              onPaste={onPaste}
              enterKeyHint={enterKeyHint}
              onCompositionStart={onCompositionStart}
              onCompositionEnd={onCompositionEnd}
              style={{
                width: "100%",
                minHeight: fillHeight ? "100%" : isSingleLine ? "44px" : "20px",
                height: fillHeight ? "100%" : isSingleLine ? "44px" : "auto",
                maxHeight: fillHeight ? "none" : "240px",
                overflowY: "auto",
                padding: isSingleLine
                  ? `${12 + topInset}px ${rightInset}px 12px 14px`
                  : `${8 + topInset}px ${rightInset}px ${bottomInset}px 14px`,
                outline: "none",
                fontSize: `${EDITOR_FONT_SIZE}px`,
                lineHeight: `${EDITOR_LINE_HEIGHT}px`,
                boxSizing: "border-box",
                whiteSpace: "pre-wrap",
                wordBreak: "break-word",
                color: "var(--text-primary)",
                position: "relative",
                zIndex: 2,
                pointerEvents: disabled ? "none" : "auto",
                cursor: readOnly ? "text" : undefined,
              }}
            />
          }
          placeholder={
            <PlaceholderLayer
              isEmpty={isEmpty}
              isFocused={isFocused}
              placeholder={placeholder}
              isSingleLine={isSingleLine}
              rightInset={rightInset}
              topInset={topInset}
              readOnly={readOnly}
            />
          }
          ErrorBoundary={({ children, onError: _onError }) => children}
        />
        <HistoryPlugin />
        <EditorBridge
          onChange={handleChange}
          readOnly={readOnly}
          onReady={({ editor, root }) => {
            editorRef.current = editor;
            rootRef.current = root;
            if (root && enterKeyHint) {
              root.setAttribute("enterkeyhint", enterKeyHint);
            }
          }}
          onEnter={onEnter}
          onDeleteToken={handleDeleteToken}
        />
      </LexicalComposer>
    </div>
  );
});

export default TokenEditor;
