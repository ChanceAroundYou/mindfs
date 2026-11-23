# Scope 组号治理：脚本申请、门禁拦截、撞号裁决

> Scope IDs: G-QD（claimed 2026-11-23T07:24Z）

**状态**：Active（2026-11-23）

**目标**：`Scope: G-XX` 这类定制组号不再靠手工维护。取号走脚本、写错被门禁拦住、
真撞上了有一条可复现的裁决路径。

## 背景：三次撞号事故

组号是手写字符串，规则写在 `docs/agent/rules/common/commit-and-customization.md`：
一组定制一个号，提交信息标 `Scope: G-X`，清单里登记。

问题出在**取号方式**：

1. 分支 A 打开清单 → 最大号是 `G-PV` → 用 `G-PW`。
2. 分支 B 同时打开同一份清单（还没合并 A）→ 也看到 `G-PV` → 也用 `G-PW`。
3. 合并时两边改的是清单**末尾**，git 三路合并可能干净通过，也可能只有一处冲突；
   无论哪种，`check-upstream` 的旧七条断言都不检查重复 —— 它只检查「每个 delta 文件
   被某组覆盖」，两组同名等于一组，照样全绿。
4. 事后读 `git log --grep 'Scope: G-PW'` 会看到两组毫不相干的定制。

2026-11-18（G-MG 撞 R1 T5 的事件账本）、2026-11-20（G-NP 撞 G-NS）、
2026-11-23（计划里写的 G-PW / G-PX / G-PY，合回 main 时实际登记为
G-PZ / G-QA / G-QB）都是这条路。前两次只留了 `# 撞号说明` 注释，
**没有机制**，第三次照旧发生。

## 三个根因

| 根因 | 表现 | 处置 |
|---|---|---|
| 取号看本地清单 | 两个分支都从「本地最大号 + 1」出发，必然撞 | `scope-id next/claim` 读 `origin/main` ∪ 本地的并集 |
| 门禁不检查唯一性 | 撞号后合并干净、门禁全绿 | 断言 6：清单内组号不得重复 |
| 计划与清单脱节 | 计划里写了号，清单里没登记，等于没人保护 | 断言 7：计划 Scope 行里的号必须已在清单 |

另外两个诱因：**回填空洞**（有人看到 `G-AB` 空着就填进去，另一个分支也看到了同一个洞）
与 **三字母孤岛**（`G-PDF` / `G-WIN` / `G-WSX` 是历史遗留，不该把新号一起拖进三字母段）。

## 设计

### 取号规则

- 两字母段 `G-AA`…`G-ZZ` 取**最大号 + 1**，绝不回填空洞。
- 两字母段用尽后进入三字母段，起于 `G-AAA`，并从三字母最大号往上续（跳过历史孤岛）。
- 取号依据 = `origin/main` 的清单 ∪ 本地清单。本地已提交但未推送的号也算占位，
  重跑不会重复发号。

算术集中在 `scripts/internal/scopeid`（`Ordinal` / `Format` / `NextIDs` / `Duplicates`），
门禁与命令行共用同一份实现 —— 两边对「什么算一个号」有不同理解，就等于没有规则。

### 三条命令

```bash
scope-id next [--count N]             # 打印接下来 N 个空号
scope-id claim <plan.md> [--count N]  # 取号 + 清单占位 + 计划头部写声明
scope-id check                        # 与门禁同一套检查，可单独跑
scope-id resolve --branch <name>      # 撞号裁决
```

`claim` 写入的占位组标题是「WIP — 待填写」，会让断言 3/5 变红 —— 刻意的：
占位必须被填成真条目，否则该组等于没有保护。

`resolve` 的规则沿用既有先例（G-MG→G-MN、G-NP→G-NS、G-PW/PX/PY→G-PZ/QA/QB）：

- 按 `git log -S 'id: G-XX'` 找两边的引入提交，**先引入者保留**；
- 站在后来者的分支上，改号；站在先引入者的分支上，拒绝并告诉你去哪边改；
- 提交信息里的历史 `Scope:` **一律不改写**（历史是事实，不是索引）；
- 清单里插一条 `# 撞号说明`，记旧号、新号、两边引入提交与日期；
- 计划正文里的历史叙述不动，只改机器拥有的 `> Scope IDs:` 头部行。

### 版本号

`make tag` 原先只做三件事（push main、打 tag、push tag），没有任何校验：
脏工作区会打出带未提交代码的 tag；`TAG` 写错形状 git 照样接受；重复 tag 直接失败；
发布说明首行与 `TAG` 不一致要到 `make release` 才报。现在前置到 `tag` 目标里。

## 交付清单

| 项 | 落点 |
|---|---|
| 组号算术与扫描 | `scripts/internal/scopeid/` |
| 门禁断言 6/7 | `scripts/check-upstream/main.go` |
| 门禁接入 ship | `scripts/deploy-all.sh`（推送前） |
| 命令行 | `scripts/scope-id/` |
| 版本 tag 校验 | `Makefile` 的 `tag` 目标 |
| `-dirty` 显式告警 | `scripts/deploy-all.sh` |
| 使用说明 | `.claude/skills/scope-id/SKILL.md` |

## 验收证据

- 断言 6：往探针 worktree 的清单里复制一组（`G-QC` 出现两次）→ 门禁退出码 1，
  报 `✗ 组号重复：1 / G-QC 出现多次`；还原后恢复 0。
- 断言 7：往另一份计划末尾追加一行声明、指向清单里没有的号（本次用 `G-QZ` 验证）→
  门禁报 `2026-11-09-kanban-agent-workflows.md:168 引用了 G-QZ`；还原后恢复 0。
- 真实仓库当前全绿：2614 个 delta 文件被 381 组完整覆盖。
- `scope-id next --count 3` 在真实仓库上输出 `G-QD G-QE G-QF`。
