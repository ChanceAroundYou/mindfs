"""对着**线上** mindfs 跑验证/复现的原语集合（只读，绝不打真实 agent）。

和 `e2e/conftest.py` 的分工：那边起一个隔离实例（自己的账户表/项目/会话库），
验的是「这份代码能不能跑」；这里直接开线上站点，验的是「装到线上的那个 bundle
到底什么行为」—— 两者不可互相替代，线上装的是旧产物时只有这边能看出来。

依赖在仓库根的 `.venv`（见 `e2e/requirements.txt`），**不要借外部 venv**：
playwright 与 Chromium revision 绑死（1.61.0 ↔ chromium-1228），借来的那份被谁升一次
就全体失效，且症状是超时而不是报错。

    uv venv --python 3.12 .venv
    uv pip install --python .venv/bin/python -r e2e/requirements.txt
    .venv/bin/python -m playwright install chromium

凭证从仓库根的 `.env` 读（已被 `.gitignore` 的 `.env*` 挡住，不会入库）：

    MINDFS_LIVE_URL=https://home.xiaokubao.space/mindfs
    MINDFS_LIVE_USER=xiaokubao
    MINDFS_LIVE_PASS=...

铁律：本模块只发 GET。不要在这里加「发消息 / 重启 / 改配置」这类动作 ——
对面是本机正在跑的生产服务，不是沙箱。
"""

from __future__ import annotations

import json
import os
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent.parent
ENV_FILE = REPO / ".env"

KEYS = ("MINDFS_LIVE_URL", "MINDFS_LIVE_USER", "MINDFS_LIVE_PASS")


def load_env(path: Path | str = ENV_FILE) -> dict[str, str]:
    """读仓库根 `.env`；已导出的环境变量优先（便于临时换站点）。"""
    path = Path(path)
    values: dict[str, str] = {}
    if path.exists():
        for raw in path.read_text(encoding="utf-8").splitlines():
            line = raw.strip()
            if not line or line.startswith("#") or "=" not in line:
                continue
            key, _, value = line.partition("=")
            values[key.strip()] = value.strip().strip('"').strip("'")
    for key in KEYS:
        if os.environ.get(key):
            values[key] = os.environ[key]
    missing = [k for k in KEYS if not values.get(k)]
    if missing:
        raise SystemExit(
            f"缺少 {', '.join(missing)}。在 {path} 里写：\n"
            "  MINDFS_LIVE_URL=https://home.xiaokubao.space/mindfs\n"
            "  MINDFS_LIVE_USER=<用户名>\n"
            "  MINDFS_LIVE_PASS=<口令>"
        )
    return values


# 会话滚动容器没有稳定的对外标记，只能从时间线行往上找。**必须挑真正会滚的那个**：
# 从行往上第一层 overflow 是 auto 的祖先实测 clientHeight == scrollHeight（不滚），
# 真正吃滚动的是再上面一层。判据取「scrollHeight 明显大于 clientHeight」的行里最外层。
# 不往产品代码里加 data-* 只为测试服务 —— 那是给线上 bundle 增加只对测试有意义的负担。
PROBE_JS = """
window.__liveProbe = {
  scroller() {
    if (this._el && document.contains(this._el)) return this._el;
    const row = document.querySelector('[data-timeline-key]');
    if (!row) return null;
    let node = row.parentElement;
    let found = null;
    while (node) {
      const style = getComputedStyle(node);
      if ((style.overflowY === 'auto' || style.overflowY === 'scroll')
          && node.scrollHeight > node.clientHeight + 1) {
        found = node;
      }
      node = node.parentElement;
    }
    this._el = found;
    return found;
  },
  metrics() {
    const el = this.scroller();
    if (!el) return { mounted: 0, error: 'no scroller' };
    return {
      top: Math.round(el.scrollTop),
      height: Math.round(el.scrollHeight),
      client: Math.round(el.clientHeight),
      atBottom: Math.round(el.scrollHeight - el.clientHeight - el.scrollTop),
      mounted: document.querySelectorAll('[data-timeline-key]').length,
      virtualized: document.querySelectorAll('[data-index]').length > 0,
    };
  },
  // 视口内最上面几行的键 —— 用它判「位置有没有回到原位」，比 scrollTop 数字有意义：
  // 前插会让 scrollHeight 整体变化，scrollTop 变大是正常的，行没动才是对的。
  // 取三行而不是一行：回补过程中最上面那行可能正好被虚拟器卸载，只留一行会读不到。
  anchors() {
    const el = this.scroller();
    if (!el) return [];
    const top = el.getBoundingClientRect().top;
    const out = [];
    for (const row of el.querySelectorAll('[data-timeline-key]')) {
      const r = row.getBoundingClientRect();
      if (r.top >= top - 1) out.push({ key: row.dataset.timelineKey, top: Math.round(r.top) });
      if (out.length === 3) break;
    }
    return out;
  },
  anchorDrift(saved) {
    const el = this.scroller();
    if (!el || !saved || !saved.length) return 'unmeasured';
    // 逐个试：第一个还挂载着的就是判据。
    for (const item of saved) {
      const row = el.querySelector(`[data-timeline-key="${CSS.escape(item.key)}"]`);
      if (row) return Math.round(row.getBoundingClientRect().top) - item.top;
    }
    return 'unmeasured';
  },
  toTop() { const el = this.scroller(); if (el) el.scrollTop = 0; return this.metrics(); },
  toBottom() { const el = this.scroller(); if (el) el.scrollTop = el.scrollHeight; return this.metrics(); },
};
"""

