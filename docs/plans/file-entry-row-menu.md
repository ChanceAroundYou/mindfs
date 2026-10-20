# 文件浏览器「逐条三点操作菜单」

**状态**：Implemented（2026-10-20），待提交与 ship。

## 目标

主区「文件」页的每一行（文件与文件夹 alike）最右侧给出三点按钮，展开应用内菜单：
删除 / 重命名 / 移动。复用系统内既有控件与面板，不新造、不用原生菜单。

## 现状与结论

- 后端**零改动**：`POST /api/file/operation`（`server/internal/api/usecase/text_edit.go:24`）
  已经支持对**任意相对项目根的路径**做 delete / rename / move。
- 前端**已有**可复用的操作层：`FileOperationItems`（菜单条目）、`useFileOperationActions`
  （删除走 `confirmDialog`、重命名走 `promptDialog`）、`MoveFilePopover`（目录浏览面板）、
  `MoreMenuButton`（系统内既有的三点按钮）。缺的只是**入口**。
- 路径语义天然对齐：`FileEntry.path` 由后端 `fs.ListEntries` 生成，就是相对项目根的路径
  （`server/internal/fs/fs.go:43`），与 `/api/file/operation` 的 `path` 一致 → 逐条复用零改造。

## 落地

| 文件 | 作用 |
|---|---|
| `web/src/components/file/FileEntryMenu.tsx`（新） | 逐条三点菜单：复用 `MoreMenuButton` / `FileOperationItems` / `MoveFilePopover`；弹层 portal 到 body + `position: fixed` |
| `web/src/components/file/FileEntryRow.tsx`（新） | 列表行：图标 + 名称 + 紧凑元信息 + 最右侧三点（行 markup 从 `DefaultListView` 抽出） |
| `web/src/components/file/DefaultListView.tsx` | 行渲染改为 `<FileEntryRow>`（文件变短，避开 500 硬线） |
| `web/src/shared/anchoredMenuPosition.ts`（新） | 锚定定位的**纯函数**（放下方 / 放不下翻上方 / 夹进视口 / 右边缘对齐） |
| `web/src/hooks/useAnchoredMenuPosition.ts`（新） | 定位 hook：测量 + 视口变化重算 |
| `web/src/components/common/useSelect.ts` / `Select.tsx` | 改为调用共享定位 hook（**行为不变**，去重而非新功能） |
| `web/src/i18n/locales/{zh-CN,en-US}-projects.ts` | 新增 `fileOperation.menuFor`（aria-label 带文件名），双语同步 |

### 两个必须处理的技术点

1. **弹层被裁**：列表容器是 `overflow:auto`，行上还有 `transform: translateZ(0)`
   （会给 `position:fixed` 造包含块）→ 菜单必须 portal 到 body 并用视口坐标定位。
   仓库里已有这套做法（`Select` 的注释写明同一原因），定位算法抽出来共用。
2. **点三点误触发行点击**：行点击 = 打开文件 / 进入目录。三点按钮包一层
   `stopPropagation`。

## 范围外

- 左栏项目树（跨节点，需 node 路由）、文件预览页（已有三点菜单）不动。
- 不做下载 / 复制路径 / 新建文件夹。

## 验证

- `cd web && npm test` → 182 个测试文件全绿（含新增 `anchored-menu-position`、
  `file-entry-row-menu` 两个契约测试）。
- `cd web && npm run typecheck` → 无错。
- `make build-web` → 构建通过；`make test` → Go 侧无改动、全绿。
- 手工：主区「文件」页逐条点三点做 删除 / 重命名 / 移动；确认点三点不打开文件；
  靠近视口底部时菜单翻到上方且不被裁。

## 提交与登记

- `Scope: G-JO`，登记进 `docs/upstream-customizations.yaml`（新组 G-JO）。
