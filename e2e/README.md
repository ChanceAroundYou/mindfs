# e2e/ · 前端运行时冒烟测试（Playwright + pytest）

给 `App.tsx` 这类巨文件的拆分当**安全网**：只断言「用户真的能看见 / 点得到」，
不断言实现细节 —— 拆分挪代码不会误报，真拆坏了才会红。

**刻意不进常规门禁**（`npm test` / `check-upstream`），单独命令跑。

## 跑法

```bash
# 完整跑（先按 /mindfs 前缀构建 dist，再编译后端、起服务、跑 10 个用例）
~/.hermes/hermes-agent/.venv/bin/python -m pytest e2e/ -v -p no:cacheprovider

# 迭代时复用已有 dist（快很多；dist 必须是 VITE_MIND_FS_BASE=/mindfs 构建的）
E2E_SKIP_BUILD=1 ~/.hermes/hermes-agent/.venv/bin/python -m pytest e2e/ -v -p no:cacheprovider

# 看夹具里的 print 输出
... -s
```

依赖：`~/.hermes/hermes-agent/.venv` 里的 Python + Playwright 1.61.0 + pytest 9.1.1，
以及 `~/.cache/ms-playwright/` 里的 Chromium。**不要用 `npx playwright`**（那是另一个缓存版本）。

## 隔离设计（不碰真实机器状态）

| 维度 | 做法 |
|---|---|
| 配置目录 | `XDG_CONFIG_HOME` 指向 pytest 临时目录 → 后端用一份全新账户表 / 项目表 / 会话库，不读也不写真实的 `~/.config/mindfs` |
| 测试账号 | `e2e_test` / `e2e-temp-7Kx9Qm2!`（`TEST_USER`），只在隔离配置目录里由 `POST /api/users` 创建，随临时目录一起销毁，与任何真实账户无关 |
| 端口 | 随机空闲端口。**绝不绑 7331** —— 那是本机真实 mindfs 服务 |
| 前端产物 | 后端用 `MINDFS_STATIC_DIR` 直接服务 `web/dist`，跑的就是生产产物 |
| 部署前缀 | 后端编译期注入 `-X mindfs/internal/deploy.Prefix=/mindfs`，与 dist 的 `VITE_MIND_FS_BASE=/mindfs` 一致，否则 `/mindfs/assets/...` 全部 404 |

**为什么不用 vite dev server**：本机 7331 被真实服务占着，而 `web/vite.config.ts` 的代理目标硬编码 7331。
走「后端直接服务 dist」既不用改主配置、也不碰真实服务。

**多账户分区**：前端所有数据面请求都会附 `user=<当前账户 id>`
（`web/src/services/net/base.ts` 的 `withAccountUser`）。所以用 API 造数据时必须打同一个分区
（`user_query` fixture），否则界面上什么都看不到。

## 覆盖范围（10 个用例）

| 用例 | 钉住什么 |
|---|---|
| `test_app_boots_to_workspace` | 登录后主工作区渲染出来（不是白屏） |
| `test_main_view_switcher_present` | 主视图切换器存在 |
| `test_project_tree_lists_seeded_files` | 项目列表 + 临时项目的目录/文件都在树里 |
| `test_project_tabs_present` | 项目面板页签（Files / git / Worktrees / Related files） |
| `test_open_file_renders_content` | 点文件 → 查看器真的把内容取回来渲染 |
| `test_view_switcher_switches_to_chat` | 切到 Chat → 会话输入框出现 |
| `test_view_switcher_switches_to_board` | 切到 Board → 看板挂载且没崩 |
| `test_view_switcher_round_trips` | Workspace → Files → Workspace 来回切 |
| `test_session_sidebar_present` | 会话侧栏 + 搜索按钮 |
| `test_sidebar_collapse_round_trip` | 左侧栏折叠/展开来回切 |

选择器口径：优先 `data-onboarding`（引导浮层锚点，属对外可观测契约）和 aria-label，
避开 class 名与内部 DOM 结构。

**刻意不做「每个用例重载页面」**：用例之间改用应用内导航复位（见下）。

## 已知上游问题（非本分支引入）

**视图交互 + 页面重载累积 8~9 次后，Go 后端整体失去响应**（连 `/api/auth/status` 都超时）。

- 复现：循环「`page.goto` + 点 Board tab」，第 9 次左右后端僵死；直接 HTTP 循环打
  `/api/tasks/overview` 则 20 次全 200 —— 是**视图交互 + 重载**的组合触发。
- 归属：**上游既存 bug**。`git diff --name-only f0f6f85..HEAD` 显示 task-35 只动
  `web/src`、`web/tests`、`web/package.json`、文档、`Makefile`，**零后端文件**。
- 影响：本套件因此**不做 per-test 重载**，改用应用内导航复位（`_show(page, name)`）。
  若将来要加「每用例全新页面」的用例，需先修这个后端问题。

## 维护提示

- 新增用例：优先用 `data-onboarding` / aria-label 选择器；需要新数据就加 fixture。
- 夹具改动要同时考虑「会不会引入页面重载」—— 那是撞上已知上游 bug 的最快路径。
- 本目录**不登记进 `check-upstream`**（不是 fork 定制，是本地验证工具），
  但已登记进 G-BA 门禁的 `files`，避免 delta 覆盖检查变红。
