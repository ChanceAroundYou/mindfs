# e2e/live/ · 对着**线上**跑的复现与验收

和 `e2e/`（隔离实例）的分工：

| | `e2e/`（conftest.py） | `e2e/live/`（本目录） |
|---|---|---|
| 打哪儿 | 临时起的后端 + `web/dist` + 全新配置目录 | `https://home.xiaokubao.space/mindfs`（线上产物） |
| 验什么 | 这份代码能不能跑 | **装到线上的那个 bundle** 到底是什么行为 |
| 数据 | 自己的账户/项目/会话，随进程销毁 | 真实账户、真实会话（**只读**） |
| 什么时候用 | 改完代码、提交前 | 用户报「线上还是不对」、装完要确认生效 |

两者不可互相替代。2026-10-27 那次就是：本地隔离实例全绿，线上装的却还是旧 bundle ——
只有对着线上跑才能一眼看出来。

## 环境（本仓库自己的一份，别借别人的 venv）

```bash
uv venv --python 3.12 .venv
uv pip install --python .venv/bin/python -r e2e/requirements.txt
.venv/bin/python -m playwright install chromium   # 首次才需要
```

全部命令用 `.venv/bin/python`。借 `~/.hermes/...` 那种外部 venv 的问题不是洁癖：
它的 playwright 版本随时被别人升级，而 playwright 与 Chromium revision 是绑死的
（1.61.0 ↔ chromium-1228），升一次就全体选择器/时序失灵，且症状是超时而非报错。

## 凭证

仓库根的 `.env`（被 `.gitignore` 的 `.env*` 挡住，**不入库**）：

```
MINDFS_LIVE_URL=https://home.xiaokubao.space/mindfs
MINDFS_LIVE_USER=<用户名>
MINDFS_LIVE_PASS=<口令>
```

环境变量同名导出可临时覆盖（`MINDFS_LIVE_URL=... python ...`）。
换节点只改 `--url`/`--root`，不用改代码。

## 跑法

```bash
# 一次上滑是否连翻多页（默认自动挑一条历史 ≥24 条、走了虚拟化的会话）
.venv/bin/python e2e/live/scroll_repro.py

# 换站点 / 换项目 / 指定会话 / 看着它跑
... scroll_repro.py --url http://127.0.0.1:7431/mindfs --root proj
... scroll_repro.py --session 1792987062-19b58929c4b5 --headed

# 变异验证：拿一份旧 index-*.js 顶替线上产物，同一段动作跑两遍
... scroll_repro.py --bundle ~/.local/share/mindfs/web/assets/index-<旧>.js
```

退出码：`0` 正常、`1` 复现到了问题、`2` 找不到够长的会话、`3` 该会话没走虚拟化。

用仓库根的 `.venv/bin/python`（依赖见 `e2e/requirements.txt`）。
**不要用 `npx playwright` / 系统 python** —— 那是另外两套缓存，浏览器版本对不上。

## 判断口径（为什么不是「看它跳不跳」）

`scroll_repro.py` 只滚一次到顶，然后**完全不再操作**，数这之后自发产生了几次
`?before_seq=` 请求：

| 观测 | 含义 |
|---|---|
| `prepends == 1` 且锚行漂移 ≤40px | 正常：只翻一页，视口回到原位 |
| `prepends > 1` | 失控：哨兵在上一页回补完成前又点着了下一页 |
| `top == 0` | 卡在顶部：回补一个字节都没写 |

这条判据是补 `web/tests/session-virtualization.tsx` 的缺口：那个 fixture 每轮自己写
`scrollTop = 0`，等于替用户反复触发，所以「一次手势不该连翻多页」它永远看不见。

**`--bundle` 的局限（2026-10-27 实测，别误读成「修了个不存在的问题」）**：
拿修复前那份 bundle 跑线上现有会话，`--bundle` 同样是 `prepends == 1` —— 失控复现要求
「前插后锚行被推出 overscan 且不再挂载」，这取决于该会话每页展开成多少个时间线条目。
所以：**确定性的回归守卫仍是 fixture 那条**（它按构造满足该条件），线上脚本负责的是
另外两件事 —— ① 线上到底跑哪份产物；② 真实数据上「一次手势一页、位置回原位」成立。
复现到失控时它会明确报 `prepends > 1`，但「没报」不等于「这条路径没问题」。

## 硬约束

- **只读**：只发 GET，不发消息、不重启、不改配置、不碰 agent。
- 不要在线上会话里「继续聊」——那条会话属于正在干活的人。
- 新增脚本复用 `harness.py` 的 `login / open_session / PROBE_JS`，别再各写一份；
  尤其是滚动容器的找法（从时间线行往上挑「scrollHeight 明显大于 clientHeight」的最外层
  `overflow-y:auto` 祖先，第一层是假的），踩过一次就别踩第二次。
