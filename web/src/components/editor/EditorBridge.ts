import React from "react";
import { useEditorBridge, type EditorBridgeProps, type BridgeOnChangePayload } from "./useEditorBridge";

export type { BridgeOnChangePayload };

export function EditorBridge(props: EditorBridgeProps) {
  useEditorBridge(props);
  return null;
}
