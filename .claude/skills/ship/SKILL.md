---
name: ship
description: 代码变更后的收尾闭环：门禁测试 → 提交推送 → 本机编译 → 推送产物到 WSL 安装重启 → 两端对账。触发词：收尾/部署/发布/上线/deploy/推上去/更新过去/更新 wsl/两侧编译
---

# Ship — 代码变更收尾闭环

> 把 CLAUDE.md「部署与提交闭环」那段散文固化成一条命令。**默认直接跑 `bash scripts/deploy-all.sh`**，别再手搓一串 make/ssh。

## 一句话

```bash
bash scripts/deploy-all.sh                  # 提交已在 main：走推送 + 双侧部署
bash scripts/deploy-all.sh -m "fix: 说明"   # 顺便把工作区改动提交了
bash scripts/deploy-all.sh --no-restart     # 只推产物+安装，不重启 WSL
```

## 铁律（违反=部署了坏代码）

1. **门禁不过不推**：`go test` / `tsc --noEmit` / `web npm test` 三项全绿才允许 push。脚本已内置，不要 `--skip`。
   **web 测试必须带 hook，别裸跑 `node --test`**：源码守卫测试读的是**逻辑路径**，靠 `source-map-hook.mjs` 把
   `fs.readFileSync` 重定向到移动/拆分后的物理文件。裸跑 `node --test tests/*.test.mjs` 会**假红 51 个**
   （全是不存在的旧路径），`npm test` 才是 209/210（1 skip）。跑之前别被这个骗了。
2. **本机 restart 只能用户做**。本机是 **system** 单元（`/etc/systemd/system/mindfs.service`），`sudo systemctl restart mindfs` 会杀掉所有托管的 claude 子进程 —— **Agent 不得代劳**。脚本只装好二进制，最后把命令打出来给用户。
3. **WSL 侧可以直接重启**。`ssh wsl 'systemctl --user restart mindfs'`，user 单元，无条件，运行与否皆可。
4. **WSL 上没有源码库，永远别想在那边编译**（2026-10-06 起）。`~/projects/mindfs` 只剩 `.mindfs/` 数据目录 —— 没有 `.git`、没有 `Makefile`、没有源码。那边只接收本机构建好的产物；去 WSL 上 `make build` / `git pull` = 走错路了。
5. **不在 main 上不部署**。脚本会直接拒绝 —— 先走 `/worktree-finish` 合回 main。
6. **对账要真的看**：版本号是**逐字比对**（推过去的就是本机那个二进制文件，必须完全相同，脚本不一致即停）。bundle **哈希不同是正常的**（每次构建注入随机戳 `buildStamp()`，见 `vite.config.ts:30`），**字节数相同才说明内容一致**。

## 脚本做了什么

| 步 | 动作 | 失败即停 |
|---|---|---|
| 0 | 查 `MERGE_HEAD`、确认在 `main` | ✓ 半截合并/错分支直接拒 |
| 1 | （可选）`git add` 指定目录 + commit | ✓ |
| 2 | `go test ./...` / `tsc --noEmit` / `npm test`（= 带 `--import` 两个 hook，见下） | ✓ 日志留 `/tmp/mindfs-*.log` |
| 3 | `git push origin main` | ✓ |
| 4 | 本机 `make build && make install` | ✓ |
| 5 | `tar` 打包产物 → `ssh wsl` 解包 → `install` → （默认）重启 → 回报版本号 | ✓ 版本号不一致直接停 |
| 6 | 核对本机 `/` 引用的 bundle 就是刚装进去的那版 | 只警告不中断 |

**第 5 步推的是什么**：`mindfs` 二进制 + `agents.json` + `task_template.json` —— `make install` 会装的四样里**去掉 `web/dist`**。打一条 tar 流走 ssh，落在 WSL 的 `~/.mindfs-deploy/` 暂存目录，装完即删。

> **不推 `web/dist`**（2026-10-07 起）：WSL 是纯 worker 节点，按设计不服务静态资源（`GET /` 是 403），前端只装在本机。曾推过一份是留给「角色翻成 control」的保险，那个假设已不成立。

## 各机器别叫错

| 代号 | 实际是 | 单元 | 监听 | 源码库 | 脚本怎么处理 |
|---|---|---|---|---|---|
| **本机** | Proxmox VM，Ubuntu 26.04 | **system** | `127.0.0.1:7331` | 有（`~/projects/mindfs`） | 编译安装，**不重启**（留给用户） |
| **wsl** | PC 上的 WSL，Ubuntu 24.04 | **user** | `0.0.0.0:7331` | **无** | 收产物 + 安装 + 直接重启 |

ssh 别名是 `wsl`（→ `pc.xiaokubao.space`，user `nnb`）。用 `MINDFS_WSL_HOST=... ` 可覆盖。

## 走完要报什么

别只说「部署完成」。要给出**可核对的事实**：

- 两端各自的版本号（**必须逐字相同**；脚本已经把不一致当失败处理）
- 门禁三项的实际通过数（web 测试是 `N/N`）
- 本机那条待用户执行的 `sudo systemctl restart mindfs`

## 常见后续

- **纯前端改动**：`make build-web && make install` 即可，dist 由静态服务直接生效，**本机刷新页面就行**。脚本走全套是安全的，只是多花几分钟。
- **worktree 里的改动**：`/worktree-finish` 合回 main → 再跑本脚本。
- **只想重新部署当前 main**：直接跑，不带 `-m`。
- **要去 WSL 上查东西**：会话库、项目数据都还在（`~/.config/mindfs/`、各项目的 `.mindfs/`），但**没有源码** —— `git log`、`go build`、`make` 在那边都不存在，别去找。
- **脚本卡在某步**：日志都在 `/tmp/mindfs-go-test.log`、`/tmp/mindfs-tsc.log`、`/tmp/mindfs-web-test.log`、`/tmp/mindfs-build.log`，直接看，不要重跑一遍盲猜。
