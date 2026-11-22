"""线上任务详情面板几何只读探针（桌面 / 移动）。

为什么单独有一个 live 脚本：隔离实例验的是「这份代码对不对」，这里验的是
**装到线上的那份 bundle 在真实任务上的行为**。2026-11-22 用户两次报「面板还是贴底 +
被顶高」，两次都不是代码没修对，而是线上发的是旧 bundle（并发会话在共享 PREFIX 上
`make install` 覆盖过）。只看文件 mtime 分不清，必须同时看三样：

1. 服务端 index.html 指向哪个 index-*.js；
2. 浏览器**实际执行**的是哪个（resource timing）；
3. 面板从第一帧到内容渲染完成后的矩形是否变化。

用法（在仓库根）：
    .venv/bin/python e2e/live/panel_geometry.py            # 桌面 1440x900
    .venv/bin/python e2e/live/panel_geometry.py --mobile   # 手机 390x844

只读：登录 → 看板 → 打开一张真实任务卡（含结果文本优先）→ 采样 → 双击验证 → 关闭。
不发送任何留言、不点任何写操作。
"""

from __future__ import annotations

import argparse
import json
import sys

import harness
from playwright.sync_api import sync_playwright

WORKSPACE = '[data-onboarding="workspace"]'
VIEW_SWITCHER = '[data-onboarding="main-view-switcher"]'
BOARD_REFRESH = '[data-onboarding="task-refresh"]'
DIALOG = ".mindfs-panel-dialog"
OVERLAY = ".mindfs-panel-overlay"

PROBE_JS = r"""
() => {
  window.__panelProbe = { samples: [], clickAt: null };
  const snap = () => {
    const d = document.querySelector('.mindfs-panel-dialog');
    if (!d) return;
    const r = d.getBoundingClientRect();
    window.__panelProbe.samples.push({
      t: Math.round(performance.now()),
      w: Math.round(r.width), h: Math.round(r.height),
      top: Math.round(r.top), left: Math.round(r.left),
      bottom: Math.round(r.bottom), vw: innerWidth, vh: innerHeight,
    });
  };
  new MutationObserver(snap).observe(document.body, { childList: true, subtree: true });
  window.__panelProbeTimer = setInterval(snap, 50);
  snap();
}
"""

DUMP_JS = r"""
() => {
  const d = document.querySelector('.mindfs-panel-dialog');
  if (!d) return { error: 'no dialog' };
  const keys = ['position', 'display', 'alignItems', 'height', 'maxHeight', 'minHeight',
    'contain', 'transform', 'filter', 'overflow', 'padding'];
  const chain = [];
  let node = d;
  while (node && node !== document.documentElement) {
    const cs = getComputedStyle(node);
    const r = node.getBoundingClientRect();
    const row = { tag: node.tagName.toLowerCase(), cls: String(node.className || '').slice(0, 80) };
    for (const k of keys) row[k] = cs[k];
    row.rect = { top: Math.round(r.top), left: Math.round(r.left), w: Math.round(r.width), h: Math.round(r.height) };
    row.panelVh = cs.getPropertyValue('--panel-vh').trim();
    row.fixedHeight = node.getAttribute('data-fixed-height');
    row.align = node.getAttribute('data-align');
    chain.push(row);
    node = node.parentElement;
  }
  return {
    chain,
    resources: performance.getEntriesByType('resource')
      .map((e) => e.name).filter((n) => /\/assets\/index-[A-Za-z0-9_-]+\.js/.test(n)),
    swController: navigator.serviceWorker && navigator.serviceWorker.controller
      ? navigator.serviceWorker.controller.scriptURL : null,
  };
}
"""


def prepare_board(page, mobile: bool) -> None:
    page.reload(wait_until="domcontentloaded")
    page.wait_for_selector(WORKSPACE, timeout=60_000)
    for sel in ('button:has-text("Skip tour")', 'button:has-text("跳过导览")'):
        try:
            page.click(sel, timeout=1_500)
            break
        except Exception:  # noqa: BLE001 - 引导只出现一次
            pass
    if mobile:
        expand = page.locator('button[aria-label^="Expand"]')
        if expand.count():
            expand.first.click()
            page.wait_for_timeout(300)
    page.locator(VIEW_SWITCHER).first.wait_for(state="visible", timeout=30_000)
    page.locator(VIEW_SWITCHER).first.get_by_role("tab", name="Board", exact=True).click()
    page.wait_for_selector(BOARD_REFRESH, timeout=30_000)
    if mobile:
        # 抽屉宽 75vw，最右边的竖带落在遮罩上，点它收起抽屉。
        page.mouse.click(page.viewport_size["width"] - 12, 400)
    else:
        page.mouse.click(page.viewport_size["width"] - 60, 400)
    page.wait_for_timeout(600)


