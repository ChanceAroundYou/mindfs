import {
  $createTextNode,
  $getRoot,
  $getSelection,
  $isLineBreakNode,
  $isRangeSelection,
  $isTextNode,
} from "lexical";
import {
  $createTokenNode,
  $isTokenNode,
  createLabel,
  type TokenNode,
  type TokenType,
} from "./TokenNode";

export type ActiveTokenType = "file" | "slash" | "prompt" | "command";

export type ActiveToken = {
  type: ActiveTokenType;
  query: string;
};

export function $insertPlainTextAtSelection(text: string): boolean {
  if (text === "") {
    return false;
  }
  let selection = $getSelection();
  if (!$isRangeSelection(selection)) {
    $getRoot().selectEnd();
    selection = $getSelection();
  }
  if (!$isRangeSelection(selection)) {
    return false;
  }
  const parts = text.replace(/\r\n/g, "\n").replace(/\r/g, "\n").split("\n");
  for (let index = 0; index < parts.length; index += 1) {
    const part = parts[index];
    if (part) {
      selection.insertText(part);
    }
    if (index < parts.length - 1) {
      selection.insertLineBreak();
    }
    selection = $getSelection();
    if (!$isRangeSelection(selection)) {
      return false;
    }
  }
  return true;
}

export function serializeEditor(): string {
  const parts: string[] = [];
  const visit = (node: any) => {
    if ($isTokenNode(node)) {
      parts.push(
        node.getTokenType() === "file"
          ? `[file: ${node.getTokenValue()}]`
          : `[use skill: ${node.getTokenValue()}]`
      );
      return;
    }
    if ($isLineBreakNode(node)) {
      parts.push("\n");
      return;
    }
    if ($isTextNode(node)) {
      parts.push(node.getTextContent());
      return;
    }
    if (typeof node.getChildren === "function") {
      for (const child of node.getChildren()) {
        visit(child);
      }
    }
  };
  visit($getRoot());
  return parts.join("");
}

export function $insertSerializedTextAtSelection(text: string): boolean {
  if (text === "") {
    return false;
  }
  const pattern = /\[(read file|file|use skill):\s*([^\]]+)\]/g;
  let lastIndex = 0;
  let inserted = false;
  let match: RegExpExecArray | null;

  const insertToken = (type: TokenType, value: string): boolean => {
    let selection = $getSelection();
    if (!$isRangeSelection(selection)) {
      $getRoot().selectEnd();
      selection = $getSelection();
    }
    if (!$isRangeSelection(selection)) {
      return false;
    }
    selection.insertNodes([$createTokenNode(type, value, createLabel(type, value))]);
    return true;
  };

  while ((match = pattern.exec(text)) !== null) {
    const prefix = text.slice(lastIndex, match.index);
    if (prefix) {
      inserted = $insertPlainTextAtSelection(prefix) || inserted;
    }
    const tokenType: TokenType = match[1] === "use skill" ? "skill" : "file";
    const tokenValue = match[2].trim();
    if (tokenValue) {
      inserted = insertToken(tokenType, tokenValue) || inserted;
    }
    lastIndex = pattern.lastIndex;
  }

  const suffix = text.slice(lastIndex);
  if (suffix) {
    inserted = $insertPlainTextAtSelection(suffix) || inserted;
  }
  return inserted;
}

export function serializedTextEndsWithToken(text: string): boolean {
  return /\[(?:read file|file|use skill):\s*[^\]]+\]\s*$/.test(text);
}

export function $selectAfterTokenNode(node: TokenNode): void {
  const next = node.getNextSibling();
  if ($isTextNode(next) && !$isTokenNode(next)) {
    next.select(next.getTextContentSize(), next.getTextContentSize());
    return;
  }
  const anchor = $createTextNode(" ");
  node.insertAfter(anchor);
  anchor.select(1, 1);
}

export function $selectEditorEndWithTokenAnchor(): void {
  const root = $getRoot();
  const lastChild = root.getLastChild();
  if ($isTokenNode(lastChild)) {
    $selectAfterTokenNode(lastChild);
    return;
  }
  root.selectEnd();
}

export function getDisplayText(): string {
  return $getRoot().getTextContent();
}

export function getActiveTokenFromSelection(): ActiveToken | null {
  const selection = $getSelection();
  if (!$isRangeSelection(selection) || !selection.isCollapsed()) {
    return null;
  }
  const anchorNode = selection.anchor.getNode();
  if (!$isTextNode(anchorNode) || $isTokenNode(anchorNode)) {
    return null;
  }
  const text = anchorNode.getTextContent();
  const offset = selection.anchor.offset;
  return parseActiveToken(text, offset);
}

export function parseActiveToken(displayText: string, cursorPos: number): ActiveToken | null {
  const cursor = Math.max(0, Math.min(cursorPos, displayText.length));
  let start = cursor - 1;
  while (start >= 0) {
    const ch = displayText[start];
    if (ch === "@" || ch === "/" || ch === "#") {
      const prev = start > 0 ? displayText[start - 1] : "";
      const isBoundary =
        prev === "" ||
        /\s/.test(prev) ||
        prev === "(" ||
        prev === "[" ||
        prev === "{" ||
        prev === '"' ||
        prev === "'";
      if (!isBoundary) {
        return null;
      }
      let end = cursor;
      for (; end < displayText.length; end++) {
        const next = displayText[end];
        if (/\s/.test(next) || next === "[" || next === "]" || next === "\n") {
          break;
        }
      }
      return {
        type: ch === "@" ? "file" : ch === "/" ? "slash" : "prompt",
        query: displayText.slice(start + 1, end),
      };
    }
    if (/\s/.test(ch) || ch === "[" || ch === "]") {
      return null;
    }
    start--;
  }
  return null;
}

export function expectedActiveTokenType(candidateType: string): ActiveTokenType {
  if (candidateType === "command") {
    return "command";
  }
  if (candidateType === "file") {
    return "file";
  }
  if (candidateType === "prompt") {
    return "prompt";
  }
  return "slash";
}

export function triggerChar(tokenType: ActiveTokenType): "@" | "/" | "#" {
  if (tokenType === "file") {
    return "@";
  }
  if (tokenType === "prompt") {
    return "#";
  }
  return "/";
}

export function $replaceWithPlainText(text: string): void {
  const root = $getRoot();
  root.clear();
  root.selectEnd();
  if (text !== "") {
    $insertPlainTextAtSelection(text);
  }
  $getRoot().selectEnd();
}

export function $replaceWithSerializedText(text: string): void {
  const root = $getRoot();
  root.clear();
  root.selectEnd();
  if (text !== "") {
    $insertSerializedTextAtSelection(text);
    if (serializedTextEndsWithToken(text)) {
      $insertPlainTextAtSelection(" ");
    }
  }
  $getRoot().selectEnd();
}

