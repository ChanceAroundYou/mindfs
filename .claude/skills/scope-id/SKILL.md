---
name: scope-id
description: 取组号（Scope: G-XX）与撞号裁决。开新计划、要登记一组 fork 定制、或在 main 上合回分支前，先跑这里的命令拿号，不要手写编号。触发词：Scope 组号/G-XX 编号/撞号/新计划开编号/upstream-customizations 登记号
---

# scope-id —— Scope 组号由脚本发，手写不算数

组号是**手写字符串**。两个并行分支各自从本地清单取「最大号 + 1」，会拿到同一批号；
合并时 git 不冲突、旧门禁也不报，只有人事后读 git log 才发现同一个号指着两组互不相关的
定制（2026-11-23 真实事故：G-PW / G-PX / G-PY 三组撞车，靠手工改成 G-PZ / G-QA / G-QB）。

所以：**号从工具来，不从记忆来**。工具是 `scripts/scope-id`，门禁是 `make check-upstream`
（断言 6 组号唯一、断言 7 计划里的号已登记）。

## 三条命令

```bash
# 1. 拿号：打印接下来 N 个空号（读 origin/main ∪ 本地清单，只增不回填）
go run ./scripts/scope-id next --count 1

# 2. 申请：占号 + 在清单里占位 + 写进计划头部 `> Scope IDs: G-QD（claimed …）`
go run ./scripts/scope-id claim docs/superpowers/plans/YYYY-MM-DD-xxx.md [--count N]

# 3. 查：清单内唯一 + 计划引用的号都已登记（= make check-upstream 的 6/7 两条）
go run ./scripts/scope-id check
```

`claim` 写的占位标题是 `WIP — 待填写`，会让门禁断言 5（至少一条测试）变红 —— **这是刻意的**：
占位必须被填成真条目，否则那组等于没有保护。

## 撞号裁决

在 main 上准备合回一条分支时，两侧都新取了号：

```bash
go run ./scripts/scope-id resolve --branch <分支名> --dry-run   # 先看结论
go run ./scripts/scope-id resolve --branch <分支名>            # 真改：清单 + 计划头
```

规则是**先引入者保留、后来者改号**，判断依据是 `git log -S` 找出各组号的引入提交时间；
基准是 `merge-base HEAD <branch>`（不是某条 remote 分支 —— 那会把祖先里早就登记过的号
全判成撞号）。改号只动清单与计划头部的 `> Scope IDs:` 行，**提交信息里的历史 `Scope:`
一律不改写**：历史是事实，不是索引。裁决后在清单里留一条 `# 撞号说明` 注释。

## 规矩

| 规矩 | 为什么 |
|---|---|
| 只从工具取号 | 手写的号没有占位，撞了没有任何机制能发现 |
| 只增不回填空洞 | 回填让两个分支更容易抢到同一个洞 |
| 两字母段用尽（G-ZZ）再进三字母 | 号短、可读；`G-AAA` 只是容量兜底 |
| 提交信息标 `Scope: G-X`，与清单组号对应 | 三件套之一，见 `docs/agent/rules/common/commit-and-customization.md` |
| 文档里写示例用 `G-XX` / `G-XXX` | 同字母重复是模板占位，扫描器会跳过；真号必须已登记 |

## 与其他门禁的关系

- `make check-upstream`：断言 6（唯一）+ 断言 7（已登记），`scripts/deploy-all.sh` 推送前会跑
- `make tag` → `scripts/check-tag.sh`：版本号的五条护栏（形状、不存在、发布说明一致等）
- 版本号不走这里：它由 `git describe` 派生，`deploy-all.sh` 在版本带 `-dirty` 时会警告

仓库里目前**没有 bundle 多条计划来做并行取号**；如果真有两条分支同时 `claim`，合并时跑
`resolve` 即可 —— 这正是它存在的理由。
