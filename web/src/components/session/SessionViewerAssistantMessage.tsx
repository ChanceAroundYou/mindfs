import React from "react";
import { MarkdownViewer } from "../common/MarkdownViewer";

export function SessionViewerAssistantMessage(props: any) {
  const { item, rootId, onFileClickRef } = props;
  return (
    // 页脚不在这里：它属于整轮（见 SessionViewerTurnMeta 的注释）。这里只画正文。
    <div
      style={{
        color: "var(--text-primary)",
        fontSize: "15px",
        lineHeight: "1.7",
        width: "100%",
        minWidth: 0,
      }}
    >
      <MarkdownViewer
        content={item.content || ""}
        root={rootId || undefined}
        onFileClick={(path) => onFileClickRef.current?.({ path })}
      />
    </div>
  );
}
