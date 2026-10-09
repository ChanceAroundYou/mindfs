import React from "react";

const agentConfigLineEditorStyle: React.CSSProperties = {
  display: "flex",
  flexDirection: "column",
  gap: "4px",
};

const agentConfigLineTextAreaStyle: React.CSSProperties = {
  width: "100%",
  border: "1px solid var(--border-color)",
  borderRadius: "6px",
  padding: "6px 8px",
  fontSize: "12px",
  fontFamily: "monospace",
  background: "var(--bg-primary)",
  color: "var(--text-primary)",
  resize: "none",
  overflow: "hidden",
  lineHeight: "1.4",
};

export function AgentConfigLineEditor({
  value,
  onChange,
  placeholder,
}: {
  value: string;
  onChange: (value: string) => void;
  placeholder: string;
}) {
  const refs = React.useRef<Array<HTMLTextAreaElement | null>>([]);
  const lines = value.length > 0 ? value.split("\n") : [""];

  const emitLines = React.useCallback((nextLines: string[]) => {
    onChange(nextLines.join("\n"));
  }, [onChange]);

  React.useLayoutEffect(() => {
    for (const node of refs.current) {
      if (!node) {
        continue;
      }
      node.style.height = "auto";
      node.style.height = `${node.scrollHeight}px`;
    }
  }, [lines]);

  return (
    <div style={agentConfigLineEditorStyle}>
      {lines.map((line, index) => (
        <textarea
          key={index}
          ref={(node) => {
            refs.current[index] = node;
          }}
          value={line}
          onChange={(event) => {
            const nextValue = event.target.value;
            const nextLines = [...lines];
            if (nextValue.includes("\n")) {
              nextLines.splice(index, 1, ...nextValue.split(/\r?\n/));
            } else {
              nextLines[index] = nextValue;
            }
            emitLines(nextLines);
          }}
          onKeyDown={(event) => {
            const target = event.currentTarget;
            if (event.key === "Enter") {
              event.preventDefault();
              const before = line.slice(0, target.selectionStart);
              const after = line.slice(target.selectionEnd);
              const nextLines = [...lines];
              nextLines.splice(index, 1, before, after);
              emitLines(nextLines);
              window.setTimeout(() => refs.current[index + 1]?.focus(), 0);
              return;
            }
            if (event.key === "Backspace" && target.selectionStart === 0 && target.selectionEnd === 0 && index > 0) {
              event.preventDefault();
              const previous = lines[index - 1] || "";
              const nextLines = [...lines];
              nextLines.splice(index - 1, 2, previous + line);
              emitLines(nextLines);
              window.setTimeout(() => {
                const previousNode = refs.current[index - 1];
                previousNode?.focus();
                previousNode?.setSelectionRange(previous.length, previous.length);
              }, 0);
              return;
            }
            if (event.key === "Delete" && target.selectionStart === line.length && target.selectionEnd === line.length && index < lines.length - 1) {
              event.preventDefault();
              const nextLines = [...lines];
              nextLines.splice(index, 2, line + (lines[index + 1] || ""));
              emitLines(nextLines);
              window.setTimeout(() => {
                const node = refs.current[index];
                node?.focus();
                node?.setSelectionRange(line.length, line.length);
              }, 0);
            }
          }}
          placeholder={lines.length === 1 && !line ? placeholder : ""}
          rows={1}
          style={agentConfigLineTextAreaStyle}
        />
      ))}
    </div>
  );
}
