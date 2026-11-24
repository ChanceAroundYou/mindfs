# 移动端工作流两级弹层避让

> Scope IDs: G-QP（claimed 2026-11-23T20:10Z；原 G-QL 已由 main 的「过期任务提示与 root 错误可诊断性修复」占用，撞号后改号）

## 背景

用户报障：移动端「新建任务」面板与「任务详情」面板里，工作流选择器的两级弹出被遮挡，
无法正常显示和操作，要求正确设置避让逻辑。截图见
`.mindfs/upload/2026-11-23/Screenshot_2026-11-23-22-32-40-04_71b1e056e543004d5f4807949b0f8efa.jpg`。

两个入口共用 `web/src/components/task/WorkflowSelector.tsx`，一处修复覆盖两处：

- 新建任务：`web/src/components/app/TaskInlineEditPanel.tsx:281`
- 任务详情：`web/src/components/task/ActivityInput.tsx:175`

「两级」是一个 portal 浮层里的两列：一级三态（右列 168px）+ 展开时插在左列的二级工作流列表。

## 根因（代码取证）

定位全部在 `web/src/components/task/useWorkflowSelectorMenu.ts`：

1. **永远向上弹、不量菜单高度**：`bottom: max(8, innerHeight - rect.top + 8)`，没有 `top` 夹取。
   两级展开后约 272px 高，可视视口被软键盘压矮时顶部直接跑出屏幕。
2. **右缘钉死、左缘不夹**：`right: max(8, innerWidth - rect.right)`，二级列向左生长，
   菜单左边缘 = `rect.right - 菜单宽`，窄屏上可成负值；`maxWidth: calc(100vw - 16px)`
   只压宽度不重定位。
3. **用 layout 视口**：`window.innerHeight/innerWidth` 在软键盘弹起时不缩小（`PanelShell`
   正是为此自算 `--panel-vh`），菜单可能画到键盘后面；且只订阅 `resize`，没有 `scroll`。

portal 到 body 后没有祖先能裁它，所以「被遮挡」= 跑出可视视口（含键盘上方那条带）或越过左右边界。

## 方案

改用仓库已有的量测式锚定，不新写第三套实现：

- `web/src/shared/anchoredMenuPosition.ts`：加可选 `prefer?: "below" | "above"`（默认保持现值），
  返回值增加 `maxHeight` / `maxWidth`；两侧都放不下时落在空间更大的一侧并内部滚动，
  而不是溢出屏幕。
- `web/src/hooks/useAnchoredMenuPosition.ts`：透传 `prefer`；订阅 `visualViewport` 的 scroll；
  对菜单挂 `ResizeObserver`（二级列表异步加载会改变高度）；位置去重补上高度/宽度字段。
- `web/src/components/task/useWorkflowSelectorMenu.ts`：删除自算 anchor，委托共享 hook，
  `prefer: "above"`（输入框在面板底部，沿用向上弹的既有观感）、`deps: [manualOpen]`；
  `menuStyle` 改为 `top/left/maxHeight/maxWidth` + `overflowY: auto`，保留 `visibility: hidden`
  的一帧隐藏，保留 `pointerdown` / `Escape` 关闭。
- `web/src/components/task/WorkflowSelector.tsx`：把 `manualOpen` 传给 hook；二级列宽度由
  `min(232px, calc(100vw - 200px))` 改成 `flex: 1 1 232px; minWidth: 0; maxWidth: 232`，
  一级列补 `flexShrink: 0`，宽度交给实际可用空间决定。

不做：改成一列阶梯/底部抽屉（用户要的是两级弹出的避让，不是换结构）、新写 workflow 专用的
measurePlacement（会成为第三份实现）、动 `--panel-vh` 或 z-index。

## 验证

- `web/tests/anchored-menu-position.test.mjs`：新增 5 条纯函数用例（prefer above 正常、
  above 放不下翻到 below、两侧都不够时取更大一侧并夹高度、超宽时夹左缘与宽度、缺省 prefer
  与改动前逐字段一致）；变异逐条打红。
