# 主视图切换规则设计（侧栏底部四态切换）

> 状态：**待批准**（批准后才动代码）
> 目标：主区内容由**唯一一个显式状态**决定，切换只发生在用户点击时，杜绝"随手一跳"。

## 一、现状（为什么会乱跳）

主区内容目前由**两套并行机制**决定，且都不是显式的：

| 机制 | 内容 | 谁写 | 谁读 |
|---|---|---|---|
| `mainContentViewByRoot` | 每项目一个 `task-kanban \| file-browser` | 只有用户点主区菜单里的「当前视图」 | `currentMainContentView` |
| `defaultMainContentView` | 全局兜底（**默认 `task-kanban`**） | 同上 | 该项目无记录时顶上 |
| `mainViewPreferenceByRootRef` | `session \| file \| directory \| git-diff` | 打开文件/目录/会话/git 时写 | 几乎没有消费者（仅会话恢复判断读一处） |

后果：
1. **跳到看板**：某项目在 `mainContentViewByRoot` 里没有记录（清缓存、换账户、新项目、根切换清理）时，主区回落到默认值 `task-kanban`，于是"点左侧目录 → 主区变成看板"。
2. **两处都能切**：主区右上角菜单里还藏着「当前视图」切换项，与侧栏（将要新增的）切换器重复，等于两个真相源。
3. **`mainViewPreferenceByRootRef` 是死代码**：写了没人读，容易误导后续修改。
4. 工作台（跨项目总面板）挂在「没有打开项目」这个状态上，而项目会被自动打开，导致它**没有入口**。

## 二、新规则（单一状态机）

主区内容 = **一个全局状态** `mainView ∈ { workspace, board, files, chat }`，持久化到 `localStorage`（键 `mindfs-main-view`），**与项目无关**（不再按项目记忆）。

| 模式 | 主区显示 | 需要项目？ |
|---|---|---|
| `workspace` | 跨项目工作台（等待你 / 运行中 / 快速发起 / 归档） | 否 |
| `board` | 当前项目的任务看板 | 是 |
| `files` | 当前项目的目录/文件列表（点文件后 = 文件内容） | 是 |
| `chat` | 当前选中的会话；未选会话时显示空态提示 | 是 |

### 切换规则（穷举）

| # | 用户动作 | 模式变化 |
|---|---|---|
| R1 | 点侧栏底部切换器 | 切到点的那个模式（唯一的手动入口） |
| R2 | 点左树里的**文件** | → `files`，并打开该文件 |
| R3 | 点左树里的**目录 / 项目根** | → `files`，并列出该目录 |
| R4 | 点右侧会话 / 任务卡上的「跳会话」/ 会话 chip | → `chat`，并打开该会话 |
| R5 | 在 chat 模式下打开文件/目录，或点切换器其它档 | 离开 chat（分别按 R2/R3/R1） |
| R6 | 在工作台点卡片的项目名 / 「跳项目」 | 切到该项目 → `board` |
| R7 | 在 `workspace` 模式下点左树里的项目 | → `files`（左树即文件语义，与 R3 一致） |
| R8 | 程序化切项目（会话/任务跳转、自动选根、URL 恢复） | **模式不变** |
| R9 | 新建任务 / 任务完成 / 收到 WS 广播 | **模式不变**（任务详情面板是浮层，不是模式） |
| R10 | 刷新页面 / 冷启动 | 恢复持久化的模式；`workspace` 之外的三种模式若无当前项目，先落到 `files` 并等自动选根 |

**红线：除 R1–R7 外，任何代码路径都不得改 `mainView`。** 尤其禁止：onboarding 引导、自动选根、缓存清理、账户切换时写这个状态。

**实现约定**：`actionHandlers.open`（打开文件）总是切 `files`；`open_dir` 默认**保持模式**，只有左树点击带 `switchToFiles: true` 时才切 —— 这条把"程序化打开目录"从切换路径里摘出去，是"不再随手跳"的关键。

### 边界

- **首次运行默认值**：`board`（与今天首次进入的行为一致，不制造"升级后界面变了"的意外）。
- **onboarding 引导**：仍可用一个**瞬态覆盖**（`onboardingOverride`）把主区临时钉在 board 上给新手看，但**结束时不写** `mainView`，恢复用户原模式。
- **移动端**：切换器同样固定在左栏底部；点完自动收左栏（与现在点文件的行为一致）。
- **多节点/多账户**：`mainView` 是**本地 UI 偏好**，不随账户/节点分区（与主题色等偏好同级）。跨节点打开项目仍按 R8/R7 走。

## 三、改动点（实现清单）

1. `App.tsx`：删除 `mainContentViewByRoot` / `defaultMainContentView` / `setMainContentViewForRoot` / `handleMainContentViewChange` 的"按项目记忆"语义，替换为单一 `mainView` + `onboardingOverride`。
2. `App.tsx`：`currentMainContentView`（喂给 `DefaultListView` 的 `task-kanban|file-browser`）改为派生值；`workspaceOpen = mainView === "workspace"`。
3. `App.tsx`：按 R2/R3/R4/R5/R6/R7 在打开流程里显式设置模式；删掉 `mainViewPreferenceByRootRef` 这套死代码（或收敛为"上一次非 chat 模式"的单一 ref）。
4. `DefaultListView.tsx`：移除菜单里的「当前视图」切换项（避免第二个真相源），改为只读展示当前模式。
5. 新增 `web/src/components/MainViewSwitcher.tsx`：四态分段控件（图标+短标签，激活态用节点主题色），挂在**左侧栏底部**（`FileTree` 下方的固定条）。
6. i18n：`view.workspace / view.board / view.files / view.chat`（中英）。
7. 测试：重写 `web/tests/main-content-view-memory.test.mjs` 为新契约（单一状态、持久化键、R1–R10 的源码守卫），新增 `web/tests/main-view-switcher.test.mjs`（切换器存在、四态、无第二真相源）。

## 四、验收

- 侧栏底部出现四态切换器，点击即切，刷新后保持。
- 在 `files` 模式点任意目录/文件，**不会再跳到看板**。
- 在工作台点卡片跳项目 → 落到该项目的看板；在 `files` 模式换项目 → 仍是 files。
- 引导流程走完后主区回到用户之前选的模式。
- `node --test tests/*.test.mjs` 全绿；`tsc --noEmit` 无错。
