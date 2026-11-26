# 边缘端节点表：主控置顶 · 统一重复的 pc 行 · 顺序与颜色跨端持久 · Windows 客户端图标

> Scope IDs: G-SA / G-SB（claimed 2026-11-26T12:42Z）

Plan 2026-11-26。

## 背景与用户诉求

用户在实际使用的边缘端（Windows，`instance_id=pc`，`127.0.0.1:7332/mindfs`）发现四件事：

1. 「管理节点」第一行是本机 `pc`（`http://127.0.0.1:7332/mindfs`），而主控 `home`
   （`https://home.xiaokubao.space/mindfs`）应当固定在最上面。
2. 列表里有两条都叫 `pc` 的节点（`127.0.0.1:7332/mindfs` 与
   `https://pc.xiaokubao.space/mindfs-win`），无法区分。用户裁定：**不保留两条，统一成一条**；
   旧的 pc 已由这台边缘替代，统一后的入口沿用旧直连网址 `/mindfs-win`。
3. 各端顺序与颜色必须一致，用户改动要持久化并同步到另一端。重排交互用户已选
   **上移/下移按钮，首行固定**。
4. Windows 边缘客户端没有图标，要把图标资源打进打包产物并确认生效。

## 取证（只读，生产 7331 全程未动）

- 边缘 `/mindfs-win/api/nodes` 与主控 `/api/nodes` 返回同一份控制面列表：`home`、`pc-wsl`、
  `fn`、`pc`（id `685a…`，`remote:true`，无 `instanceId`）。
- 边缘 `/api/node-info` = `{id:"pc", role:"control"}`；主控 = `{id:"home", role:"control"}`。
- 前端 `normalizeAndDedup` 在列表里找不到「本机」时**无条件前插**一条合成 self 行
  （id/name 取本机自报身份、URL 取当前 origin），共享表里的 `pc`（id `685a…`）却因没有
  `instanceId` 无法并入 —— 两条 `pc` 由此而来，主控也被挤到第二行。
- 该合成行还会随整表 PUT 回主控：服务端 `NameConflicts` 拒绝「新出现同名不同 id」，
  于是**边控端所有节点表写入都静默失败**（改名/改色/学到 instanceId 都不落库），
  这正是「顺序/颜色不跨端同步」的直接原因。
- 顺序本身也没有持久字段：服务端 `List()` 原样返回数组顺序，`controlsync` 的 `Apply`
  只按 key 保留位置，顺序纯属各端本地排列。
- Windows 产物：`scripts/build-all.sh` 用 `-H=windowsgui` 编 `mindfs.exe`，托盘图标走
  `go:embed tray.ico`；exe 本身没有 PE 图标资源（无 `.syso`），NSIS 安装器
  （`scripts/windows-installer/mindfs.nsi`）也没有 `MUI_ICON`。

## 方案

### 1. 统一 self 行（前端 `nodeRegistry`）

- 合成 self 行带上 `instanceId = 本机自报 id`（边缘即 `pc`）。
- 合成行改为**追加**（而不是前插），并让去重链看到它：一旦共享表里某行学到的
  `instanceId` 等于本机身份，两者按 instance 合并成一行，保留共享行的 id/URL/颜色
  （即 `/mindfs-win` 旧直连网址）。
- 判断「是不是本机」改为 connection-aware（`id` 命中或 `instanceId` 命中），
  面板的「本机」标记、URL 只读、删除保护、路由都跟着它走。
- 合成行只在「控制面表为空」时才允许写回服务端；表里已有其它行时从 PUT 载荷剔除，
  彻底解除同名冲突。
- 编辑/删除/路由的 self 判定走新 helper；路由到 self 时仍用设备本地基址
  （`window.location.origin` 推导），因此 unified 行保留旧直连网址展示，但请求走本机，
  公网链路挂了也不影响本机内的操作。
- 旧的 active id（`pc`）在合并后自动迁到统一行的 id（`685a…`）。

### 2. 顺序跨端：`NodeConnection.Order`

- 服务端 `nodes.NodeConnection` 增加 `Order int`；`List()` 按 `Order` 升序稳定排序，
  `Order==0` 的旧数据排在后面并保持原相对顺序。
- `PUT /api/nodes` 按请求数组下标赋 `Order=i+1`，并把主控自己的行强制移到第一位
  （`h.NodeID`）——服务端保证「主控置顶」，不依赖任何客户端的自觉。
- `GET /api/nodes` 返回前同样把主控行排到第一位；`Upsert`/`SetRelay` 新行取
  `max(Order)+1`。
- 顺序随 control-sync 的 nodes 域整条记录自然同步（无需改 domain 代码）。
- 前端面板：第 1 行固定（主控），其余行提供上移/下移按钮；`moveNode` 交换相邻行后
  走既有 `setNodes`（整表 PUT，服务端按新下标写 Order）。

### 3. 颜色

颜色本来就在同一张表里；写入失败的根因是第 1 条的同名冲突。统一之后读写恢复正常，
无需额外机制，顺序/颜色一起跨端持久。

### 4. Windows 图标

- 由 `web/public/pwa-512.png` 生成多尺寸 `cli/cmd/assets/app.ico`（16/32/48/256）。
- 用 `rsrc` 生成 `cli/cmd/rsrc_windows_{amd64,arm64}.syso` 并提交；
  `scripts/build-all.sh` 交叉编译 Windows 时 Go 自动链接，exe/快捷方式/任务栏获得图标。
- NSIS 安装器在拿到 `-DAPP_ICON` 时定义 `MUI_ICON` / `MUI_UNICON`（由
  `scripts/build-windows-installer.sh` 传入），安装向导与卸载器也显示同一图标；
  不传时保持 NSIS 默认，手动构建不因缺参数失败。
- 定向测试：图标文件存在且含必需尺寸、`.syso` 存在且架构匹配、`.nsi` 用 `APP_ICON`
  定义两个图标、构建脚本确实传参；另以 `llvm-readobj --coff-resources` / PE 解析
  核对实际产物含 `RT_ICON` / `RT_GROUP_ICON`（exe 与 setup-stub 均已验）。

## 验证

- Go：`nodes` store 的顺序/赋值用例、`api` 的 GET 置顶与 PUT 赋值用例；`go test ./server/...`。
- Web：节点表合成/合并、PUT 载荷剔除、上移下移与首行固定、self 路由走本机；`tsc -b` + 全量 vitest。
- 图标：构建 `windows/amd64` exe + 安装器，解析 PE 资源确认 `RT_ICON`/`RT_GROUP_ICON`。
- `make check-upstream`：两组新定制登记齐全。
- 真实端到端用隔离实例（`XDG_CONFIG_HOME` + 新端口），不碰生产 7331、不重启任何节点。

## 边界

只本地提交；不 push、不部署、不重启任何节点。生产节点表里两条 `pc` 的统一由新前端 +
自动 learned `instanceId` 完成，不手工删库。