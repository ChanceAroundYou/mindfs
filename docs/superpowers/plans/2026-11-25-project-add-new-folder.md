# 导入项目时新建文件夹

> Scope IDs: G-RD（claimed 2026-11-24T16:33Z）

Status: Active

## Goal

添加项目（加入本地目录 / 空白项目 / GitHub 导入）的路径浏览弹窗里，用户可以直接在当前浏览目录下新建文件夹，建完停留在新目录并预选，继续完成导入，不必先切到终端或文件管理器建目录。

## Implementation

- 后端：`usecase.CreateLocalDir`（`server/internal/api/usecase/local_dirs.go`）按绝对父路径 + 单段名称建目录，名称校验与 `CreateEntry` 一致（拒绝空、`.`、`..`、路径分隔符、`.mindfs`）；父目录不存在 → 404，同名已存在 → 409，无权限 → 403。新端点 `POST /api/local_dirs`（`server/internal/api/http_local_dirs.go`）挂在数据面，worker 放行（前缀不在控制面表内）。
- 前端：`LocalPanel` 面包屑行新增「新建文件夹」按钮，走 `useProjectDirectoryBrowser.handleLocalDirCreate`：`promptDialog` 取名 → `POST /api/local_dirs`（按当前节点 `appURL`）→ 成功后 `loadLocalDirs(新路径)` 并把 `selectedPath` 指向新目录，用户可直接点「添加 / 放置于此目录」。
- i18n：`projectAdd.newFolder` / `projectAdd.newFolderPrompt` / `projectAdd.newFolderFailed` / `projectAdd.newFolderExists`，两份 locale 齐备。

## Tests

- `server/internal/api/usecase/local_dirs_test.go`：成功建目录、非法名称拒绝、同名 409（`os.ErrExist`）、父目录缺失 404。
- `server/internal/api/http_local_dirs_test.go`：端点建目录 201 + 冲突 409 + 非法名称 400。
- `web/tests/project-add-new-folder.test.mjs`：按钮接线、POST 打当前节点、建后进入并预选、i18n 双份。

## Compatibility

新增端点与按钮，不改既有 `GET /api/local_dirs` 与 `POST /api/dirs` 行为；无数据迁移。
