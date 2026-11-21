"""输入框四项调整的浏览器验收（Playwright + pytest）。

跑法（与其它 e2e 一样，刻意不进常规门禁）：

    .venv/bin/python -m pytest e2e/test_task_composer_enter_and_layout.py -v -p no:cacheprovider

对应需求（2026-11-21）：
  1. 新建面板的「立即执行」去掉文字，只留图标，外观与发送键一致（圆角方形）；
  2. 详情面板发送框不再有「立即执行」，新建面板要有「选择工作流」；
  3. 桌面端 Enter 换行、Ctrl+Enter 发送；移动端沿用「回车发送」偏好；
  4. 详情对话框里输入区长高时把对话往上顶，下方胶囊不掉出视口。

源码契约见 web/tests/task-run-immediately.test.mjs、composer-bottom-pin.test.mjs、
shared-send-shortcut.test.mjs；这里验真实浏览器里的几何与交互。
"""

from __future__ import annotations

import json
import re
import time
import urllib.request

import pytest
from playwright.sync_api import Page, TimeoutError as PWTimeout

BOARD_TAB = "Board"
BOARD_REFRESH = '[data-onboarding="task-refresh"]'
VIEW_SWITCHER = '[data-onboarding="main-view-switcher"]'
WORKSPACE = '[data-onboarding="workspace"]'
CREATE_BUTTON = '[data-onboarding="task-create"]'

EDITOR = ".token-editor-input"
PANEL = ".mindfs-panel-dialog"
TOGGLE = '[data-testid="run-immediately-toggle"]'
# 建完任务会自动弹详情面板，它同样是 .mindfs-panel-dialog 且同样带编辑器，
# 所以「新建面板关没关」不能只看有没有编辑器。两个面板的结构性差异正好是需求 2：
# 「立即执行」只在新建面板里，详情面板没有 —— 用它区分，顺带把这条也钉住。
CREATE_PANEL = f"{PANEL}:has({TOGGLE})"
ACTIVITY_INPUT = '[data-testid="activity-input"]'
NAME_INPUT = 'input[placeholder]:visible'
WORKFLOW_SELECTOR = '[data-testid="workflow-selector"]'
QUEUED_RUN = '[data-testid="activity-run-queued"]'
EXECUTION_PILLS = ".task-detail-execution"

TASK_NAME = "输入框按键与布局验收任务 E2E"


def _post_json(url: str, payload: dict) -> dict:
    req = urllib.request.Request(
        url,
        data=json.dumps(payload).encode(),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=20) as resp:
        return json.loads(resp.read().decode() or "{}")


@pytest.fixture(scope="module")
def queued_task(api_url: str, user_query: str, project: dict) -> dict:
    """排队留言的任务：详情里既有排队气泡（派发入口），又有胶囊与输入框。"""
    root_id = str(project["id"])
    detail = _post_json(
        f"{api_url}/api/tasks?{user_query}",
        {
            "root_id": root_id,
            "input": "这是输入框按键与布局验收夹具，不需要执行。",
            "name": TASK_NAME,
            "create_worktree": False,
            "start_immediately": False,
        },
    )
    task = detail.get("task") or {}
    assert task.get("id"), f"创建任务失败：{detail}"
    return {"id": task["id"], "root_id": root_id}


# ── 导航 ──────────────────────────────────────────────────────────────────


def _reload(page: Page) -> None:
    page.reload(wait_until="domcontentloaded")
    page.wait_for_selector(WORKSPACE, timeout=60_000)
    try:
        page.click('button:has-text("Skip tour")', timeout=3_000)
    except PWTimeout:
        pass


def _show_board(page: Page) -> None:
    tab = page.locator(VIEW_SWITCHER).first.get_by_role("tab", name=BOARD_TAB, exact=True)
    if tab.count():
        tab.first.click()
    # 看板工具栏只在看板主视图里出现；等它出现再刷新，别用固定 sleep 赌。
    page.wait_for_selector(BOARD_REFRESH, timeout=30_000)
    page.click(BOARD_REFRESH)


def _show_board_mobile(page: Page) -> None:
    """窄屏：切视图的入口在左抽屉里，切完点遮罩把抽屉收起来。"""
    # header 两侧的抽屉开关：左栏收起时是「Expand …」。
    page.locator('button[aria-label^="Expand"]').first.click()
    page.locator(VIEW_SWITCHER).first.get_by_role("tab", name=BOARD_TAB, exact=True).click()
    page.wait_for_selector(BOARD_REFRESH, timeout=30_000)
    # 切视图不会自动收抽屉（只有点文件/会话才收），点抽屉右侧的遮罩收起它；
    # 抽屉宽 75vw，所以最右边这条竖带一定落在遮罩上。
    page.mouse.click(page.viewport_size["width"] - 12, 400)
    page.wait_for_timeout(400)
    page.click(BOARD_REFRESH)


