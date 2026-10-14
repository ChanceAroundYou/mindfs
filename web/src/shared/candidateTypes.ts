export type CandidateType = "file" | "skill" | "prompt" | "command";
export type CandidateItemType = CandidateType | "slash_command";

export type CandidateItem = {
  type: CandidateItemType;
  name: string;
  description?: string;
};