TOUR_BUTTONS = ('button:has-text("跳过导览")', 'button:has-text("Skip tour")')


def skip_tour(page) -> None:
    """首次进入会盖一层 13 步引导浮层，它吃掉点击。没有就跳过。"""
    for selector in TOUR_BUTTONS:
        try:
            page.click(selector, timeout=3_000)
            return
        except Exception:  # noqa: BLE001 - 浮层没出现就是正常情况
            continue


def login(page, env: dict[str, str]) -> None:
    """走真实登录界面（AuthGate 的 autoComplete 是稳定选择器）。"""
    page.goto(env["MINDFS_LIVE_URL"], wait_until="domcontentloaded", timeout=60_000)
    page.wait_for_selector('input[autocomplete="username"]', timeout=60_000)
    page.fill('input[autocomplete="username"]', env["MINDFS_LIVE_USER"])
    page.fill('input[autocomplete="current-password"]', env["MINDFS_LIVE_PASS"])
    page.press('input[autocomplete="current-password"]', "Enter")
    page.wait_for_selector('[data-onboarding="workspace"]', timeout=60_000)
    skip_tour(page)


def open_session(page, env: dict[str, str], root: str, key: str) -> None:
    """打开某个会话。必须带 `session=`，只给 `view=chat` 会被改写成 workspace。"""
    url = f"{env['MINDFS_LIVE_URL']}/?root={root}&session={key}&view=chat"
    page.goto(url, wait_until="domcontentloaded", timeout=60_000)
    skip_tour(page)
    # 等「挂载」而不是「可见」：虚拟化下第一条数据行常常在视口外（或高度还没测出来），
    # 等的目标只是「时间线渲染出来了」，可见与否跟这条判据无关。
    page.wait_for_selector("[data-timeline-key]", state="attached", timeout=60_000)
    page.evaluate(PROBE_JS)


def sessions(page, env: dict[str, str], root: str, limit: int = 20) -> list[dict]:
    """列出该项目的会话（供挑一条长会话来复现）。用绝对 URL：页面路径未必以 / 结尾，
    相对路径会算到部署前缀外面去（`/api/sessions` 而不是 `/mindfs/api/sessions`）。"""
    base = env["MINDFS_LIVE_URL"].rstrip("/")
    payload = page.evaluate(
        """async ({base, root, limit}) => {
            const r = await fetch(`${base}/api/sessions?root=${encodeURIComponent(root)}&limit=${limit}`);
            return r.ok ? await r.json() : { items: [] };
        }""",
        {"base": base, "root": root, "limit": limit},
    )
    return list(payload.get("items") or [])


def session_total(page, env: dict[str, str], root: str, key: str) -> int:
    """该会话的历史条数（window_meta.total），用来挑「够长、翻得动」的那条。"""
    base = env["MINDFS_LIVE_URL"].rstrip("/")
    return int(
        page.evaluate(
            """async ({base, root, key}) => {
                const r = await fetch(`${base}/api/sessions/${encodeURIComponent(key)}?root=${encodeURIComponent(root)}&latest=1`);
                if (!r.ok) return 0;
                const d = await r.json();
                const m = d.window_meta || (d.session && d.session.window_meta) || {};
                return Number(m.total || 0);
            }""",
            {"base": base, "root": root, "key": key},
        )
        or 0
    )


def dump(value) -> str:
    return json.dumps(value, ensure_ascii=False, indent=1)


def bundle_hashes(env: dict[str, str]) -> dict[str, str]:
    """线上正在发的 index-*.js 是否就是本地 `web/dist` 的那一份。

    2026-10-27 踩过两次「改了但线上没变」：一次是 `make install` 没跑，一次是服务没重启。
    光看页面没变化分不清是这两者还是代码本身没修好 —— 先把这条排掉再谈行为。
    """
    import re
    import urllib.request

    url = env["MINDFS_LIVE_URL"].rstrip("/") + "/"
    try:
        with urllib.request.urlopen(url, timeout=20) as resp:
            html = resp.read().decode("utf-8", "replace")
    except Exception as exc:  # noqa: BLE001
        return {"error": str(exc)}
    match = re.search(r"assets/(index-[A-Za-z0-9_-]+\.js)", html)
    served = match.group(1) if match else ""
    installed = Path.home() / ".local" / "share" / "mindfs" / "web" / "assets" / served
    return {
        "served": served,
        # 「线上跑的就是刚装的那份」——不同就是服务没重启，或 make install 没跑。
        "matches_installed": bool(served) and installed.exists(),
    }


