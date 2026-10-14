import type { EditorState } from "@codemirror/state";
import type { EditorView as CodeMirrorView } from "@codemirror/view";

export type FilePayload = {
  revision?: string;
  name: string;
  path: string;
  content: string;
  encoding: string;
  truncated: boolean;
  next_cursor?: number;
  size: number;
  ext?: string;
  mime?: string;
  mtime?: string;
  root?: string;
  file_meta?: any[];
  targetLine?: number;
  targetColumn?: number;
};

export type EditableFile = FilePayload & { revision: string };

export type EditorView = {
  start: number;
  end: number;
  direction: "forward" | "backward" | "none";
  top: number;
  left: number;
  editorState?: EditorState;
  scrollSnapshot?: ReturnType<CodeMirrorView["scrollSnapshot"]>;
};

export type FileEditSession = {
  rootId: string;
  path: string;
  text: string;
  savedText: string;
  source: string;
  file?: EditableFile;
  revision: string;
  loading: boolean;
  saving: boolean;
  leaving: boolean;
  error: string;
  view: EditorView;
};

export interface FileEditStoreContract {
  get(key: string): FileEditSession | undefined;
  patch(key: string, patch: Partial<FileEditSession>): void;
  close(key: string): void;
  save(key: string): Promise<EditableFile | null>;
}
