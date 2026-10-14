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
  createdAt: string;
  updatedAt: string;
  sources?: AgentConfigSource[];
  envKeys?: string[];
};

export type AgentAPIProvider = {
  id: string;
  name: string;
  baseUrl: string;
  protocols: string[];
  modelFamilies: string[];
  models?: string[];
  createdAt: string;
  updatedAt: string;
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
