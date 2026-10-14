export type SessionMode = "chat" | "plugin" | "command";
export type WSStatus = "connecting" | "connected" | "reconnecting" | "disconnected";

export type CandidateType = "file" | "skill" | "slash_command" | "prompt" | "command";

export type TokenEditorHandle = {
  focus: () => void;
  blur: () => void;
  getHeight: () => number;
  clear: () => void;
  setText: (value: string) => void;
  getSerializedText: () => string;
  insertCandidate: (type: CandidateType, value: string) => void;
};

export type PendingAttachment = {
  id: string;
  file: File;
  previewUrl?: string;
  isImage: boolean;
};
