import { MarkdownViewer } from "../file/MarkdownViewer";
import { AnsiOutput, terminalFontFamily } from "./toolCallRender";

export function basename(path: string): string {
  const normalized = (path || "").replace(/\\/g, "/");
  const parts = normalized.split("/");
  return parts[parts.length - 1] || path;
}

export function shouldPreserveDisplayStatus(current?: string, incoming?: string): boolean {
  const currentStatus = (current || "").toLowerCase();
  const incomingStatus = (incoming || "").toLowerCase();
  const currentIsTerminal =
    currentStatus === "complete" ||
    currentStatus === "success" ||
    currentStatus === "failed" ||
    currentStatus === "error" ||
    currentStatus === "cancelled";
  const incomingIsRunning =
    incomingStatus === "running" ||
    incomingStatus === "pending" ||
    incomingStatus === "in_progress";
  return currentIsTerminal && incomingIsRunning;
}

export function isRunningStatus(status?: string): boolean {
  const normalized = (status || "").toLowerCase();
  return normalized === "running" || normalized === "pending" || normalized === "in_progress";
}

export function stringMeta(meta: Record<string, unknown> | undefined, key: string): string {
  const value = meta?.[key];
  return typeof value === "string" ? value.trim() : "";
}

export function toolProgressText(meta: Record<string, unknown> | undefined): string {
  const progress = stringMeta(meta, "progress");
  if (progress) return progress;
  const lastToolName = stringMeta(meta, "lastToolName");
  return lastToolName ? `Using ${lastToolName}` : "";
}

export function extractExecuteCommand(meta: Record<string, unknown> | undefined, fallbackTitle: string): string {
  const command = stringMeta(meta, "command");
  if (command) return command;

  const rawInput = stringMeta(meta, "input");
  if (rawInput) {
    try {
      const parsed = JSON.parse(rawInput) as { command?: unknown };
      if (typeof parsed.command === "string" && parsed.command.trim()) {
        return parsed.command;
      }
    } catch {
      return rawInput;
    }
    return rawInput;
  }

  return fallbackTitle;
}

export function ExecuteToolDetails({
  command,
  output,
  onOutputRendered,
}: {
  command: string;
  output: string;
  onOutputRendered?: () => void;
}) {
  const hasCommand = command.length > 0;
  const hasOutput = output.trim().length > 0;
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "10px", marginTop: "10px", minWidth: 0 }}>
      {hasCommand ? (
        <div
          style={{
            padding: "8px",
            borderRadius: "8px",
            background: "rgba(47, 128, 237, 0.10)",
            border: "1px solid rgba(47, 128, 237, 0.24)",
            minWidth: 0,
          }}
        >
          <pre
            style={{
              margin: 0,
              color: "var(--mindfs-code-text, var(--text-primary))",
              fontFamily: terminalFontFamily,
              fontSize: "12px",
              lineHeight: 1.6,
              whiteSpace: "pre-wrap",
              overflowWrap: "anywhere",
              wordBreak: "break-word",
            }}
          >
            {command}
          </pre>
        </div>
      ) : null}
      {hasOutput ? <AnsiOutput text={output} onRendered={onOutputRendered} /> : null}
    </div>
  );
}

export function CollabToolDetails({ meta, rootId }: { meta?: Record<string, unknown>; rootId?: string | null }) {
  const prompt = typeof meta?.prompt === "string" ? meta.prompt.trim() : "";
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "10px", marginTop: "10px", minWidth: 0 }}>
      {prompt ? <MarkdownViewer content={prompt} root={rootId || undefined} /> : null}
    </div>
  );
}
