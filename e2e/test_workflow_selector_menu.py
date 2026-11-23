"""移动端工作流两级弹层的避让验收（Playwright + pytest）。

跑法（与其它 e2e 用例一致，刻意不进常规门禁）：

    .venv/bin/python -m pytest e2e/test_workflow_selector_menu.py -v -p no:cacheprovider

用户报障：新建任务面板与任务详情面板里，工作流的两级弹出在窄屏上被裁掉，
点不到一级三态或二级列表。这里验的是**真实浏览器里四边都在可视视口内**
（visualViewport，软键盘压矮后的那块），而不是源码字符串。

线上 7331 全程不碰：conftest 用独立 XDG_CONFIG_HOME + 随机端口起隔离实例。
"""

import json
import urllib.request

import pytest
from playwright.sync_api import Page, TimeoutError as PWTimeout

BOARD_TAB = "Board"
BOARD_REFRESH = '[data-onboarding="task-refresh"]'
VIEW_SWITCHER = '[data-onboarding="main-view-switcher"]'
WORKSPACE = '[data-onboarding="workspace"]'
CREATE_BUTTON = '[data-onboarding="task-create"]'
PANEL = ".mindfs-panel-dialog"
ACTIVITY_INPUT = '[data-testid="activity-input"]'
EDITOR = ".token-editor-input"

TRIGGER = '[data-testid="workflow-selector-trigger"]'
MENU = '[data-testid="workflow-menu"]'
MANUAL = '[data-testid="workflow-choice-manual"]'
MANUAL_PANEL = '[data-testid="workflow-manual-panel"]'
WORKFLOW_ROW = "[data-workflow-id]"

TASK_NAME = "工作流弹层验收任务 E2E"
VIEWPORTS = [(375, 667), (360, 640)]


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
def pending_task(api_url: str, user_query: str, project: dict) -> dict:
    """建一张停在前台的任务卡：详情面板里才有输入框与工作流选择器。"""
    root_id = str(project["id"])
    detail = _post_json(
        f"{api_url}/api/tasks?{user_query}",
        {
            "root_id": root_id,
            "input": "这条任务是工作流弹层验收夹具，不需要真的执行。",
            "name": TASK_NAME,
            "create_worktree": False,
            "start_immediately": False,
        },
    )
    task = detail.get("task") or {}
    assert task.get("id"), f"创建任务失败：{detail}"
    return {"id": task["id"], "root_id": root_id}


# ── 浏览器导航辅助（与 test_task_composer_mentions.py 同源） ─────────────────


def _reload_for_loaded_board(page: Page) -> None:
    page.reload(wait_until="domcontentloaded")
    page.wait_for_selector(WORKSPACE, timeout=60_000)
    try:
        page.click('button:has-text("Skip tour")', timeout=3_000)
    except PWTimeout:
        pass


def _show_board(page: Page) -> None:
    (
        page.locator(VIEW_SWITCHER)
        .first.get_by_role("tab", name=BOARD_TAB, exact=True)
        .click()
    )
    page.wait_for_selector(WORKSPACE, timeout=20_000)
    page.click(BOARD_REFRESH)


def _open_task(page: Page) -> None:
    editor = page.locator(f"{ACTIVITY_INPUT} {EDITOR}").first
    if editor.count() and editor.is_visible():
        return  # reload 后可能已经是选中态，再点卡片反而会关掉详情面板
    card = page.locator("article", has=page.get_by_text(TASK_NAME, exact=True)).first
    card.wait_for(state="visible", timeout=20_000)
    card.get_by_text(TASK_NAME, exact=True).first.click()
    try:
        editor.wait_for(state="visible", timeout=5_000)
    except PWTimeout:
        # 点到的正是已选中的卡（选中态未必恢复出输入框）：再点一次打开。
        card.get_by_text(TASK_NAME, exact=True).first.click()
        editor.wait_for(state="visible", timeout=20_000)


# 菜单矩形 + 每一行「中心点真的能点到」+ 二级列表的滚动状态。
# 量 visualViewport 而不是 window.innerHeight：后者在软键盘弹起时不缩小。
MENU_GEOMETRY_JS = """
() => {
  const menu = document.querySelector('[data-testid="workflow-menu"]');
  if (!menu) return { error: "menu missing" };
  const vv = window.visualViewport;
  const view = vv
    ? { left: vv.offsetLeft, top: vv.offsetTop,
        right: vv.offsetLeft + vv.width, bottom: vv.offsetTop + vv.height }
    : { left: 0, top: 0, right: window.innerWidth, bottom: window.innerHeight };
  const r = menu.getBoundingClientRect();
  const selectors = [
    '[data-testid="workflow-choice-auto"]',
    '[data-testid="workflow-choice-disabled"]',
    '[data-testid="workflow-choice-manual"]',
    '[data-workflow-id]',
  ];
  const rows = selectors.flatMap((sel) =>
    [...document.querySelectorAll(sel)].map((row) => {
      const b = row.getBoundingClientRect();
      const x = b.left + Math.min(b.width / 2, 20);
      const y = (b.top + b.bottom) / 2;
      const hit = document.elementFromPoint(x, y);
      return {
        sel,
        label: (row.textContent || "").trim().slice(0, 20),
        rect: { left: b.left, top: b.top, right: b.right, bottom: b.bottom },
        hit: !!hit && (hit === row || row.contains(hit)),
      };
    })
  );
  const list = document.querySelector('[data-testid="workflow-manual-panel"]');
  return {
    view,
    rect: { left: r.left, top: r.top, right: r.right, bottom: r.bottom },
    rows,
    listScroll: list
      ? { scrollHeight: list.scrollHeight, clientHeight: list.clientHeight,
          scrollTop: list.scrollTop, top: list.getBoundingClientRect().top }
      : null,
  };
}
"""