- `web/tests/workflow-selector.test.mjs`：新增 2 条源码契约（hook 必须委托共享 hook 且不再出现
  `window.innerHeight/innerWidth`；菜单样式必须用 top/left/maxHeight 且列宽用 flex）。
- `e2e/test_workflow_selector_menu.py`：375×667 与 360×640，两个面板各跑一遍，断言菜单矩形
  完全落在 `visualViewport` 内、一级三项与二级条目 `elementFromPoint` 命中、二级列表可滚动、
  选中后菜单关闭并出现状态圆点。
- 门禁：`npx tsc -b --noEmit`、`npx vitest run`、`make build-web`、`make check-upstream`。

## 实施结果（2026-11-24）

产品代码与测试已落盘；组号原为 `G-QL`，合并 main 时发现该号已被「过期任务提示与 root
错误可诊断性修复」占用，按 `scripts/scope-id next` 的输出改为 `G-QP` 并登记。

- 定向 Vitest：`2 files / 22 tests passed`；全量 Vitest：`272 files / 953 passed / 6 skipped`。
- `npx tsc --noEmit -p tsconfig.json` clean；`make build-web` 成功。
- 变异验证 M1–M5（默认 prefer 改 above、删翻面分支、`maxHeight` 返回 Infinity、去高度夹取、
  去水平夹取）各自先打红对应用例再还原；首版 M1 没红，是因为兼容用例的锚点上方本就放不下，
  已补「两侧都有空间时默认向下」用例后才确定性打红。
- 真浏览器：`env E2E_SKIP_BUILD=1 .venv/bin/python -m pytest e2e/test_workflow_selector_menu.py -v
  -p no:cacheprovider` → `1 passed in 82.82s`。两条入口 × 两个视口的全流程放在一个用例里：
  `authed_page` 是会话级 Playwright 夹具，拆成两个 pytest 用例在同一进程连跑时，第二个用例会在
  fixture 交接处挂住（各自单独进程跑均通过）；单用例顺序覆盖四个组合稳定通过。
- 提交与合并：产品代码与测试提交为 `0666db09`（10 文件，+579/−51），随后 `cdc97420` 合入 main；
  main 上 `make check-upstream` 通过（`2682 个 delta 文件被 394 组完整覆盖`），
  `go run ./scripts/scope-id check` 报 394 个组号唯一。
- 部署：随四端 `v0.5.5-1400-12-ge2be3c18` 发布（`bash scripts/deploy-all.sh` 退出 0）。
  本机 7331 服务的 `assets/index-DsKaVt7P.js` 已含本条修复
  （bundle 内可 grep 到 `prefer:"above"` 与 `minWidth:0,maxWidth:232`）。
- 仍未做：本机运行进程是旧二进制（`v0.5.5-1300-66-g6d1b174e`，待用户
  `sudo systemctl restart mindfs`）；移动端真机点验未做。静态资源不 embed，
  前端修复由旧进程从安装目录直接服务，刷新客户端即可生效。
- 2026-11-24 11:13 按用户要求重建纯前端并安装：用 `git archive e2be3c18`（已部署提交，
  刻意不含 main 上其他会话尚未部署的前端提交）在 `/tmp` 快照里 `make build-web`，
  产物 `cp -R dist/. ~/.local/share/mindfs/web/`（未重编 Go 二进制、未重启任何节点）。
  - 新入口 `assets/index-BZpBxgmt.js`，bundle 内 `prefer:"above"` 与
    `minWidth:0,maxWidth:232` 各命中 1 次；
  - `service-worker.js` 的 `SHELL_CACHE` 从 `mindfs-shell-268695349d08` 变为
    `mindfs-shell-926565d4c62f`，客户端重装 SW 后会清掉旧 shell 缓存；
  - 7331 已服务新入口与新的 `ui-manifest.json`（`build_version` / `min_server_build`
    均为 `v0.5.5-1400-12-ge2be3c18`，与运行中二进制一致）。

## 范围

只改前端浮层定位与列宽，不动面板结构、后端与配置；部署需 `make install`（静态资源不 embed）。