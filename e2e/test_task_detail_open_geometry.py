"""任务详情面板打开路径的浏览器端到端验收（Playwright + pytest）。

钉两件用户 2026-11-22 报的事：

1. **双击卡片不该把面板刚打开就关掉**。实测双击的第一下打开面板，第二下落在
   刚挂载的遮罩上，面板数从 1 变 0 —— 用户看到的是「要点两下才打开」。修复后
   双击仍然只剩一个面板（遮罩在面板刚挂载的 350ms 内不吃点击）。
2. **外框固定尺寸、默认居中**。打开第一帧与数据到齐后的高度必须一致，
   不再出现「贴底 + 向上展开」；桌面 720×min(88dvh, 视口-48)；移动端宽度
   100%、高度 = 可视视口 - 56。

跑法：

    E2E_SKIP_BUILD=1 .venv/bin/python -m pytest e2e/test_task_detail_open_geometry.py -v
"""

from __future__ import annotations

import json
import urllib.request

from playwright.sync_api import Page

BOARD_TAB = "Board"
BOARD_REFRESH = '[data-onboarding="task-refresh"]'
VIEW_SWITCHER = '[data-onboarding="main-view-switcher"]'
WORKSPACE = '[data-onboarding="workspace"]'
PILLS = ".task-detail-execution"
ACTION_ROW = '[data-testid="task-detail-actions"]'
DIALOG = ".mindfs-panel-dialog"


def _post_json(url: str, payload: dict) -> dict:
    req = urllib.request.Request(
        url,
        data=json.dumps(payload).encode(),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=20) as resp:
        return json.loads(resp.read().decode() or "{}")


def _create_task(api_url: str, user_query: str, root_id: str, name: str) -> dict:
    detail = _post_json(
        f"{api_url}/api/tasks?{user_query}",
        {
            "root_id": root_id,
            "input": f"{name}：打开路径验收夹具，不需要真的执行。",
            "name": name,
            "create_worktree": False,
            "start_immediately": False,
        },
    )
    task = detail.get("task") or {}
    assert task.get("id"), f"创建任务失败：{detail}"
    return task


def _prepare_board(page: Page) -> None:
    page.reload(wait_until="domcontentloaded")
    page.wait_for_selector(WORKSPACE, timeout=60_000)
    try:
        page.click('button:has-text("Skip tour")', timeout=3_000)
    except Exception:  # noqa: BLE001 - 引导只出现一次
        pass
    # 窄屏默认把侧栏/视图切换器收起，先展开再点 Board。
    expand = page.locator('button[aria-label^="Expand"]')
    if expand.count():
        expand.first.click()
    page.locator(VIEW_SWITCHER).first.wait_for(state="visible", timeout=30_000)
    page.locator(VIEW_SWITCHER).first.get_by_role("tab", name=BOARD_TAB, exact=True).click()
    page.wait_for_selector(BOARD_REFRESH, timeout=30_000)
    # 抽屉宽 75vw，最右边这条竖带落在遮罩上，点它收起抽屉。
    page.mouse.click(page.viewport_size["width"] - 12, 400)
    page.wait_for_timeout(500)


def _card(page: Page, name: str):
    card = page.locator("article", has=page.get_by_text(name, exact=True)).first
    card.wait_for(state="visible", timeout=20_000)
    return card


def _instrument(page: Page) -> None:
    """从点击前开始按 50ms 采样面板矩形，记录首次出现与最终尺寸。"""
    page.evaluate(
        """() => {
          window.__panelProbe = { first: null, last: null, centerDeltas: [], clickAt: null };
          const seen = () => {
            const d = document.querySelector('.mindfs-panel-dialog');
            if (!d) return;
            const r = d.getBoundingClientRect();
            const sample = {
              t: performance.now(),
              width: Math.round(r.width * 10) / 10,
              height: Math.round(r.height * 10) / 10,
              top: Math.round(r.top * 10) / 10,
              left: Math.round(r.left * 10) / 10,
              vw: innerWidth,
              vh: innerHeight,
            };
            if (!window.__panelProbe.first) window.__panelProbe.first = sample;
            window.__panelProbe.last = sample;
            window.__panelProbe.centerDeltas.push(
              Math.abs(sample.top - (sample.vh - sample.height) / 2),
            );
          };
          const mo = new MutationObserver(seen);
          mo.observe(document.body, { childList: true, subtree: true });
          window.__panelProbeTimer = setInterval(seen, 50);
          seen();
        }"""
    )


