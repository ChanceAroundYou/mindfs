import type * as React from "react";

export type SessionRuntimeMeta = {
  agent?: string;
  model?: string;
  model_display_name?: string;
  mode?: string;
  effort?: string;
  fast_service?: "" | "on" | "off";
};
