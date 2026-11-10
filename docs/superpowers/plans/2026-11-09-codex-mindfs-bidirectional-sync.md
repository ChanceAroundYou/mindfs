# Codex and MindFS Configuration Sync Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Codex `default` and `llmux` settings plus MindFS provider metadata bidirectionally sync through the private `~/.agents` Git checkout while retaining full backups and credentials locally.

**Architecture:** `~/.agents/codex` holds only portable, allowlisted configuration and provider metadata. A `mindfs agents-config-sync` command and the MindFS service use one shared Go package for migration, validation, Git transport, and rendering. Each device stores its active shared profile locally; runtime `config.toml` is rendered from shared `default` plus the selected `llmux` overlay when active. MindFS keeps complete snapshots locally, separates API keys from shared provider metadata, and retries pending synchronization while the service runs. Codex reads the shared llmux profile through a relative symlink where supported; Windows uses a tracked local copy and imports local edits only when the shared source has not changed, otherwise it reports a conflict.

**Tech Stack:** Go, Git CLI, `github.com/pelletier/go-toml/v2` v2.4.3 or later, React/TypeScript.

**Spec:** `/home/xiaokubao/.agents/codex/docs/superpowers/specs/2026-11-09-codex-mindfs-bidirectional-sync-design.md`

## Global Constraints

