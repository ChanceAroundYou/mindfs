import type { MessageKey } from "../i18n/index";

export type ErrorCode =
  // Session errors
  | "session.not_found"
  | "session.create_failed"
  | "session.closed"
  | "session.resume_failed"
  | "session.delete_failed"
  | "session.import_failed"
  | "session.rename_failed"
  | "session.pin_failed"
  | "session.archive_failed"
  | "session.sync_failed"
  | "session.list_load_failed"
  | "session.slash_command_failed"
  | "app.init_failed"
  // Root/project errors
  | "root.create_failed"
  | "root.delete_failed"
  | "root.rename_failed"
  | "git.checkout_failed"
  | "git.related_file_diff_failed"
  | "git.worktree_switch_failed"
  | "git.worktree_remove_failed"
  // Agent errors
  | "agent.unavailable"
  | "agent.timeout"
  | "agent.crashed"
  | "agent.permission_denied"
  // View errors
  | "view.invalid"
  | "view.render_failed"
  // File errors
  | "file.not_found"
  | "file.read_failed"
  | "file.write_failed"
  // Task errors
  | "task.action_failed"
  // Clipboard errors
  | "clipboard.write_failed"
  // Skill errors
  | "skill.not_found"
  | "skill.execute_failed"
  // Network errors
  | "network.disconnected"
  | "network.timeout"
  // Remote node errors
  | "node.load_failed";

export type ErrorSeverity = "info" | "warning" | "error" | "fatal";

export type AppError = {
  code: ErrorCode;
  message: string;
  messageKey?: MessageKey;
  usesDefaultMessage?: boolean;
  severity: ErrorSeverity;
  recoverable: boolean;
  retryAction?: () => Promise<void>;
  details?: Record<string, unknown>;
};