def _open_create_panel(page: Page):
    page.locator(f"{CREATE_BUTTON}:visible").first.click()
    dialog = page.locator(f"{CREATE_PANEL}:visible").first
    dialog.wait_for(state="visible", timeout=20_000)
    dialog.locator(EDITOR).first.wait_for(state="visible", timeout=20_000)
    return dialog


def _open_task(page: Page) -> None:
    card = page.locator("article", has=page.get_by_text(TASK_NAME, exact=True)).first
    card.wait_for(state="visible", timeout=20_000)
    card.get_by_text(TASK_NAME, exact=True).first.click()
    page.wait_for_selector(f"{ACTIVITY_INPUT} {EDITOR}", timeout=20_000)


def _editor_text(page: Page, scope: str) -> str:
    return page.locator(f"{scope} {EDITOR}").first.inner_text()


def _line_count(text: str) -> int:
    return len([line for line in re.split(r"\n+", text.strip()) if line.strip()])


def _style(page: Page, selector: str, prop: str) -> str:
    return page.locator(selector).first.evaluate(
        "(el, p) => getComputedStyle(el).getPropertyValue(p)", prop
    )


# ── 1/2/3：新建任务面板 ────────────────────────────────────────────────────


def test_create_panel_icon_toggle_workflow_selector_and_enter(
    authed_page: Page, queued_task: dict
) -> None:
    page = authed_page
    page.set_viewport_size({"width": 1440, "height": 900})
    _reload(page)
    _show_board(page)
    dialog = _open_create_panel(page)

    toggle = dialog.locator(TOGGLE).first
    toggle.wait_for(state="visible", timeout=10_000)
    box = toggle.bounding_box()
    assert box is not None
    # 图标方形：与发送键同一套 28×28 / 8px 圆角。
    assert abs(box["width"] - 28) <= 1, f"开关宽度 {box['width']}，应为 28"
    assert abs(box["height"] - 28) <= 1, f"开关高度 {box['height']}，应为 28"
    assert _style(page, f"{PANEL}:visible {TOGGLE}", "border-radius") == "8px"
    # 按钮里没有可见文字（title/aria-label 仍在，供读屏与鼠标悬停）。
    assert toggle.inner_text().strip() == "", f"开关里还有文字：{toggle.inner_text()!r}"
    assert toggle.get_attribute("aria-label"), "开关必须保留可读标签"

    # 控件行：开关、选择工作流、附件、发送同一排（纵向中心重合）。
    workflow = dialog.locator(WORKFLOW_SELECTOR).first
    workflow.wait_for(state="visible", timeout=10_000)
    send = toggle.locator("xpath=..").locator("button").last
    assert send.is_visible(), "控件行里应有发送键"
    row_center = box["y"] + box["height"] / 2
    for label, loc in (("工作流选择器", workflow), ("发送键", send)):
        other = loc.bounding_box()
        assert other is not None, f"{label} 不可见"
        assert abs((other["y"] + other["height"] / 2) - row_center) <= 3, (
            f"{label} 与开关不在同一排：{other}"
        )

    # 背景与发送键一致：打点字让发送键进入可用态，两者应同底色。
    dialog.locator(EDITOR).first.click()
    page.keyboard.type("对齐底色")
    page.wait_for_timeout(200)
    assert send.is_enabled(), "输入内容后发送键应可用"
    assert toggle.evaluate(
        "(el) => getComputedStyle(el).backgroundColor"
    ) == send.evaluate("(el) => getComputedStyle(el).backgroundColor"), (
        "开关与发送键底色不一致"
    )

    # 选择工作流：点开有菜单，选「自动」后关闭。
    workflow.locator('[data-testid="workflow-selector-trigger"]').first.click()
    menu = page.locator('[data-testid="workflow-menu"]:visible').first
    menu.wait_for(state="visible", timeout=10_000)
    menu.locator('[data-testid="workflow-choice-auto"]').first.click()
    page.wait_for_timeout(200)
    assert not page.locator('[data-testid="workflow-menu"]:visible').count(), "菜单没关"

    # 桌面端回车 = 换行：面板不关、文字多一行。
    page.keyboard.press("End")
    page.keyboard.press("Enter")
    page.keyboard.type("第二行")
    page.wait_for_timeout(200)
    text = _editor_text(page, f"{PANEL}:visible")
    assert _line_count(text) >= 2, f"回车没有换行：{text!r}"
    assert page.locator(f"{PANEL}:visible").count() == 1, "回车把面板发出去了"

    # Ctrl+Enter = 发送：真的发出 POST /api/tasks，新建面板随之关闭。
    # （建完是否自动弹详情面板由别的逻辑决定，所以只钉「新建面板本身没了」。）
    with page.expect_response(
        lambda r: re.search(r"/api/tasks(\?|$)", r.url) is not None and r.request.method == "POST",
        timeout=20_000,
    ):
        page.keyboard.press("Control+Enter")
    page.wait_for_timeout(800)
    assert not page.locator(f"{CREATE_PANEL}:visible").count(), (
        "Ctrl+Enter 没有发出首条留言（新建面板还在）"
    )


