export type AgentStatus = {
  name: string;
  protocol?: string;
  brief?: string;
  installed: boolean;
  available: boolean;
  version?: string;
  error?: string;
  last_probe?: string;
  current_model_id?: string;
  current_mode_id?: string;
  default_model_id?: string;
  default_effort?: string;
  default_fast_service?: string;
  last_config_selection?: AgentLastConfigSelection;
  supports_api_provider_switch?: boolean;
  supported_api_provider_protocols?: string[];
  supports_fast_service?: boolean;
  efforts?: string[];
  models?: AgentModelInfo[];
  modes?: AgentModeInfo[];
  models_error?: string;
  modes_error?: string;
  commands?: AgentCommandInfo[];
  commands_error?: string;
  install_commands?: string[];
  update_commands?: string[];
};

export type AgentLastConfigSelection = {
  type?: string;
  id?: string;
  name?: string;
};

export type AgentModelInfo = {
  id: string;
  name: string;
  description?: string;
  hidden?: boolean;
  supportEffort?: boolean;
  efforts?: string[];
};

export type AgentModeInfo = {
  id: string;
  name: string;
  description?: string;
};

export type AgentCommandInfo = {
  name: string;
  description?: string;
  argument_hint?: string;
};

export type AgentConfigSource = {
  sourcePath: string;
  backupPath: string;
};

export type AgentConfigBackup = {
  id: string;
  agent: string;
  name: string;
  source?: "local" | "shared";
  createdAt: string;
  updatedAt: string;
  sources?: AgentConfigSource[];
  envKeys?: string[];
  sync_state?: AgentConfigSyncResult["state"];
  sync_message?: string;
  localCredentialAvailable?: boolean;
};

export type AgentConfigHarnessInfo = {
  name: string;
  available: boolean;
  shared_dir?: string;
  message?: string;
};

export type AgentConfigSyncResult = {
  state: "idle" | "syncing" | "synced" | "pending" | "conflict" | "invalid" | "unavailable";
  commit_ids?: string[];
  last_attempt_at?: string;
  last_success_at?: string;
  message?: string;
  harnesses?: AgentConfigHarnessInfo[];
};

export type AgentAPIProvider = {
  id: string;
  name: string;
  kind?: "official";
  baseUrl: string;
  protocols: string[];
  modelFamilies: string[];
  models?: string[];
  createdAt: string;
  updatedAt: string;
  localCredentialAvailable?: boolean;
  sync_state?: AgentConfigSyncResult["state"];
  sync_message?: string;
};

export type ShellStatus = {
  id: string;
  name?: string;
  label: string;
  command: string;
  resolved_command?: string;
  args?: string[];
  default?: boolean;
};
