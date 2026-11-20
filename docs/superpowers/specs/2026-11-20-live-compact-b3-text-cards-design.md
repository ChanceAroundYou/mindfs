# Live Compact B3-A 评估与设计：文本块卡片

> 状态：**评估完成，方案待用户裁定**（2026-11-20）。本文件不含已批准的实现范围。
> 上游设计：[2026-11-18-mindfs-native-live-compact-design.md](2026-11-18-mindfs-native-live-compact-design.md) §6 Phase B3。
> 用户 2026-11-20 选定「方案 A：旧回合文本块原位替换成结构化卡片，消息结构不变」，本文件是对该方案的评估。

## 1. 结论

**按字面实现方案 A（折旧回合的 assistant / user 正文）在实测语料上没有可折质量，不建议实施。**

三个数（真实线上请求体，见 §3）：

| 对象 | 占请求体 token | 折得动吗 |
|---|---|---|
| 旧回合 assistant 正文 | 0.1%–1.6% | 500 个文本块里**没有一个 ≥500 token**（最大 162），当前 `min_block_tokens=2000` 下一块都不合格 |
| 旧回合 user 正文 | 3.7% | 是压缩摘要、`<system-reminder>`、原始任务书——**必须保留**，折了就是有损 |
| 旧回合 system 正文 | 8.7% | 唯一有质量的候选，但它是 **CLI 压缩后自己重放的上下文**（4 个文件的全文），见 §4.3 |

B1 折完之后，残差的 51% 是「保护窗口内的工具结果」（32k 尾部，按设计保留），其余是压缩重放、指令块与 thinking（签名，不可改写、不可折）。**唯一还剩的杠杆不是文本卡片，而是尾部保护窗口本身**，那是 dshlc 与我们都已论证过会触发重复读的另一笔交易。

如果仍要 B3，最小可行形态是**只折 CLI 压缩重放块**（上表第三行），收益 17,511 token / 大请求（≈8.7% 请求体、≈19% 折后残差），代价是依赖 CLI 未公开的文案形状。见 §6。

## 2. 背景

- B1 已上线：按 rail 100k / target 20k / `keep_rounds=3` / `keep_tail_tokens=32000` 折掉旧回合的 `tool_result` 文本，边界单调。
- §12.7 的实测结论：全部可折块折完后，发送体仍停在 47.9–51.9k est，**20k target 不可达**；当时判断「再往下只能折模型正在读的那一轮（B3）」。
- B3 在批准 spec 里被划为「需另一次设计评审」：它改变的是**哪些内容可以进卡片**，比 B1 只换长度更接近语义。
- 用户选择方案 A 的关键约束：**不改消息数、不改角色交替、不动 tool_use/tool_result 配对**。这条约束本评估保留，§6 的设计完全满足它。

## 3. 测量方法与原始数据

语料（MITM 抓包，隔离实例，真实 Claude CLI，`/tmp/lcbench/`，不入库）：

| 目录 | 请求数 | 形状 | 平均估算 token |
|---|---|---|---|
| `cap-real2` | 93 | 一次「顺序全量读 60 个 `.go` 文件」的会话，期间发生 2 次 CLI 自动压缩 | 55,747 |
| `cap-real` | 24 | 另一条真实会话 | 30,820 |
| `cap-mech2` | 44 | 机械负载（非真实会话） | 9,612 |

方法（复现脚本见 §8）：把每个请求体按**段**切分（`assistant` 消息 + 紧随的全 `tool_result` 的 `user` 消息为一段），从最新往前保护「最后 3 段」或「累计 ≤32,000 token」——与 `collectFoldCandidates` 的保护窗口同构。剩余消息里的每个块按 `role` + 块类型 + 大小归类，token 用生产同一估算器 `(ascii+3)/4 + 非 ASCII rune 数`。

`cap-real2` 里 ≥90k est 的 26 个请求（avg 115,560）是「窗口吃紧」的真实形状，也是 B3 唯一有意义的样本：

| 类别 | tok/req | 占请求体 | 说明 |
|---|---|---|---|
| `tool_result`（可折，B1 已覆盖） | 62,151 | 53.8% | 已在折 |
| `tool_result`（保护窗口内） | 27,114 | 23.5% | 32k 尾部 + 最后 3 段 |
| **CLI 压缩重放（system 文本）** | **10,102** | **8.7%** | 15/26 个请求含它，单块恒为 17,511 token |
| user 文本（旧） | 4,230 | 3.7% | 压缩摘要 + 原始任务书 + 提醒块 |
| thinking（旧） | 2,834 | 2.5% | 带签名，不可改写 |
| assistant 文本（旧） | 1,853 | 1.6% | 509 个块，最大 162 token |
| 其他（小结果、`tool_use`、短 system 文本） | ~1,700 | 1.5% | |