# ── 2/3/4：任务详情面板 ────────────────────────────────────────────────────


def test_detail_composer_has_no_toggle_and_keeps_pills_stable(
    authed_page: Page, queued_task: dict
) -> None:
    page = authed_page
    page.set_viewport_size({"width": 1440, "height": 900})
    _reload(page)
    _show_board(page)
    _open_task(page)

    composer = page.locator(ACTIVITY_INPUT).first
    # 2) 发送框里不再有「立即执行」，但保留选择工作流。
    assert composer.locator(TOGGLE).count() == 0, "详情发送框不该再出现立即执行开关"
    assert composer.locator(WORKFLOW_SELECTOR).count() == 1, "详情发送框缺少工作流选择器"

    # 派发入口搬到了排队气泡的页脚。
    queued_run = page.locator(QUEUED_RUN).first
    queued_run.wait_for(state="visible", timeout=10_000)
    qbox = queued_run.bounding_box()
    view = page.viewport_size
    assert qbox and qbox["y"] + qbox["height"] <= view["height"] + 1, "派发按钮在视口外"

    # 4) 输入区长高时对话往上顶，胶囊不掉出去。
    pills = page.locator(EXECUTION_PILLS).first
    pills.wait_for(state="visible", timeout=10_000)
    before = pills.bounding_box()
    composer_before = composer.bounding_box()
    assert before and composer_before

    editor = page.locator(f"{ACTIVITY_INPUT} {EDITOR}").first
    editor.click()
    page.keyboard.type("第一行")
    for i in range(2, 8):
        page.keyboard.press("Enter")
        page.keyboard.type(f"第{i}行")
    page.wait_for_timeout(600)

    after = pills.bounding_box()
    composer_after = composer.bounding_box()
    assert after and composer_after
    # 输入区确实长高了（否则这条用例什么都没验）。
    assert composer_after["height"] > composer_before["height"] + 10, (
        f"输入区没长高：{composer_before['height']} -> {composer_after['height']}"
    )
    # 胶囊原地不动（允许 4px 像素抖动）。
    assert abs(after["y"] - before["y"]) <= 4, (
        f"胶囊被顶下去了：{before['y']} -> {after['y']}"
    )
    assert after["y"] + after["height"] <= view["height"] + 1, "胶囊掉出视口"
    # 输入区向上长：顶边上升，底边基本不动。
    assert composer_after["y"] < composer_before["y"] - 10, (
        f"输入区没有向上生长：{composer_before['y']} -> {composer_after['y']}"
    )
    # 回车没有把留言发出去（还停在输入框里）。
    assert _line_count(_editor_text(page, ACTIVITY_INPUT)) >= 3
    assert page.locator(f"{PANEL}:visible").count() == 1, "详情面板被回车关掉了"


# ── 3：移动端 ─────────────────────────────────────────────────────────────


def test_mobile_create_panel_keeps_send_usable(authed_page: Page) -> None:
    """390×844：图标开关在控件行里，回车换行，发送键始终可用。

    窄屏下左栏是抽屉、切视图的入口只在抽屉里，所以按真机路径走：窄屏加载（两栏默认
    收起、主视图是工作台）→ 打开左抽屉 → 点看板 → 点遮罩收起抽屉。
    """
    page = authed_page
    page.set_viewport_size({"width": 390, "height": 844})
    try:
        _reload(page)
        _show_board_mobile(page)
        dialog = _open_create_panel(page)

        toggle = dialog.locator(TOGGLE).first
        toggle.wait_for(state="visible", timeout=10_000)
        box = toggle.bounding_box()
        assert box and abs(box["width"] - 28) <= 1 and abs(box["height"] - 28) <= 1
        assert box["x"] >= 0 and box["x"] + box["width"] <= 390, "开关横向溢出"
        assert box["y"] + box["height"] <= 844, "开关在视口外"
        assert dialog.locator(WORKFLOW_SELECTOR).first.is_visible(), "移动端缺工作流选择器"

        dialog.locator(EDITOR).first.click()
        page.keyboard.type("移动端第一行")
        page.keyboard.press("Enter")
        page.keyboard.type("第二行")
        page.wait_for_timeout(200)
        text = _editor_text(page, f"{PANEL}:visible")
        assert _line_count(text) >= 2, f"移动端默认应回车换行：{text!r}"

        send = toggle.locator("xpath=..").locator("button").last
        assert send.is_enabled(), "移动端回车换行时发送键必须可用"
        sbox = send.bounding_box()
        assert sbox and sbox["x"] + sbox["width"] <= 390, "发送键在视口外"
    finally:
        page.set_viewport_size({"width": 1440, "height": 900})
