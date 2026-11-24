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

## 命令

```bash
# 1. 拿号（只读）：打印接下来 N 个空号。读 origin/main ∪ 本地清单 ∪ 全部本地分支
#    ∪ 共享预留目录，只增不回填
go run ./scripts/scope-id next --count 1

# 2. 申请：先在共享目录原子占号，再在清单里占位 + 写进计划头部 `> Scope IDs: G-QD（claimed …）`
go run ./scripts/scope-id claim docs/superpowers/plans/YYYY-MM-DD-xxx.md [--count N]

# 3. 查：清单内唯一 + 计划引用的号都已登记（= make check-upstream 的 6/7 两条）
go run ./scripts/scope-id check

# 辅助：看/回收预留、失败回滚
go run ./scripts/scope-id reservations [--gc]
go run ./scripts/scope-id release <ID...>
```

`claim` 写的占位标题是 `WIP — 待填写`，会让门禁断言 5（至少一条测试）变红 —— **这是刻意的**：
占位必须被填成真条目，否则那组等于没有保护。

## 为什么 claim 不会撞号（两条机制）

1. **跨 worktree 原子预留**：号先在 `$(git rev-parse --path-format=absolute --git-common-dir)/scope-ids/<ID>`
   用 `O_CREATE|O_EXCL` 占住。所有 worktree 共享同一个 clone、同一个 common dir，于是并发
   `claim` 抢同一个号时只有一个能创建成功，其余顺延成下一个号。文件体记 `{id,branch,plan,worktree,ts}`；
   崩溃留下空文件也算占用（宁可跳号，绝不重发），由 `reservations --gc` 回收。
2. **扫全部本地分支已提交的号**：兄弟分支常常已经提交了某个号但还没推、也没走预留
   （旧机制留下的号）。claim 取号时会 `git show <branch>:清单` 扫一遍所有本地分支，
   不再只读可能是陈旧的 origin/main。2026-11-24 实测：origin/main 停在 G-QT，但
   task-117 已用 G-RD、edge-relay 已用 G-RE、task-115 已用 G-RG —— 只看远端就会重发 G-RD。

## 合并不再因「末尾各自追加」而冲突

清单是 append-only、每分支都在文件尾追加新组；两条分支即使追加**不同**号，也改动了
同一段尾部上下文，普通 merge 会报文本冲突。仓库根的 `.gitattributes` 对清单声明了
`merge=union`：不同号的追加自动合并（两边都留），同号则留成重复（两段 `id:`，或重复
mapping key 导致解析失败），**无论哪种都由 `check-upstream` 当场判红**，再走下面的 `resolve`。

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
| 取的号立刻 `claim` 占住 | 预留是跨 worktree 不撞号的物理基础；只 `next` 不 `claim` 等于没占 |
| 只增不回填空洞 | 回填让两个分支更容易抢到同一个洞 |
| 两字母段用尽（G-ZZ）再进三字母 | 号短、可读；`G-AAA` 只是容量兜底 |
| 提交信息标 `Scope: G-X`，与清单组号对应 | 三件套之一，见 `docs/agent/rules/common/commit-and-customization.md` |
| 文档里写示例用 `G-XX` / `G-XXX` | 同字母重复是模板占位，扫描器会跳过；真号必须已登记 |

## 与其他门禁的关系

- `make check-upstream`：断言 6（唯一）+ 断言 7（已登记），`scripts/deploy-all.sh` 推送前会跑
- `make tag` → `scripts/check-tag.sh`：版本号的五条护栏（形状、不存在、发布说明一致等）
- 版本号不走这里：它由 `git describe` 派生，`deploy-all.sh` 在版本带 `-dirty` 时会警告

仓库里目前**没有 bundle 多条计划来做并行取号**。若两条分支仍撞了号（例如一方在预留机制
上线前就提交了旧号），合并后 `check-upstream` 会红，跑 `resolve` 改号即可 —— 这正是它存在的理由。
