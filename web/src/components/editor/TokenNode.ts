import {
  EditorConfig,
  NodeKey,
  SerializedTextNode,
  Spread,
  TextNode,
} from "lexical";

export type TokenType = "file" | "skill";

export type SerializedTokenNode = Spread<
  {
    type: "token";
    tokenType: TokenType;
    tokenValue: string;
    label: string;
    version: 1;
  },
  SerializedTextNode
>;

export class TokenNode extends TextNode {
  __tokenType: TokenType;
  __tokenValue: string;
  __label: string;

  static getType(): string {
    return "token";
  }

  static clone(node: TokenNode): TokenNode {
    return new TokenNode(node.__tokenType, node.__tokenValue, node.__label, node.__key);
  }

  static importJSON(serializedNode: SerializedTokenNode): TokenNode {
    return $createTokenNode(
      serializedNode.tokenType,
      serializedNode.tokenValue,
      serializedNode.label
    );
  }

  constructor(tokenType: TokenType, tokenValue: string, label: string, key?: NodeKey) {
    super(label, key);
    this.__tokenType = tokenType;
    this.__tokenValue = tokenValue;
    this.__label = label;
  }

  createDOM(config: EditorConfig): HTMLElement {
    const dom = super.createDOM(config);
    dom.dataset.mindfsTokenNode = "true";
    dom.contentEditable = "false";
    dom.style.display = "inline-flex";
    dom.style.alignItems = "center";
    dom.style.padding = "1px 6px";
    dom.style.margin = "0 1px";
    dom.style.borderRadius = "8px";
    dom.style.whiteSpace = "pre";
    if (this.__tokenType === "file") {
      dom.style.background = "var(--token-file-bg)";
      dom.style.color = "var(--token-file-text)";
    } else {
      dom.style.background = "var(--token-skill-bg)";
      dom.style.color = "var(--token-skill-text)";
    }
    return dom;
  }

  updateDOM(prevNode: TokenNode, dom: HTMLElement, config: EditorConfig): boolean {
    const updated = super.updateDOM(prevNode as unknown as this, dom, config);
    if (prevNode.__tokenType !== this.__tokenType) {
      if (this.__tokenType === "file") {
        dom.style.background = "var(--token-file-bg)";
        dom.style.color = "var(--token-file-text)";
      } else {
        dom.style.background = "var(--token-skill-bg)";
        dom.style.color = "var(--token-skill-text)";
      }
    }
    return updated;
  }

  exportJSON(): SerializedTokenNode {
    return {
      ...super.exportJSON(),
      type: "token",
      tokenType: this.__tokenType,
      tokenValue: this.__tokenValue,
      label: this.__label,
      version: 1,
    };
  }

  getTokenType(): TokenType {
    return this.__tokenType;
  }

  getTokenValue(): string {
    return this.__tokenValue;
  }

  getLabel(): string {
    return this.__label;
  }

  isTextEntity(): true {
    return true;
  }

  canInsertTextBefore(): boolean {
    return false;
  }

  canInsertTextAfter(): boolean {
    return false;
  }
}

export function $createTokenNode(type: TokenType, value: string, label: string): TokenNode {
  return new TokenNode(type, value, label);
}

export function $isTokenNode(node: unknown): node is TokenNode {
  return node instanceof TokenNode;
}

export function createLabel(type: TokenType, value: string): string {
  if (type === "file") {
    const parts = value.replace(/\\/g, "/").split("/");
    return parts[parts.length - 1] || value;
  }
  return value;
}
