# File view: new folder
> **Shipped 2026-11-07（v0.5.5-1115）** —— 实现提交已落在该 tag 内，产物已装本机 / WSL / fn 并版本逐字一致
> （本机待用户 `sudo systemctl restart mindfs`、pc-win 已装未运行）。本计划已移入 `docs/plans/README.md` 的历史表，
> 下方「待提交与 ship / 待 ship」是当时的记录，不是当前待办。

Status: Implemented 2026-11-04, 待提交与 ship.

## Goal

文件视图 ⋯ 菜单新增「新建文件夹」，与「创建空白文件」「上传到当前目录」并列，建在当前浏览目录。

## Implementation

- 后端：`usecase.CreateBlankFile` → `CreateEntry(rootID, dir, name, asDir)`（`server/internal/api/usecase/text_edit.go`）；`POST /api/file` body 加 `kind:"dir"`（`server/internal/api/http_text_edit.go`）。`asDir=true` 走 `os.Mkdir(0o755)`，冲突仍由 `os.ErrExist` → 409。新建路径加 `.mindfs` 护栏（AGENTS.md #12）。
- 前端：`handleCreateFolder` 沿 `handleCreateBlankFile` 链路（`useSessionListActions.ts`），请求用 `appURL("/api/file", params, getNodeIdForRoot(rootID))`（跨节点不打错机器）；prop 链 DefaultListView → DefaultListViewMenu → SessionMainContent → AppRootView；菜单按钮用 `fileMenuItemStyle`。
- i18n：`directory.createFolder` / `directory.folderNamePrompt` / `directory.createFolderFailed`，两份 locale。

## Tests

- `server/internal/api/usecase/create_entry_test.go`（3 cases）
- `web/tests/file-view-create-folder.test.mjs`（4 cases）

## Compatibility

`kind` 缺省 = 创建空白文件，零破坏；无数据迁移；`/api/file` 本就是数据面，worker 放行，role 表不变。
YAML group: G-KH.