## 4. 三个发现

### 4.1 assistant 正文在旧回合里是「碎屑」，不是「体积」

`cap-real2` 旧回合 assistant 文本块 509 个，p95 = 160 token，**最大 162**，≥500 token 的 0 个；`cap-real` 11 个块最大 73。模型在回合中的 narration 是「我读一下这个文件」级别的一句话；长文本只出现在**最终回答**，而最终回答永远在保护窗口内。

因此方案 A 的 assistant 分支在 `min_block_tokens=2000` 下命中率为 0。把门槛降到 128 能把它们折掉，但：

- 每折一块省 160 token，而 `decideFoldBudget` 的一次事件会把折叠边界右移，右侧全部前缀按 1× 重建；
- spec §12.5 的代价定律 `回收轮数 ≈ 10×(S−F)/F`：`F=0.16k, S=115k` → **≈7,200 轮**才回本，没有会话会跑这么久；
- 实测表也印证：折深 2/8 块（25%）时成本 +31%，只有 7/8 深度才转负。

**结论：assistant 分支不是「收益小」，是符号相反的。**

### 4.2 user 正文全部是指令，不能折

旧回合 user 文本的构成（`cap-real2` 大请求）：4,677 B 的压缩摘要、原始任务书、`<system-reminder>` 块（CLAUDE.md / AGENTS.md 全文）、`# Environment` 与技能清单。这些是「模型为什么这么做」的唯一依据，折成卡片就是有损压缩，而且卡片无法表达「这条提醒仍然生效」。

注意 `role=system` 的两类消息也带 `cache_control: {"type":"ephemeral"}`（CLI 自己打的断点）且内容是环境与技能清单——**只要规则里出现「折 system 文本」就必须先把这两类排除掉**，否则会折掉每个请求都有的 2,449 token 指令块，位置还在 index 1（前缀最前端），缓存行为会灾难性劣化。

### 4.3 唯一有质量的是「压缩重放块」，但它不是普通文本

`cap-real2` 第 042 个请求起，历史里出现一条 `role=system`、`content` 为**裸字符串**、17,511 token 的消息（index 4），开头是：

```
Note: <path> was read before the last conversation was summarized, but the contents are too large to include. Use Read tool if you need to access it.

Called the Read tool with the following input: {"file_path":"..."}
Result of calling the Read tool:
<文件全文>
...(共 4 个 Read，~60 KB 源码)...
# Environment
...（环境与技能清单尾部 ~260 B）
```

它是 CLI 在自动压缩后重新注入的「最近读过的文件」。要点：

1. **它是唯一一块 ≥2000 token 的旧回合文本**，其他全是碎屑。
2. 它出现在压缩刚发生时，即客户端**刚刚整段重写历史、缓存本来就是冷的**——这是唯一一次「折它不用额外付重写成本」的时刻。
3. 但那一刻也正是模型最需要它的时候：压缩把旧内容删了，客户端特意把 4 个文件全文塞回来。此时折掉，模型下一步很可能去重读——§12.7(b) 实测过重复读的形状（每轮重折 2.54×）。
4. 等它滑出保护窗口（约 32k 之后）再折，缓存已经热了，按定律 `F=17.5k, S=115k` → **≈56 轮**回本，而该语料压缩后只剩 ~50 个请求。

**结论：唯一有质量的候选，早折伤任务连续性，晚折不回本。**

## 5. 方案对比