- Full MindFS snapshots, Codex auth files, API keys, project trust, hook trust hashes, and device-specific paths stay outside Git.
- Shared Codex default keys are limited to `model`, `model_reasoning_effort`, and `features.goals`; `tui`, `projects`, and `hooks` remain local.
- The shared `llmux` profile uses `env_key = "LLMUX_API_KEY"`; never serialize a bearer token or API key into shared TOML/JSON.
- Only explicitly allowlisted `~/.agents/codex/{config.toml,profiles/llmux.config.toml,providers.json}` paths may be committed; never use blanket `git add`.
- Git integration uses the configured upstream ref, no force push, bounded retries, and visible conflict state. Validation or conflict errors stop before replacing runtime files.
- Local credential files use mode `0600`; logs and API responses never include credential values.
- Existing MindFS backups retain their complete local restore behavior. Shared profiles are listed and switched as a separate source kind.
- Bound TOML/JSON reads to 1 MiB and use go-toml v2.4.3+; [the upstream advisory](https://github.com/pelletier/go-toml/security/advisories/GHSA-frc4-6h9q-39fq) marks earlier versions vulnerable to unrecoverable stack overflow on deeply nested input.
- Follow MindFS Go/frontend layering, i18n, file-size, and fork-customization rules. Register code changes as a new `G-KP` group in `docs/upstream-customizations.yaml` with matching targeted tests.
- No remote device is built or deployed outside MindFS's `scripts/deploy-all.sh` workflow. The runtime sync itself needs only the deployed MindFS binary, Git, and a configured `.agents` checkout.

## Review Focus

- Same-file concurrent edits: the sync command must stop with both versions recoverable and must not render either version over the current runtime.
- Unrelated dirty `.agents` paths or already-created local commits: syncing must refuse to push them and must leave the worktree untouched.
- Secret-like fields in provider JSON or profile TOML: reject before commit or runtime replacement, without echoing the value.
- Missing local LLMUX credentials: sync shared metadata successfully, keep the provider visible, and report that it cannot be used on that device.
- Invalid/oversized TOML or JSON, or failed atomic rendering: stop before replacing active configuration and retain the previous runtime file as rollback copy.

---

## File Map

### Shared source (`~/.agents`)

- Create `codex/profiles/llmux.config.toml` and `codex/providers.json` as the portable shared sources.
- Keep `codex/config.toml` as the shared default source. `$CODEX_HOME/llmux.config.toml` points to the shared profile through a relative symlink where supported; a Windows copy fallback records its last-applied hash in local MindFS state and detects local edits/conflicts.

### MindFS sync engine and CLI

- Create `internal/codexsync/` with focused files for paths, migration, TOML profiles, provider metadata, Git transport, locking, rendering, and retry runner.
- Add `mindfs agents-config-sync --check|--apply|--sync` in `cli/cmd/agent_config_sync.go`; the CLI and API call the same Go package.
- `server/app/server.go` starts the process runner. API handlers request an immediate run after successful provider/profile saves.

### MindFS API, UI, and customization record

- `server/internal/api/agent_api_provider.go` and a focused storage file separate shared metadata from device-local secrets.
- `server/internal/api/agent_config_backup.go` exports sanitized projections and returns both local snapshots and shared profiles; `agent_config_switch.go` applies them without copying auth.
- Add safe sync status and manual retry handlers at `/api/agent-config/sync`.
- `web/src/shared/agentTypes.ts`, `web/src/services/agent/agentConfig.ts`, `web/src/components/file/useAgentConfigSelection.ts`, `useAgentConfigActions.ts`, and `AgentConfigPopover.tsx` show local/shared profiles, missing-key state, and sync status.
- Create `web/src/components/file/AgentConfigProfileList.tsx` and `AgentConfigSyncStatus.tsx` so the existing 450-line popover stays below the 500-line hard limit.
- Add matching strings to `web/src/i18n/locales/en-US-agents.ts` and `web/src/i18n/locales/zh-CN-agents.ts`.
- Extend `web/tests/file-tree-agent-config-flow.test.mjs` and register backend/frontend files plus targeted tests in a new `G-KP` entry in `docs/upstream-customizations.yaml`.

## Plan

### Task 1: Shared source, Go sync engine, and CLI

**Files:**
- Create: `.agents/codex/profiles/llmux.config.toml`
- Create: `.agents/codex/providers.json`
- Create: `internal/codexsync/paths.go`
- Create: `internal/codexsync/migrate.go`
- Create: `internal/codexsync/profiles.go`
- Create: `internal/codexsync/providers.go`
- Create: `internal/codexsync/git.go`
- Create: `internal/codexsync/render.go`
- Create: `internal/codexsync/profile_link.go`
- Create: `internal/codexsync/profile_link_test.go`
- Create: `internal/codexsync/lock_unix.go` and `internal/codexsync/lock_windows.go`
- Create: `internal/codexsync/sync_test.go`
- Create: `cli/cmd/agent_config_sync.go` and `cli/cmd/agent_config_sync_test.go`
- Modify: `cli/cmd/mindfs.go`
- Modify: `go.mod` and `go.sum`
- Modify: `.agents/codex/config.toml`

**Interfaces:**
- CLI: `mindfs agents-config-sync --check | --apply | --sync` is recognized before the normal server flags are parsed.
- `codexsync.Run(ctx context.Context, mode Mode) (Result, error)` supports `Check`, `Apply`, and `Sync`; `Check` is read-only, `Apply` performs one-time local migration/bootstrap/rendering, and `Sync` performs routine Git transport then rendering without reseeding snapshots.
- `Result` carries `State`, `CommitIDs`, `LastAttemptAt`, `LastSuccessAt`, and a sanitized `Message`.
- Shared llmux schema contains `model`, optional `model_reasoning_effort`, `model_provider = "llmux"`, and `[model_providers.llmux]` with `name`, `base_url`, `wire_api`, and `env_key = "LLMUX_API_KEY"`.
- Shared provider JSON contains only `id`, `name`, `baseUrl`, `protocols`, `modelFamilies`, `models`, `createdAt`, and `updatedAt`.

- [x] Add tests for read-only `Check`, idempotent migration, upstream-ref mapping (including local `master` tracking `origin/main`), dirty unrelated paths, pre-existing local commits outside the allowlist, secret rejection, invalid/oversized TOML/JSON, conflicted rebase preservation, and a stale-push race.
- [x] Add profile-link tests for relative symlink creation plus Windows copy fallback: import local-only edits, and stop with both copies preserved when both sides changed.
- [x] Add go-toml v2.4.3+ and decode only size-bounded input; use explicit allowlists for shared fields and credentials.
- [x] Implement per-checkout locking for Unix and Windows; concurrent invocations must exit without modifying files.
- [x] Implement Git transport: explicit-path staging, commit identity check, fetch configured upstream, rebase only unpublished allowed commits, exact upstream-ref push, at most three push-race retries, and no force/reset. On conflict, leave Git's conflict state intact and list paths.
- [x] Make `Apply` seed from `<MindFSConfigDir>/agents-config/codex-default/002-config.toml`, `codex-llmux/002-config.toml`, and legacy `<MindFSConfigDir>/api-providers.json` only once. Resolve `MindFSConfigDir` as `os.UserConfigDir()/mindfs`, matching the existing backend path helper. Preserve all source snapshots and the legacy provider file; write timestamped rollback copies before migration. A local version marker prevents rerunning the seed after migration.
- [x] Treat missing snapshots or a missing legacy provider file as an empty local seed; malformed existing files stop migration before any change.
- [x] During seeding, merge only missing allowlisted values into existing shared sources; if an existing shared value differs from the snapshot for the same key, stop and report the exact key rather than choosing a winner.
- [x] Render `$CODEX_HOME/config.toml` from the shared default plus device-local overlay, preserving `projects`, `tui`, and `hooks`; remove the old in-base llmux bearer field and use the separate shared profile with local `LLMUX_API_KEY`.
- [x] Before every runtime replacement, save the prior config as a timestamped rollback copy and replace via a same-directory temporary file and atomic rename. Preserve an existing regular `llmux.config.toml` before linking.
- [x] On Windows, if symlink creation is unavailable, materialize an atomic local profile copy and store its last-applied hash outside `.agents`; import a local edit only when the shared source still matches the stored hash, otherwise preserve both and report conflict.
- [x] Run CLI and sync-engine tests in temporary repositories, then verify `Check` leaves tracked/untracked files and runtime mtimes unchanged.

### Task 2: Provider metadata and local API keys

**Files:**
- Modify: `server/internal/api/agent_api_provider.go`
- Modify: `server/internal/api/agent_api_provider_sync_ops.go`
- Create: `server/internal/api/agent_api_provider_storage.go`
- Modify: `server/internal/api/agent_api_provider_sync_test.go`
- Modify: `server/internal/api/agent_api_provider_security_test.go`
- Modify: `internal/codexsync/providers.go`

**Interfaces:**
- `readAgentAPIProviders() ([]agentAPIProvider, error)` merges shared metadata at `$AGENTS_ROOT/codex/providers.json` with local keys in `<MindFSConfigDir>/api-provider-secrets.json` by provider ID.
- `writeAgentAPIProviders([]agentAPIProvider) error` atomically writes shared metadata and mode-`0600` local secrets independently.
- The public provider response adds `localCredentialAvailable bool`; it never includes a key.

- [x] Add tests for metadata-only shared JSON, local key merge, absent-key behavior, 0600 permissions, and key-free public responses.
- [x] Read the legacy provider file only as fallback until explicit `Apply` migration succeeds; after migration prefer shared metadata even when it is empty, preventing deleted providers from reappearing.
- [x] Change create/update/delete paths to preserve local keys by ID and update shared metadata without serializing `apiKey`.
- [x] Make model refresh retain old metadata on probe failure; if a local key is missing, report that provider as unavailable and continue refreshing others.
- [x] Run the focused provider tests and inspect generated shared JSON to confirm it contains no key field.

### Task 3: Export snapshots and expose shared Codex profiles

**Files:**
- Modify: `server/internal/api/agent_config.go`
- Modify: `server/internal/api/agent_config_backup.go`
- Modify: `server/internal/api/agent_config_switch.go`
- Create: `server/internal/api/agent_config_shared.go`
- Modify: `server/internal/api/agent_config_test.go`
- Modify: `internal/codexsync/profiles.go`

**Interfaces:**
- Extend `agentConfigManifestEntry` response with `Source string`; old manifest rows without it decode as `local`, while shared rows are synthesized and never written to `manifest.json`.
- Shared IDs are stable: `shared:codex-default` and `shared:codex-llmux`.
- `exportCodexSharedProfile(name, snapshotPath string) error` writes only allowlisted settings to the corresponding shared source, validates it, and requests sync.
- `switchAgentConfig` dispatches local IDs to the current restore path; shared default re-renders the base with local overlay, and shared llmux validates/repairs the profile link without copying auth.

- [x] Add tests proving default export removes `tui`, `projects`, and `hooks` while retaining `model`, `model_reasoning_effort`, and `features.goals`.
- [x] Add tests proving llmux export removes `experimental_bearer_token`, substitutes `env_key`, and retains only approved provider/profile fields.
- [x] Keep complete local snapshot creation/overwrite behavior, then export only for Codex backups named `default` or `llmux`.
- [x] Merge shared profiles into the list response with `source: "shared"`; do not add them to or rewrite the local manifest.
- [x] Implement shared switching through the renderer and retain local snapshot overwrite-confirmation behavior.
- [x] Run focused agent-config API tests and compare local snapshots before and after shared-profile application.

### Task 4: Provider availability and backup UI

**Files:**
- Modify: `web/src/shared/agentTypes.ts`
- Modify: `web/src/services/agent/agentConfig.ts`
- Modify: `web/src/components/file/useAgentConfigSelection.ts`
- Modify: `web/src/components/file/useAgentConfigActions.ts`
- Modify: `web/src/components/file/AgentConfigPopover.tsx`
- Create: `web/src/components/file/AgentConfigProfileList.tsx`
- Create: `web/src/components/file/AgentConfigSyncStatus.tsx`
- Modify: `web/src/i18n/locales/en-US-agents.ts`
- Modify: `web/src/i18n/locales/zh-CN-agents.ts`
- Modify: `web/tests/file-tree-agent-config-flow.test.mjs`

**Interfaces:**
- `AgentConfigBackup.source` is `"local" | "shared"`; absent `source` from older responses means `local`.
- `AgentAPIProvider.localCredentialAvailable` is a boolean; `AgentConfigSyncResult` mirrors the safe `codexsync.Result` JSON fields.
- Add `fetchAgentConfigSyncStatus()` and `requestAgentConfigSync()` to the service.

- [x] Add UI assertions for separate shared/local sections, no delete action on shared profiles, shared selection, and missing-local-key status.
- [x] Label shared entries as synchronized profiles and local entries as device recovery snapshots; keep local delete/overwrite actions limited to local entries.
- [x] Apply shared selection through the existing switch endpoint using stable IDs; keep overwrite confirmation for local snapshots only.
- [x] Show synced/pending/conflict state and a manual retry action without exposing raw Git output or secret values.
- [x] Run focused frontend flow tests and i18n key parity check.

### Task 5: Save-triggered sync, status, and periodic retry

**Files:**
- Create: `internal/codexsync/runner.go`
- Create: `internal/codexsync/runner_test.go`
- Modify: `server/app/server.go`
- Modify: `server/internal/api/agent_config_backup.go`
- Modify: `server/internal/api/agent_api_provider_sync_ops.go`
- Modify: `server/internal/api/http_routes.go`
- Create: `server/internal/api/agent_config_sync_handlers.go`
- Create: `server/internal/api/agent_config_sync_handlers_test.go`
- Modify: `docs/upstream-customizations.yaml`

**Interfaces:**
- `codexsync.StartRunner(ctx context.Context, interval time.Duration)` performs one startup sync and retries every five minutes while MindFS runs.
- `codexsync.Request(ctx context.Context) Result` coalesces an immediate run after a successful provider or named Codex backup save; it returns current safe state without blocking or rolling back the local save.
- `GET /api/agent-config/sync` returns safe status (`idle`, `syncing`, `synced`, `pending`, `conflict`, `invalid`, or `unavailable`); `POST` requests retry. Neither returns raw command output or credentials.

- [x] Add runner tests for missing checkout, duplicate request coalescing, timeout, and retry after a failed command.
- [x] Add handler checks for safe status fields and ensure a failed render leaves the old runtime file intact.
- [x] Start one process runner from the MindFS lifetime context; missing `.agents` checkout or Git reports `unavailable` and does not block server startup.
- [x] Request an immediate run after successful provider/profile saves; report `pending` if Git transport fails while preserving the local save.
- [x] Expose status and manual retry through the API; attach safe sync state to save responses for immediate UI feedback.
- [x] Register all backend/frontend files and targeted tests as `G-KP`, including symptom, preservation reason, test anchors, and `Scope: G-KP` commit requirement.
- [x] Run focused and full backend/frontend checks. The required `ship` workflow remains pending final review and target-device inventory.

### Task 6: Preserve the active Codex profile and simplify switching

**Files:**
- Create: `internal/codexsync/active_profile.go` and `internal/codexsync/active_profile_test.go`
- Modify: `internal/codexsync/types.go`, `internal/codexsync/render.go`, `internal/codexsync/run.go`, `internal/codexsync/profile_link.go`, and their focused tests
- Modify: `server/app/server.go` and startup tests
- Modify: `server/internal/api/agent_api_provider_apply.go`, `server/internal/api/agent_api_provider_sync_ops.go`, `server/internal/api/agent_api_provider_caps.go`, `server/internal/api/agent_api_provider_sync_test.go`
- Modify: `server/internal/api/agent_config_backup.go`, `server/internal/api/agent_config_shared.go`, `server/internal/api/agent_config_switch.go`, `server/internal/api/agent_config_shared_test.go`
- Modify: `web/src/components/file/useAgentConfigSelection.ts`, `web/src/components/file/useAgentConfigActions.ts`, `web/src/components/file/AgentConfigPopover.tsx`, `web/src/components/file/AgentConfigProfileList.tsx`, `web/src/components/file/agentConfigProfiles.ts`, `web/tests/agent-config-custom-codex.test.ts`, and `web/tests/file-tree-agent-config-flow.test.mjs`
- Modify: `docs/upstream-customizations.yaml`
- Modify shared CLI adapter: `~/.agents/claude/scripts/sync-codex.sh` and `~/.agents/claude/tests/test_sync_codex.sh`

**Interfaces:**
- `codexsync.InitializeActiveProfile(name string) (string, error)` writes the local marker only when one does not already exist; `ActiveProfile() (string, error)` reads the persisted `default` or `llmux` value.
- `codexsync.RenderRuntime()` and `codexsync.CheckRuntime()` use the same profile-aware renderer. `mindfs agents-config-sync --render` applies it without Git transport; `--check-runtime` compares the expected rendering without writing.
- Existing `api_provider` selection `api-llmux` migrates to `backup/shared:codex-llmux`; other Codex API-provider selections fall back to `shared:codex-default`. Provider create/list/test/delete and metadata sync remain available.
- Codex's switch UI lists only shared profiles. Local Codex snapshots remain stored for recovery and cannot be applied through the switch endpoint. Shared IDs cannot be deleted; failed delete requests refresh the list.

- [x] Add failing tests for active-profile rendering, persistence across `ModeSync`, first-start migration from `api-llmux`, and rollback of the active marker when rendering fails.
- [x] Add failing API tests proving Codex provider switching/reapply cannot write `config.toml`, local Codex snapshots cannot be selected, and deleting a shared ID returns an explicit error.
- [x] Add failing UI tests proving Codex shows only the shared profiles, initializes selection from the last shared selection, and refetches after a failed delete.
- [x] Make the old `sync-codex.sh` `--apply` and `--check` delegate to MindFS's same renderer; test both `default` and `llmux` using isolated paths.
- [x] Implement active-profile state and profile-aware runtime merging. Preserve local `tui`, `projects`, and `hooks`, and keep llmux credentials out of shared files.
- [x] Migrate a legacy `api-llmux` last selection once at service startup, before the periodic sync runner starts; persist the resulting shared-profile selection.
- [x] Keep API-provider metadata operations available while rejecting Codex provider runtime switches and skipping Codex during provider-refresh reapply.
- [x] Limit Codex switching to shared profiles, retain local snapshots without exposing them as switch targets, and make deletion errors explicit with a UI refresh after failure.
- [x] Register the new MindFS files and targeted tests as `G-KQ`; run focused tests, `make test`, `npm run typecheck`, `npm test`, `go run ./scripts/check-upstream`, and the shared shell test after installing the new CLI.
- [x] Run `bash scripts/deploy-all.sh` from `main`; verify worker process health and report the required 本機 restart command without executing it.

## Acceptance Checklist

- [x] A shared-source edit on one checkout reaches another after its next successful sync; a stale push race retries without force push.
- [x] Same-path conflicts, unrelated dirty paths, and local commits containing unrelated files remain visible and block runtime replacement/push.
- [x] API keys, OAuth tokens, account IDs, trust paths, and hook hashes stay local; API responses expose only credential availability.
- [x] Local `codex-default` and `codex-llmux` snapshots remain complete and restorable; remote updates do not rewrite them.
- [x] Shared profiles appear separately in MindFS and apply without changing `auth.json` or local trust/hook sections.
- [x] A device without `LLMUX_API_KEY` receives shared metadata and reports the missing key without blocking unrelated sync.
- [x] On Windows without symlink permission, local profile edits are imported when safe and concurrent local/shared edits stop with both files recoverable.
- [x] Failed network sync leaves local edits recoverable and a later five-minute retry can push/pull them.
- [x] Initial migration creates timestamped rollback copies; `--check` is read-only.
- [x] Selecting `shared:codex-llmux` remains active after startup sync, periodic sync, and explicit `--sync`; selecting `shared:codex-default` behaves the same.
- [x] Legacy Codex `api-llmux` selection migrates once to shared llmux; provider metadata management never rewrites Codex runtime configuration.
- [x] Codex switching exposes only the two shared profiles, restores the active selection, rejects local snapshots as switch targets, and reports shared-profile deletion clearly.
- [x] The manual `sync-codex.sh` check/apply path uses the same profile-aware renderer as MindFS.

## Deployment Review

### Task 6 follow-up · 2026-11-11 — profile merge and provider switch

- Bug 1（切到 llmux 无法对话）：`mergeSharedProfiles` 把 llmux overlay 直接拼在 shared default 末尾，
  而 default 末尾是 `[features]` 表头 —— `model` / `model_provider` / `model_reasoning_effort`
  被 TOML 规则吞进该表，Codex app-server 报
  `invalid configuration: invalid type: string "gpt-6-luna", expected a boolean`，`session.send` 直接失败。
  之前的 `ValidateRenderedConfig` 只查 bearer token、`--check-runtime` 只比字节，所以状态一直显示 `synced`。
  修复：合并时按第一个表头切分，把所有根级赋值放在表体之前；渲染后新增 `validateMergedRootKeys`
  护栏（表值按子键包含比较，允许设备本地 `model_providers.local` 等兄弟键并存），键被吞掉就渲染失败而不是写坏配置。
- Bug 2（API 供应商切换报 metadata only）：Codex 的运行时只允许来自 shared profile，provider 条目是纯元数据
  （设计约束保留）。但当 provider 就是 llmux 时，这个动作本应等价于切到 `shared:codex-llmux`；
  现在 `switchAgentAPIProvider` 先查 provider，Codex 分支用 `SharedProfileForProvider`
  按「名字为 llmux 且 `base_url` 精确匹配（尾斜杠归一）」委托给该 shared profile，其余仍返回原错误。
  `applyAgentAPIProvider` 保持拒绝，reapply/sync 路径不会借 provider 元数据改写 Codex 运行时。
- 前端 `isCodexProfileAgent` 在 agent 状态缺失 `protocol`（旧版 worker）时按名字兜底识别 `codex`，
  避免露出只适用于其它 agent 的 API 供应商入口。
- 测试：`internal/codexsync/render_test.go`（合并后根键不落表、验证器拒绝嵌套键）、
  `active_profile_test.go`（渲染结果解码断言根级 `model_provider=llmux`、`features` 仅 `goals`）、
  `server/internal/api/agent_api_provider_codex_switch_test.go`（匹配 provider 委托成功并写入根级配置；
  base URL 不匹配时报原错误且不写运行时）、`web/tests/agent-config-custom-codex.test.ts`（空 protocol 兜底）。

### Task 6 follow-up · 2026-11-11

- Commits `9c42274f` and `e33a4422` shipped as `v0.5.5-1118-6-ge33a4422`. `deploy-all.sh` passed Go, TypeScript, and Vitest gates (487 tests passed), then installed the local binary and deployed WSL/fn/pc-win. Independent checks confirmed WSL PID `1372641`, fn PID `2872970`, and pc-win PID `132036`; worker versions match and health returns 200. The installed local CLI reports the same version; its system service remains on the pre-restart process.
- `.agents` commit `b6d3055` is pushed to `origin/main` and present on all four checkouts. The isolated `sync-codex.sh` test passed on 本机, WSL, and fn with 16 assertions each.
- The local `~/.config/codex/llmux.env` was copied over SSH to each device-local config path. WSL/fn files are mode `0600`; pc-win inherits an ACL limited to the current user, Administrators, and SYSTEM. The local API and all three worker APIs report `localCredentialAvailable: true` for shared llmux. No credential was added to `.agents`.
- All four installed CLIs report `--check-runtime` state `synced`. The 本机 system service still needs the user-run command `sudo systemctl restart mindfs`; it was not restarted by the agent.

### Initial deployment · 2026-11-10

`deploy-all.sh` shipped `v0.5.5-1118-4-gc6799567` to all workers. The reviewed WSL `skills/goal-prompt/SKILL.md` edit was integrated and pushed as shared commit `f84c8a95b30791e3a9e7f3a563ac96e937437001`; 本机, WSL, and fn all ran `mindfs agents-config-sync --sync` and report `synced`. PC-Win already had a populated non-Git `%USERPROFILE%\.agents` data/skill root, so its checkout lives at `%USERPROFILE%\.agents\codex-config-sync`; user-level `AGENTS_ROOT` points there, and a junction exposes the updated `goal-prompt` skill without replacing existing skills. Windows `--apply` and `--sync` succeeded; after restarting task `\mindfs`, `GET /mindfs/api/agent-config/sync` reported `synced` and `GET /mindfs/health` returned 200 (PID 22480). Its local `config.toml` moved from reasoning effort `max` to shared `medium`; a rollback copy was retained under `%APPDATA%\mindfs\agents-config-sync\rollback`. Shared provider metadata contains no API key. At that time PC-Win had no local `LLMUX_API_KEY`; the credential was added during the Task 6 follow-up. The local system service was not restarted; the user must run `sudo systemctl restart mindfs` to activate its API and periodic runner.
