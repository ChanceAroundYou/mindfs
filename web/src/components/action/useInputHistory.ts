import { useCallback, useEffect, useRef, useState } from "react";
import { type TokenEditorHandle } from "../editor/TokenEditor";

/** 输入历史（↑/↓）：草稿保底 + 替换编辑器文本时的 re-entrancy 守卫。 */
export function useInputHistory({
  editorRef,
  serializedInput,
  inputHistory,
  sessionHistoryKey,
  syncEditorHeight,
  clearActiveToken,
  clearCandidates,
  setSerializedInput,
}: {
  editorRef: React.MutableRefObject<TokenEditorHandle | null>;
  serializedInput: string;
  inputHistory: string[];
  sessionHistoryKey: string;
  syncEditorHeight: () => void;
  clearActiveToken: () => void;
  clearCandidates: () => void;
  setSerializedInput: (value: string) => void;
}) {
  const [inputHistoryIndex, setInputHistoryIndex] = useState<number | null>(null);
  const inputHistoryDraftRef = useRef("");
  const applyingInputHistoryRef = useRef(false);

  useEffect(() => {
    setInputHistoryIndex(null);
    inputHistoryDraftRef.current = "";
  }, [sessionHistoryKey]);

  useEffect(() => {
    if (inputHistoryIndex !== null && inputHistoryIndex >= inputHistory.length) {
      setInputHistoryIndex(null);
      inputHistoryDraftRef.current = "";
    }
  }, [inputHistory.length, inputHistoryIndex]);

  const resetHistoryCursor = useCallback(() => {
    setInputHistoryIndex(null);
    inputHistoryDraftRef.current = "";
  }, []);

  const applyInputHistoryAt = useCallback((index: number | null) => {
    const nextText = index === null ? inputHistoryDraftRef.current : inputHistory[index] || "";
    applyingInputHistoryRef.current = true;
    editorRef.current?.setText(nextText);
    setSerializedInput(nextText);
    clearActiveToken();
    clearCandidates();
    setInputHistoryIndex(index);
    requestAnimationFrame(() => {
      applyingInputHistoryRef.current = false;
      syncEditorHeight();
    });
  }, [editorRef, inputHistory, syncEditorHeight, clearActiveToken, clearCandidates, setSerializedInput]);

  const navigateInputHistory = useCallback((direction: "previous" | "next"): boolean => {
    if (inputHistory.length === 0) {
      return false;
    }
    if (direction === "previous") {
      if (inputHistoryIndex === null) {
        inputHistoryDraftRef.current = serializedInput;
        applyInputHistoryAt(inputHistory.length - 1);
        return true;
      }
      if (inputHistoryIndex > 0) {
        applyInputHistoryAt(inputHistoryIndex - 1);
      }
      return true;
    }
    if (inputHistoryIndex === null) {
      return false;
    }
    if (inputHistoryIndex < inputHistory.length - 1) {
      applyInputHistoryAt(inputHistoryIndex + 1);
    } else {
      applyInputHistoryAt(null);
    }
    return true;
  }, [applyInputHistoryAt, inputHistory, inputHistoryIndex, serializedInput]);

  return {
    inputHistoryIndex,
    setInputHistoryIndex,
    applyingInputHistoryRef,
    inputHistoryDraftRef,
    navigateInputHistory,
    resetHistoryCursor,
  };
}