def _assert_geometry(page: Page) -> dict:
    geometry = page.evaluate(MENU_GEOMETRY_JS)
    assert not geometry.get("error"), geometry
    view = geometry["view"]
    rect = geometry["rect"]
    # 1px 容差给边框与小数像素；超出即说明菜单仍会跑出屏幕。
    assert rect["left"] >= view["left"] - 1, geometry
    assert rect["top"] >= view["top"] - 1, geometry
    assert rect["right"] <= view["right"] + 1, geometry
    assert rect["bottom"] <= view["bottom"] + 1, geometry
    assert len(geometry["rows"]) >= 3, geometry
    for row in geometry["rows"]:
        assert row["hit"], f"选项无法点击（被遮挡或在视口外）：{row}"
        assert row["rect"]["top"] >= view["top"] - 1, row
        assert row["rect"]["bottom"] <= view["bottom"] + 1, row
    return geometry


def _open_two_levels(trigger) -> None:
    trigger.click()
    page_menu = trigger.page.locator(MENU).first
    page_menu.wait_for(state="visible", timeout=10_000)
    trigger.page.locator(MANUAL).first.click()
    # 二级列表是异步拉取的；ResizeObserver 要在列表落盘后重新量一次。
    trigger.page.wait_for_timeout(500)


def _run_panel(page: Page, scope: str, prefix: str = "") -> None:
    scope_locator = f"{prefix} {scope}" if prefix else scope
    trigger = page.locator(f"{scope_locator} {TRIGGER}:visible").first
    trigger.wait_for(state="visible", timeout=20_000)
    _open_two_levels(trigger)
    geometry = _assert_geometry(page)

    # 二级列表很长时必须内部滚动，不能把整个菜单顶出屏幕。
    scroll = geometry.get("listScroll")
    if scroll and scroll["scrollHeight"] > scroll["clientHeight"]:
        page.locator(MANUAL_PANEL).first.evaluate(
            "(el) => { el.scrollTop = el.scrollHeight; }"
        )
        after = _assert_geometry(page)
        assert after["listScroll"]["scrollTop"] > 0, after
        last_row = page.locator(f"{MANUAL_PANEL} {WORKFLOW_ROW}").last
        assert last_row.is_visible(), "滚到底后最后一条工作流仍不可见"

    # 有工作流时点一条：菜单关闭且触发按钮出现非默认状态圆点。
    rows = page.locator(f"{MANUAL_PANEL} {WORKFLOW_ROW}")
    if rows.count():
        rows.first.click()
        page.wait_for_selector(MENU, state="detached", timeout=10_000)
        assert page.locator(f"{scope_locator} " + '[data-testid="workflow-selection-dot"]').first.is_visible()
    else:
        # 没有可用 workflow 时，加载分支的空提示也必须在视口内可点。
        assert page.locator(MANUAL_PANEL).first.is_visible()

    page.keyboard.press("Escape")


def _run_viewport(page: Page, width: int, height: int, pending_task: dict) -> None:
    page.set_viewport_size({"width": width, "height": height})
    page.set_default_timeout(15_000)
    try:
        # 1) 新建任务面板
        _reload_for_loaded_board(page)
        try:
            _show_board(page)
        except PWTimeout:
            pass
        page.locator(f"{CREATE_BUTTON}:visible").first.click()
        dialog = page.locator(f"{PANEL}:visible").first
        dialog.wait_for(state="visible", timeout=20_000)
        dialog.locator(EDITOR).first.wait_for(state="visible", timeout=20_000)
        _run_panel(page, PANEL)

        # 2) 任务详情输入框（reload 顺便关掉面板）
        _reload_for_loaded_board(page)
        try:
            # 上一轮可能把 mainView 留在详情/编辑器；显式切回看板再找卡片。
            _show_board(page)
        except PWTimeout:
            pass
        _open_task(page)
        _run_panel(page, ACTIVITY_INPUT)
    finally:
        # authed_page 是会话级夹具，别把窄屏留给后面的用例。
        page.set_viewport_size({"width": 1440, "height": 900})


def test_workflow_menu_is_fully_visible_on_mobile(
    authed_page: Page, pending_task: dict
) -> None:
    """新建与详情两个入口：375×667 与 360×640 下两级展开都不出可视视口。

    两条入口放进同一个用例：authed_page 是会话级夹具，拆成两个用例在同一
    pytest 进程里连跑时，第二个用例会在 fixture 交接处卡住（浏览器仍活着，
    但 Playwright 同步 API 不再推进）；单用例里顺序跑两个入口稳定通过。
    """
    for width, height in VIEWPORTS:
        _run_viewport(authed_page, width, height, pending_task)