def pick_card(page):
    cards = page.locator("article")
    total = cards.count()
    for i in range(total):
        card = cards.nth(i)
        if card.locator('[data-testid="task-card-result"]').count():
            return card, total
    return (cards.first if total else None), total


def measure(page, mobile: bool) -> dict:
    """采样从点击前开始（main 里先装好探针），这里只负责收尾与双击验证。"""
    page.wait_for_selector(DIALOG, timeout=10_000)
    page.wait_for_timeout(3_000)
    probe = page.evaluate("() => { clearInterval(window.__panelProbeTimer); return window.__panelProbe; }")
    dump = page.evaluate(DUMP_JS)
    # 双击第二下：第一下打开、第二下落在刚挂载的遮罩上，修复后仍应只有一个面板。
    page.locator(OVERLAY).first.click(position={"x": 5, "y": 5})
    page.locator(DIALOG).first.wait_for(state="detached", timeout=10_000)
    card, _ = pick_card(page)
    card.dblclick(position={"x": 40, "y": 30})
    page.wait_for_timeout(1_200)
    dialogs = page.locator(DIALOG).count()
    if dialogs:
        page.locator(OVERLAY).first.click(position={"x": 5, "y": 5})
    return {"probe": probe, "dump": dump, "dialogs_after_dblclick": dialogs}


def check(result: dict, mobile: bool) -> list[str]:
    problems: list[str] = []
    samples = result["probe"]["samples"]
    if not samples:
        return ["面板没有出现（0 个采样）"]
    first, last = samples[0], samples[-1]
    heights = {s["h"] for s in samples}
    if max(heights) - min(heights) > 1:
        problems.append(f"内容渲染后外框高度变了：{sorted(heights)[:3]}…{sorted(heights)[-3:]}")
    if mobile:
        expect = last["vh"] - 56
        if abs(last["h"] - expect) > 1:
            problems.append(f"移动端高度 {last['h']} != 视口-56 ({expect})")
        if last["top"] < 35:
            problems.append(f"移动端面板顶到标题栏之上：top={last['top']}")
    else:
        if abs(last["w"] - 720) > 1:
            problems.append(f"桌面宽度 {last['w']} != 720")
        delta = abs((last["top"] + last["bottom"]) / 2 - last["vh"] / 2)
        if delta > 1.5:
            problems.append(f"桌面未居中：中心偏差 {delta:.1f}px")
    if result["dialogs_after_dblclick"] != 1:
        problems.append(f"双击后面板数 {result['dialogs_after_dblclick']} != 1（第二下把面板关了）")
    executed = result["dump"].get("resources") or []
    served = result.get("bundle", {}).get("served")
    if served and not any(served in url for url in executed):
        problems.append(f"浏览器执行的 bundle 与线上入口不一致：{executed}")
    return problems


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--mobile", action="store_true")
    args = parser.parse_args()
    env = harness.load_env()
    out: dict = {"bundle": harness.bundle_hashes(env), "url": env["MINDFS_LIVE_URL"], "mobile": args.mobile}
    viewport = {"width": 390, "height": 844} if args.mobile else {"width": 1440, "height": 900}
    with sync_playwright() as p:
        browser = p.chromium.launch()
        ctx = browser.new_context(viewport=viewport, service_workers="block")
        page = ctx.new_page()
        harness.login(page, env)
        prepare_board(page, args.mobile)
        card, total = pick_card(page)
        out["cards"] = total
        if card is None:
            print(json.dumps({**out, "error": "看板上没有任务卡"}, ensure_ascii=False, indent=1))
            return 1
        out["card_text"] = card.inner_text()[:120]
        page.evaluate(PROBE_JS)
        page.evaluate("() => { window.__panelProbe.clickAt = Math.round(performance.now()); }")
        card.click(position={"x": 40, "y": 30})
        out.update(measure(page, args.mobile))
        ctx.close()
        browser.close()
    out["problems"] = check(out, args.mobile)
    print(json.dumps(out, ensure_ascii=False, indent=1))
    return 1 if out["problems"] else 0


if __name__ == "__main__":
    sys.exit(main())
