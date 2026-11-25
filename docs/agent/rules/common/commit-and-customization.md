# 提交与 fork 定制（长期规则 · 两端通用）

> 这是本项目**最硬的约束**：每次改动都是会被上游合并冲掉的定制，必须留机器可核对的痕迹。
> 完整说明见 `AGENTS.md` 末节「fork 定制与上游合并」，此处是代码提交时的执行摘要。

## 提交信息

- Conventional Commits，**中文描述**：`<type>: <简述>`（`feat`/`fix`/`refactor`/`docs`/`test`/`chore`）。
- 涉及 fork 定制的改动，信息末尾标 **`Scope: G-X`**（组号对应
  `docs/upstream-customizations.yaml` 的分组），便于 `git log` 反查。

## 组号从脚本取，不从记忆取

手写组号会撞：两个并行分支各自取「本地清单最大号 + 1」，拿到同一批号；合并时 git 不冲突、
门禁也不报，只有事后读 `git log` 才发现一个号指着两组互不相关的定制（2026-11-23 真实发生
过一次，三组撞车；2026-11-24 又发生一次）。

```bash
go run ./scripts/scope-id claim docs/superpowers/plans/YYYY-MM-DD-xxx.md [--count N]
```

这条命令**先在共享目录原子占号**（`$(git rev-parse --path-format=absolute --git-common-dir)/scope-ids/`
下用 `O_EXCL`），再在清单里写占位组、在计划头部写声明行。共享同一 clone 的多个 worktree 并发
`claim` 因此不会拿到同一个号；取号还会扫过全部本地分支已提交的号，避免只看陈旧 `origin/main`
而重发兄弟分支已用的号。`next` 只读不占位，真正防撞的是 `claim`。

占位会让门禁变红直到填成真条目 —— 刻意的。规则：只增号不回填空洞；两字母段 `G-AA`…`G-ZZ`
用尽才进三字母段；讲机制的文档里写示例一律用 `G-XX`（同字母重复算模板占位，扫描器跳过），
不要写具体号。

两侧都新取了号时用 `go run ./scripts/scope-id resolve --branch <分支>` 裁决：先引入者保留、
后来者改号，提交信息里的历史 `Scope:` 不改写。详见 `.claude/skills/scope-id/SKILL.md`。

## 清单合并：merge=union，不再人工解尾部冲突

清单是 append-only，每个分支都在文件尾追加新组。仓库根 `.gitattributes` 对
`docs/upstream-customizations.yaml` 声明了 `merge=union`：两条分支追加**不同**号时自动合并、
两边都留，不再报 EOF 文本冲突；追加**同一**号时留成重复（两段 `id:`，或重复 mapping key
导致解析失败），由 `check-upstream` 当场判红，再走 `resolve` 改号。这样「合并意外冲突」与
「真撞号」被干净地分开：前者消失，后者一定响亮地暴露。

## 三件套（缺一不可）

1. **登记** `docs/upstream-customizations.yaml`：按分组格式写「哪个文件、改了什么、
   **为什么必须保留**」，并写明**可见症状**（用户看到什么）。
   新开一组的前提是有**独立的互斥边界**（一组 = 合上游时要么全留要么全弃的改动面）。
2. **针对性测试**：在该组「针对性测试」表列出「测试 → 钉住什么」。合上游时测试红=定制被冲掉。
3. **提交标 `Scope: G-X`**。

## 合上游之后

跑全量门禁；清单里每组的测试必须绿。红了按「为什么必须保留」逐条恢复，**不要按上游实现重写**。

## 完成即提交（不许拖）

每个会话完成一个自洽单元（一处修复 / 一组相关文件 + 门禁绿）就**立刻提交**，
不要攒着等「全部做完再说」。共享工作区里没人认领的未提交改动会被别人卷走：

- `git add -A` 的整包提交会把你的半成品一起提交；
- `make build` / `deploy-all.sh` 全盘编译工作区，你的 WIP 会被编进部署到四端的二进制
  （2026-11-19 实测：另一会话在飞的 `router_brief_cap.go` / `router_service_recovery.go`
  就进了 `-dirty` 产物）；
- 被别人覆盖或覆盖别人时，事后无法分清哪一段是谁的。

做法：

- 提交用**显式 pathspec**（`git commit -- <自己的路径>`），只提交自己动的文件；
- 还没验证完的中间态不要留在主工作区过夜：要么开独立 worktree，要么当轮收口；
  确实要停手时，在提交信息或计划文档里写明 WIP 状态与下一步；
- 没把握「哪些文件是自己的」时，先 `git status` 比对会话开始时的基线。

## 收尾

走 `ship` skill（`bash scripts/deploy-all.sh`）：门禁 → 提交推送 → 本机装 → 分发 WSL/fn/pc-win → 对账。
不要手搓 make/ssh 序列。
