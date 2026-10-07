export type ReadMode = "full" | "incremental";

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

export type FetchFileParams = {
  fresh?: boolean;
  rootId: string;
  path: string;
  readMode?: ReadMode;
  cursor?: number;
  timeoutMs?: number;
  nodeId?: string;
};

export type CachedFileRecord = {
  type?: "file";
  key: string;
  rootId: string;
  path: string;
  readMode: ReadMode;
  cursor: number;
  touchedAt: number;
  file: FilePayload;
};

export type CachedGitDiffPayload = {
  path: string;
  status: string;
  additions: number;
  deletions: number;
  content: string;
  file_meta?: Array<{
    source_session: string;
    session_name?: string;
    agent?: string;
    created_at?: string;
    updated_at?: string;
    created_by?: string;
  }>;
};

export type CachedGitDiffRecord = {
  type: "git-diff";
  key: string;
  rootId: string;
  path: string;
  touchedAt: number;
  diff: CachedGitDiffPayload;
};

export type CacheRecord = CachedFileRecord | CachedGitDiffRecord;

export type FileResponse = {
  file?: FilePayload | null;
};