def _probe(page: Page) -> dict:
    return page.evaluate(
        "() => { clearInterval(window.__panelProbeTimer); return window.__panelProbe; }"
    )


def _close(page: Page) -> None:
    if page.locator(DIALOG).count() == 0:
        return
    page.locator(".mindfs-panel-overlay").first.click(position={"x": 5, "y": 5})
    page.wait_for_selector(DIALOG, state="detached", timeout=10_000)
    page.wait_for_timeout(400)


def test_double_click_opens_once_and_frame_is_fixed_centered(
    authed_page: Page, api_url: str, user_query: str, project: dict
) -> None:
    page = authed_page
    name = "打开路径验收 · 桌面"
    _create_task(api_url, user_query, str(project["id"]), name)
    _prepare_board(page)

    card = _card(page, name)
    _instrument(page)
    # 用户报的「要点两下」：双击第一下打开、第二下打到新遮罩把它关掉。
    page.evaluate("() => { window.__panelProbe.clickAt = performance.now(); }")
    card.get_by_text(name, exact=True).first.dblclick()
    page.wait_for_selector(ACTION_ROW, timeout=20_000)
    page.wait_for_timeout(1_200)

    assert page.locator(DIALOG).count() == 1, "双击之后面板必须仍然打开"
    assert page.locator(ACTION_ROW).first.is_visible(), "双击后动作行仍可见"

    data = _probe(page)
    first, last = data["first"], data["last"]
    assert first and last, f"没采到面板矩形：{data}"
    # 第一帧就是最终尺寸：不再随异步内容从底部往上长。
    assert abs(first["height"] - last["height"]) <= 1, f"面板高度在打开过程中变了：{first} → {last}"
    assert abs(first["top"] - last["top"]) <= 1, f"面板位置在打开过程中变了：{first} → {last}"
    assert abs(last["width"] - 720) <= 1, f"桌面宽度应为 720，实际 {last}"
    assert abs((last["vh"] - last["height"]) / 2 - last["top"]) <= 1.5, f"面板没有居中：{last}"
    assert data["centerDeltas"] and max(data["centerDeltas"]) <= 1.5, f"打开过程有位移：{data}"
    assert first["t"] - data["clickAt"] < 500, f"面板挂载太慢：首次出现 +{first['t'] - data['clickAt']:.0f}ms"

    # 单次刻意点遮罩仍然要能关闭（打开守卫只覆盖刚挂载的 350ms）。
    page.wait_for_timeout(400)
    page.locator(".mindfs-panel-overlay").first.click(position={"x": 5, "y": 5})
    page.wait_for_selector(DIALOG, state="detached", timeout=10_000)


def test_mobile_frame_is_fixed_and_fits_visible_viewport(
    authed_page: Page, api_url: str, user_query: str, project: dict
) -> None:
    page = authed_page
    page.set_viewport_size({"width": 390, "height": 844})
    name = "打开路径验收 · 移动端"
    _create_task(api_url, user_query, str(project["id"]), name)
    _prepare_board(page)

    card = _card(page, name)
    _instrument(page)
    page.evaluate("() => { window.__panelProbe.clickAt = performance.now(); }")
    card.get_by_text(name, exact=True).first.click()
    page.wait_for_selector(ACTION_ROW, timeout=20_000)
    page.wait_for_selector(PILLS, timeout=20_000)
    page.wait_for_timeout(1_000)

    data = _probe(page)
    first, last = data["first"], data["last"]
    assert first and last, f"没采到面板矩形：{data}"
    assert abs(first["height"] - last["height"]) <= 1, f"移动端高度也必须在打开时固定：{first} → {last}"
    assert abs(last["height"] - (last["vh"] - 56)) <= 1, f"移动端高度应为视口-56：{last}"
    assert last["width"] <= 390 - 24 + 1, f"移动端不该横向溢出：{last}"
    assert last["top"] >= 36 and last["top"] + last["height"] <= last["vh"] - 11, f"面板越出可视区：{last}"
    no_hscroll = page.evaluate("() => document.documentElement.scrollWidth <= innerWidth + 1")
    assert no_hscroll, "面板打开后页面不应出现横向滚动"
    # 短对话时输入框仍被顶到底部（空占位只吃剩余空间，不把胶囊推到屏幕外）。
    assert page.locator(PILLS).first.is_visible(), "胶囊仍要在首屏可见"

    # authed_page 是会话级夹具，别把 390 宽留给后面的用例。
    page.set_viewport_size({"width": 1440, "height": 900})