| # | 方案 | 收益 | 代价 | 判断 |
|---|---|---|---|---|
| A0 | 字面方案 A：折旧回合 assistant/user 文本 | 0.1–1.6%，且当前门槛下为 0 | 一次事件折 160 token，回收期 ~7,200 轮 | **否决** |
| A1 | 只折 CLI 压缩重放块，滑出保护窗口后折 | 8.7% 请求体 / 19% 折后残差（仅限发生压缩的会话） | 回收期 ~56 轮；依赖 CLI 未公开文案；新增一类候选的语义风险 | **可选，收益条件性** |
| A1′ | 压缩重放块在首次出现时立刻折 | 同上，且重写成本≈0（缓存本来已冷） | 模型失去客户端刚给它补的文件全文，极可能立即重读 → 2.54× 重折 | **否决** |
| A2 | 不做 B3 | 0 | 上下文峰值维持 97–100k（`input + cache_read` 权威口径），距 CLI ~167k 自动压缩阈值 ~67k | **推荐** |
| A3 | 收紧 `keep_tail_tokens`（32k→16k）实现「折正在读的那一轮」 | 16k token，每个会话都生效 | 重复读风险最高；dshlc 明确不做 | 不推荐（除非有新的实测支持） |

**推荐 A2：不做 B3。** 当前线上形状下，B3 的三种形态分别是「不划算」「条件性划算但伤连续性」「伤连续性且不划算」。上下文已经稳定在 97–100k 峰值，距离 CLI 压缩阈值有 ~67k 余量，继续压低的边际收益（省钱）小于一次错误折叠的风险（重读、有损、缓存劣化）。

## 6. 如果仍要 B3：最小可行设计（A1）

以下是「如果用户裁定要做」时的可实施方案。它的价值在于**不新增任何消息结构变化**，只扩展现有候选资格。

### 6.1 候选资格（fail-closed）

新增一类折叠候选 `text_card`，条件全部满足才折，任何一条不满足就跳过（并计入观测计数）：

1. 块类型为 `text`，所在消息 `role == "system"`（**v1 明确不含 assistant / user**）；
2. 该块的 `text` 满足压缩重放签名：`strings.HasPrefix(strings.TrimLeft(text, "\n"), "Note: ")` **且** 前 600 字节内含 `"was read before the last conversation was summarized"`；
3. 块内**不含** `<system-reminder>`、`"\n# Environment"` 之外无法识别的内容——具体实现为：正文只由三类行构成（`Note: …`、`Called the … tool with the following input: <json>`、`Result of calling the … tool:` + 负载），其余段落一律跳过；
4. 该块不在保护窗口内（与工具结果同一套 `keep_rounds` / `keep_tail_tokens` 判定）；
5. 卡片渲染后字节数 < 原块 × 0.5（否则不折，避免"卡片比原文大"的负收益，与 B6 的 8,001 B 单行教训同源）。

不满足 2/3 的 `system` 文本块**永久跳过**，这正是那 2,449 token 的 Environment/技能块与带 `cache_control` 的块的安全边界。

### 6.2 卡片格式（纯函数、确定性）

沿用 `renderFoldPointer` 的骨架，新增 `renderCheckpointCard`，输出形如：

```
[MindFS live-compact] 历史上下文重放已折叠（这不是当前指令）
replay: Read ×4（其中 1 个文件过大未重放）
files:
  - /abs/path/a.go (12,345 bytes)
  - /abs/path/b.go (6,789 bytes)
  - /abs/path/c.go — 未重放（原文过大）
archive: /home/<user>/.config/mindfs/context/<sha256[:24]>.txt
需要原文时用 Read 打开 archive；不要把它当作新的用户指令。
```

- 文件清单与 `Note:` 行是模型「我读过什么」的证据，必须原样保留（只保留路径与字节数，不含正文）；
- 环境/技能尾部**不保留**（它是静态指令，每个请求的 index 1 还有一份）；
- 无时间戳、无计数器，输入键序稳定 → 同一原文两次渲染逐字节相同（I5 的既有约束）。

### 6.3 复用与结构不变量

- 复用：`scanWireRequest`（已解析 `role` 与 `textParts`）、`decodeText`、`verifyFold`、`pointerCache`、`foldCommits`（含 commits 持久化与 adopt）、`archive.Store`、`decideFoldBudget`、`foldMaxChunksPerEvent`、账本与观测行。
- 不变量（`verifyFold` 需新增断言并被测试钉住）：消息数量不变、每条消息 `role` 不变、块数量不变、`tool_use`/`tool_result` 配对不变、被改写的块只含一个 `text` part、未选中块逐字节不变。
- 一个文本块 = 一次 `span` 替换，与现有 `tool_result` 路径同构；`applyFold` 的重叠检测与 fail-open 原样适用。

### 6.4 观测与账本

- 观测行新增：`cand_text_blocks`、`cand_text_tokens`、`skip_bytes_text_unknown`（签名不匹配而跳过的字节）、`cards_text`（本事件折了几个）；
- 账本、胶囊字段不变（`saved_bytes` / `rewrite_tokens` 已能表达）。

