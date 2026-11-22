"""线上「从任务详情跳进会话」只读探针：跳转后详情面板必须自己关掉。

症状（本轮修复前）：详情里的「打开会话」（活动卡、执行详情里的节点行、工作流会话按钮）
点下去，会话已经在后台切好、URL 也写了 `?session=`，详情面板却还盖在上面，用户看不到
刚跳过去的会话。

判据（四样同时成立才算过）：
1. 点击后 `.mindfs-panel-dialog` 消失；
2. URL 出现 `?session=<非空>`（导航确实发生了，不是面板被误关）；
3. 点击前确实只有一个面板、页面在看板（前置状态没跑偏）；
4. 浏览器实际执行的 bundle 就是服务端入口那份（并发会话覆盖过共享 PREFIX，见
   panel_geometry.py 的教训）。

用法（在仓库根）：
    .venv/bin/python e2e/live/detail_to_session.py            # 桌面 1440x900
    .venv/bin/python e2e/live/detail_to_session.py --mobile   # 手机 390x844

只读：登录 → 看板 → 打开任务卡 → 点「打开会话」→ 断言 → 结束。不发留言、不点写操作。
"""

from __future__ import annotations

import argparse
import json
import sys
from urllib.parse import parse_qs, urlparse

import harness
from panel_geometry import DIALOG, OVERLAY, pick_card, prepare_board
from playwright.sync_api import sync_playwright

# 详情面板里所有「打开会话」入口（G-NY）：活动卡页脚、直连节点行、工作流节点行。
OPEN_BUTTONS = (
    '[data-testid="activity-open-session"], '
    '[data-testid="direct-node-open-session"], '
    '[data-testid^="open-session-"]'
)
RUN_VIEW_TOGGLE = '[data-testid="run-view-toggle"]'


def scroll_panel_to_bottom(page) -> None:
    page.evaluate(
        """() => {
          const body = document.querySelector('.mindfs-panel-body');
          if (body) body.scrollTop = body.scrollHeight;
        }"""
    )
    page.wait_for_timeout(400)


def find_open_button(page):
    """先找已经渲染出来的入口；没有再展开「执行详情」找节点行/工作流会话按钮。"""
    for _ in range(2):
        scroll_panel_to_bottom(page)
        loc = page.locator(OPEN_BUTTONS)
        if loc.count():
            return loc.first
        toggle = page.locator(RUN_VIEW_TOGGLE)
        if toggle.count() and toggle.first.get_attribute("data-open") != "true":
            toggle.first.click()
            page.wait_for_timeout(600)
            continue
        break
    return None


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--mobile", action="store_true")
    parser.add_argument("--max-cards", type=int, default=10)
    args = parser.parse_args()
    env = harness.load_env()
    out: dict = {"bundle": harness.bundle_hashes(env), "url": env["MINDFS_LIVE_URL"], "mobile": args.mobile}
    viewport = {"width": 390, "height": 844} if args.mobile else {"width": 1440, "height": 900}
    problems: list[str] = []
    with sync_playwright() as p:
        browser = p.chromium.launch()
        ctx = browser.new_context(viewport=viewport, service_workers="block")
        page = ctx.new_page()
        harness.login(page, env)
        prepare_board(page, args.mobile)
        cards = page.locator("article")
        total = cards.count()
        out["cards"] = total
        tried: list[str] = []
        clicked = False
        for i in range(min(total, args.max_cards)):
            card = cards.nth(i)
            label = card.inner_text()[:60].replace("\n", " ")
            card.click(position={"x": 40, "y": 30})
            try:
                page.wait_for_selector(DIALOG, timeout=6_000)
            except Exception:  # noqa: BLE001 - 卡片没打开就算这张不适用
                tried.append(f"{label} :: 面板未打开")
                continue
            page.wait_for_timeout(800)
            button = find_open_button(page)
            if button is None:
                tried.append(f"{label} :: 没有打开会话入口")
                page.locator(OVERLAY).first.click(position={"x": 5, "y": 5})
                page.wait_for_timeout(400)
                continue
            out["dialogs_before_click"] = page.locator(DIALOG).count()
            out["url_before"] = page.url
            out["card_text"] = label
            out["button_testid"] = button.get_attribute("data-testid")
            button.scroll_into_view_if_needed()
            button.click()
            page.wait_for_timeout(1_500)
            out["dialogs_after_click"] = page.locator(DIALOG).count()
            out["url_after"] = page.url
            out["session_param"] = (parse_qs(urlparse(page.url).query).get("session") or [""])[0]
            out["timeline_rows"] = page.locator("[data-timeline-key]").count()
            clicked = True
            break
        out["tried"] = tried
        executed = page.evaluate(
            "() => performance.getEntriesByType('resource')"
            ".map((e) => e.name).filter((n) => /\\/assets\\/index-[A-Za-z0-9_-]+\\.js/.test(n))"
        )
        out["executed_bundles"] = executed
        ctx.close()
        browser.close()

    if not clicked:
        problems.append("没有找到带「打开会话」入口的任务卡（无法验证跳转路径）")
    else:
        if out.get("dialogs_before_click") != 1:
            problems.append(f"点击前面板数 {out.get('dialogs_before_click')} != 1")
        if out.get("dialogs_after_click") != 0:
            problems.append(f"跳转后详情面板没关：仍有 {out.get('dialogs_after_click')} 个")
        if not out.get("session_param"):
            problems.append(f"URL 没有 session 参数：{out.get('url_after')}")
        if "root=" not in (out.get("url_after") or ""):
            problems.append(f"URL 丢了 root 参数：{out.get('url_after')}")
    served = (out.get("bundle") or {}).get("served")
    if served and not any(served in url for url in out.get("executed_bundles") or []):
        problems.append(f"浏览器执行的 bundle 与线上入口不一致：{out.get('executed_bundles')}")
    out["problems"] = problems
    print(json.dumps(out, ensure_ascii=False, indent=1))
    return 1 if problems else 0


if __name__ == "__main__":
    sys.exit(main())
