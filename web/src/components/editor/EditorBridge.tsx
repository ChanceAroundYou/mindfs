import React, { useEffect, useRef, useState } from "react";
import { useLexicalComposerContext } from "@lexical/react/LexicalComposerContext";
import {
  $getNearestNodeFromDOMNode,
  COMMAND_PRIORITY_HIGH,
  KEY_BACKSPACE_COMMAND,
  KEY_DELETE_COMMAND,
  KEY_ENTER_COMMAND,
  LexicalEditor,
  PASTE_COMMAND,
} from "lexical";
import { $isTokenNode } from "./TokenNode";
import {
  $insertPlainTextAtSelection,
  $selectAfterTokenNode,
  $selectEditorEndWithTokenAnchor,
  getActiveTokenFromSelection,
  getDisplayText,
  serializeEditor,
  type ActiveToken,
} from "./tokenEditorUtils";
import {
  getPlainTextFromPasteEvent,
  isKeyboardPasteInput,
  pasteEventHasFiles,
  readClipboardTextFallback,
} from "./pasteSupport";

export type BridgeOnChangePayload = {
  serializedText: string;
  displayText: string;
  activeToken: ActiveToken | null;
};

export function EditorBridge({
  onChange,
  onReady,
  onEnter,
  onDeleteToken,
  readOnly,
}: {
  onChange: (payload: BridgeOnChangePayload) => void;
  onReady: (api: { editor: LexicalEditor; root: HTMLDivElement | null }) => void;
  onEnter?: (event: KeyboardEvent | null) => boolean;
  onDeleteToken: (forward: boolean) => boolean;
  readOnly?: boolean;
}) {
  const [editor] = useLexicalComposerContext();
  const rootRef = useRef<HTMLDivElement | null>(null);
  const [rootElement, setRootElement] = useState<HTMLDivElement | null>(null);

  useEffect(() => {
    editor.setEditable(!readOnly);
  }, [editor, readOnly]);

  useEffect(() => {
    return editor.registerRootListener((rootElement) => {
      rootRef.current = rootElement as HTMLDivElement | null;
      setRootElement(rootRef.current);
      onReady({ editor, root: rootRef.current });
    });
  }, [editor, onReady]);

  useEffect(() => {
    if (!rootElement) {
      return;
    }
    const handleTokenPointerDown = (event: PointerEvent) => {
      const target = event.target;
      if (!(target instanceof HTMLElement)) {
        return;
      }
      const tokenElement = target.closest<HTMLElement>("[data-mindfs-token-node='true']");
      if (tokenElement && rootElement.contains(tokenElement)) {
        event.preventDefault();
        event.stopPropagation();
        rootElement.focus({ preventScroll: true });
        editor.update(() => {
          const node = $getNearestNodeFromDOMNode(tokenElement);
          if ($isTokenNode(node)) {
            $selectAfterTokenNode(node);
          }
        });
        return;
      }
      if (target !== rootElement) {
        return;
      }
      event.preventDefault();
      rootElement.focus({ preventScroll: true });
      editor.update(() => {
        $selectEditorEndWithTokenAnchor();
      });
    };
    rootElement.addEventListener("pointerdown", handleTokenPointerDown, { capture: true });
    return () => {
      rootElement.removeEventListener("pointerdown", handleTokenPointerDown, { capture: true });
    };
  }, [editor, rootElement]);

  useEffect(() => {
    if (!rootElement) {
      return;
    }
    const insertFromNativePaste = (event: ClipboardEvent | InputEvent) => {
      if (pasteEventHasFiles(event)) {
        return;
      }
      const text = getPlainTextFromPasteEvent(event);
      const inputText = typeof InputEvent !== "undefined" && event instanceof InputEvent ? event.data || "" : "";
      event.preventDefault();
      event.stopPropagation();
      event.stopImmediatePropagation();
      const insert = (nextText: string) => {
        if (nextText === "") {
          return;
        }
        editor.update(() => {
          $insertPlainTextAtSelection(nextText);
        });
        rootElement.focus({ preventScroll: true });
      };
      if (text !== "") {
        insert(text);
        return;
      }
      void readClipboardTextFallback().then((clipboardText) => {
        insert(clipboardText || inputText);
      });
    };
    const handlePaste = (event: ClipboardEvent) => insertFromNativePaste(event);
    const handleBeforeInput = (event: InputEvent) => {
      if (isKeyboardPasteInput(event)) {
        insertFromNativePaste(event);
      }
    };
    rootElement.addEventListener("paste", handlePaste, { capture: true });
    rootElement.addEventListener("beforeinput", handleBeforeInput, { capture: true });
    return () => {
      rootElement.removeEventListener("paste", handlePaste, { capture: true });
      rootElement.removeEventListener("beforeinput", handleBeforeInput, { capture: true });
    };
  }, [editor, rootElement]);

  useEffect(() => {
    return editor.registerUpdateListener(({ editorState }) => {
      editorState.read(() => {
        onChange({
          serializedText: serializeEditor(),
          displayText: getDisplayText(),
          activeToken: getActiveTokenFromSelection(),
        });
      });
    });
  }, [editor, onChange]);

  useEffect(() => {
    return editor.registerCommand(
      PASTE_COMMAND,
      (event) => {
        if (pasteEventHasFiles(event)) {
          return false;
        }
        const text = getPlainTextFromPasteEvent(event);
        if (text === "") {
          return false;
        }
        event.preventDefault();
        if ($insertPlainTextAtSelection(text)) {
          return true;
        }
        return false;
      },
      COMMAND_PRIORITY_HIGH
    );
  }, [editor]);

  useEffect(() => {
    return editor.registerCommand(
      KEY_ENTER_COMMAND,
      (event) => onEnter?.(event) ?? false,
      COMMAND_PRIORITY_HIGH
    );
  }, [editor, onEnter]);

  useEffect(() => {
    return editor.registerCommand(
      KEY_BACKSPACE_COMMAND,
      () => onDeleteToken(false),
      COMMAND_PRIORITY_HIGH
    );
  }, [editor, onDeleteToken]);

  useEffect(() => {
    return editor.registerCommand(
      KEY_DELETE_COMMAND,
      () => onDeleteToken(true),
      COMMAND_PRIORITY_HIGH
    );
  }, [editor, onDeleteToken]);

  return null;
}