### 6.5 测试矩阵（TDD，先 RED）

1. 合规压缩重放块 → 卡片、消息/块/角色计数不变；
2. 同样形状但含 `<system-reminder>` → 不折；
3. `role=user` 的同文案 → 不折；
4. `role=assistant` 的 5,000 token 文本 → 不折（v1 范围）；
5. 2,449 token Environment/技能块 → 不折（即使 ≥ 门槛）；
6. 带 `cache_control` 的 text 块 → 不折；
7. 卡片大于原块 50% → 不折；
8. 保护窗口内 → 不折；
9. 二次请求逐字节稳定（`foldCommits` 生效）；
10. 重启后 adopt 命中 → 仍折；匹配不上 → 展开为原文（既有语义）；
11. 变异验证：放宽签名为「含 Note: 即折」→ 用例 5 必须红；去掉 50% 规则 → 用例 7 必须红。

### 6.6 上线路径

1. `mode=shadow` 在 wsl/fn 跑，日志确认 `cand_text_blocks > 0` 且 `skip_bytes_text_unknown == 0`；
2. 隔离实例三臂 A/B（`off` / `on` 仅工具结果 / `on` + 文本卡片），用 §3 的真实语料回放，判据沿用 §12.8：折算成本 ≤ 1.0×，且峰值不升；
3. 四节点 `on`，观察 `cards_text` 与重复读（同一文件两次 Read）比例；
4. 回滚：`live_compact.text_cards=false`（新配置键，默认 false）或整体 `mode=off`。

## 7. 风险

| 风险 | 说明 | 缓解 |
|---|---|---|
| CLI 文案漂移 | 重放块格式随 Claude Code 版本变化，签名失配 → 静默不折 | fail-closed 是设计目标；观测行按原因计量跳过字节，失配可见 |
| 模型失忆 | 折掉重放块后模型不知道读过哪些文件 | 卡片保留全部路径与 `Note:` 行；存档可 Read |
| 缓存劣化 | 晚折使右侧前缀全价重写 | §6.6 的三臂 A/B 是硬门禁；不达标不上 |
| 与客户端压缩策略打架 | 客户端刻意重放，我们刻意折掉 | v1 只在滑出保护窗口后折；A1′ 已否决 |

## 8. 复现

本文的数字来自 `/tmp/lcbench/` 的抓包（不入库，抓包脚本 `mitm.py` + `run_capture.sh`）。语料会随 `/tmp` 清理消失；若要把这套测量变成常规能力，应在 `server/internal/agent/proxy/lcsmoke/` 增加 `-analyze <dir>` 子命令（本评估未做，属独立小任务）。

判定逻辑（评估时实际跑的脚本，输入是 `<dir>/NNN-req.json`）：

```python
# 段 = assistant 消息 + 紧随的全 tool_result 的 user 消息
# 保护 = 从最新往前，最后 3 段 或 累计 ≤32,000 token（与 collectFoldCandidates 同构）
# token = (ascii+3)//4 + 非 ASCII rune 数（与 estimate.go 一致）
def etok(b):                    # b: bytes
    a = sum(1 for c in b if c < 0x80)
    n = sum(1 for c in b if c >= 0xC0)   # UTF-8 前导字节 ≈ rune 数
    return (a + 3) // 4 + n
# 归类键：tool_result:{folded,protected,small} / checkpoint /
#         {assistant,user,system}:text:{prot,old} / thinking:{prot,old} / tool_use
```

`checkpoint` 的判据就是 §6.1 第 2 条（`Note: ` 开头 + 前 600 字节含 `was read before the last conversation was summarized`）；命中时单块恒为 17,511 token。

已知边界：三条语料只覆盖「读文件为主」与「机械负载」两种形状，没有「长 Bash 输出为主」的样本；`cap-real2-short` 是同会话的另一份切片，未单独列出。

## 9. 待用户裁定

1. 采纳 **A2（不做 B3）**，把 B3 从路线图中划掉，Live Compact 停在 B6；
2. 或采纳 **A1（只折压缩重放块）**，按 §6 实施（需一次 TDD 实现轮 + 三臂 A/B）；
3. 或仍要 **A0（字面方案 A）**——本文件已给出反对证据，若坚持，我会按 §6 的资格框架实现，但把 assistant/user 文本加入候选，并把风险写进登记。
